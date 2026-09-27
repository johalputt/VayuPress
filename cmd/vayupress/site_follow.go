// SPDX-License-Identifier: Apache-2.0

package main

// site_follow.go — a hosted domain whose website is a build of a repository.
//
// The domain names a GitHub repository, a branch and either a folder that is
// the site or nothing (the docs, rendered by internal/docsite, which is what
// vayupress.com and updates.vayupress.com are). The install looks at the branch
// every few minutes, and at once when asked; when it has moved it fetches what
// changed, builds, and deploys the result as the domain's bundle through the
// same customsite.Deploy an upload takes.
//
// The install reaches out rather than GitHub reaching in, as the release mirror
// does: no key that can write to production is kept in GitHub, and a site of
// any size is deployed by a push, which no connector call could carry.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/docsite"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
)

const (
	// siteFollowInterval is how long a push may wait to go live when nobody
	// asks for it sooner. A look that finds nothing costs one ref list, which
	// is not counted against GitHub's API allowance.
	siteFollowInterval = 5 * time.Minute
	// siteFollowTimeout bounds one sync and build: a first sync of a docs site
	// is a few hundred small files.
	siteFollowTimeout = 10 * time.Minute
)

// siteForge points the sync at a stand-in for GitHub. nil in production;
// tests set it, with a client that may reach the loopback server the
// production transport refuses.
type siteForge struct {
	Git, API, Raw string
	Client        *http.Client
}

// siteFollowRoot sits beside the uploaded sites under the data root, like the
// release mirror; the working copies are not a cache to be purged.
func siteFollowRoot() string {
	return filepath.Join(filepath.Dir(config.Cfg.MediaDir), "site-follow")
}

// followSource is the sync's view of what a domain follows.
func followSource(f domain.Follow) docsite.Source {
	return docsite.Source{Repo: f.Repo, Branch: f.Branch, Dir: f.Dir}
}

// validateFollow is the one check both the connector and the loop apply.
func validateFollow(f domain.Follow) error {
	if err := followSource(f).Valid(); err != nil {
		return err
	}
	switch {
	case f.Site != "" && f.Site != docsite.SiteUpdates:
		return errors.New(`site is empty (the product site) or "updates" (the release mirror's page)`)
	case f.Site != "" && f.Dir != "":
		return errors.New("a folder is served as it is; site chooses between the two docs sites, so give one or the other")
	}
	return nil
}

func (a *App) siteSyncer(d domain.Domain) (*docsite.Syncer, error) {
	if !isHexScope(d.ID) {
		return nil, errors.New("this domain's id cannot name a folder")
	}
	s := &docsite.Syncer{
		Dir:    filepath.Join(siteFollowRoot(), d.ID),
		Client: &http.Client{Timeout: siteFollowTimeout, Transport: safeOutboundTransport()},
	}
	if f := a.siteForge; f != nil {
		s.Git, s.API, s.Raw, s.Client = f.Git, f.API, f.Raw, f.Client
	}
	return s, nil
}

// siteSyncSlot lets one sync run per domain, and says whether one is.
type siteSyncSlot struct {
	mu      sync.Mutex
	running atomic.Bool
}

func (a *App) siteSyncSlot(d domain.Domain) *siteSyncSlot {
	v, _ := a.siteSyncs.LoadOrStore(d.ID, &siteSyncSlot{})
	return v.(*siteSyncSlot)
}

// errSiteSyncBusy answers a sync asked for while one is running for the domain.
var errSiteSyncBusy = errors.New("a build of this site is already running")

// syncFollowedSite looks at the branch the domain follows and, when it has
// moved, builds and deploys the site. One at a time per domain: two would
// share the working copy.
func (a *App) syncFollowedSite(ctx context.Context, d domain.Domain) (docsite.SyncState, error) {
	f, ok := d.Follow()
	if !ok {
		return docsite.SyncState{}, errors.New(d.Host + " does not follow a repository")
	}
	if err := validateFollow(f); err != nil {
		return docsite.SyncState{}, err
	}
	slot := a.siteSyncSlot(d)
	if !slot.mu.TryLock() {
		return docsite.SyncState{}, errSiteSyncBusy
	}
	slot.running.Store(true)
	defer func() {
		slot.running.Store(false)
		slot.mu.Unlock()
	}()
	s, err := a.siteSyncer(d)
	if err != nil {
		return docsite.SyncState{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, siteFollowTimeout)
	defer cancel()
	return s.Sync(ctx, followSource(f), func(repo fs.FS, st *docsite.SyncState) error {
		b, err := a.buildFollowedSite(ctx, d, f, repo, st)
		if err != nil {
			return err
		}
		data, err := b.Zip()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		m, err := customsite.Deploy(scopedBundleDir(d), zr, bundleBudget())
		if err != nil {
			return err
		}
		return a.servePublishedBundle(ctx, d, "site-follow", "built "+f.Repo+"@"+st.Commit[:8]+": "+itoaSafe(m.Files)+" file"+plural(m.Files))
	})
}

// buildFollowedSite turns the working copy into the site: a folder as it is,
// or the docs through the renderer.
func (a *App) buildFollowedSite(ctx context.Context, d domain.Domain, f domain.Follow, repo fs.FS, st *docsite.SyncState) (docsite.Bundle, error) {
	if f.Dir != "" {
		b, skipped, err := docsite.Static(repo, f.Dir)
		st.Skipped = skipped
		return b, err
	}
	home, download, err := a.docsiteLinks(ctx, d, f)
	if err != nil {
		return nil, err
	}
	return docsite.Build(docsite.Input{
		Repo: repo, Commit: st.Commit, Source: "https://github.com/" + f.Repo, Synced: st.Synced,
		Install: a.docsiteInstall(ctx), Updates: st.Updates, Assets: embeddedStaticFS,
		Site: f.Site, Home: home, Download: download,
	})
}

// docsiteLinks finds the two addresses the docs sites link each other by,
// from this install's own domains rather than from a setting that could name
// a site that is not there: the product site is the domain that follows the
// docs as the product site, and Download opens the domain serving the mirror.
func (a *App) docsiteLinks(ctx context.Context, d domain.Domain, f domain.Follow) (home, download string, err error) {
	all, err := a.domains.List(ctx)
	if err != nil {
		return "", "", err
	}
	var product, mirror string
	for _, o := range all {
		if o.IsPrimary || o.Status != domain.StatusActive {
			continue
		}
		if of, ok := o.Follow(); ok && of.Dir == "" && of.Site == "" && product == "" {
			product = o.Host
		}
		if o.ReleaseMirror() && mirror == "" {
			mirror = o.Host
		}
	}
	if f.Site != docsite.SiteUpdates {
		if mirror != "" {
			download = "https://" + mirror + "/"
		}
		return "", download, nil
	}
	if product == "" {
		return "", "", errors.New("the mirror's page links back to the product site, and no domain on this install follows the docs as it")
	}
	return "https://" + product, "/", nil
}

// docsiteInstall is what the docs site says about the install serving it,
// read when the site is built.
func (a *App) docsiteInstall(ctx context.Context) docsite.Install {
	in := docsite.Install{Host: config.Cfg.Domain}
	_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM articles WHERE status='published' AND is_page=0`).Scan(&in.Posts)
	if all, err := a.domains.List(ctx); err == nil {
		for _, o := range all {
			if o.Status == domain.StatusActive {
				in.Sites++
			}
		}
	}
	return in
}

// servePublishedBundle is what follows every deploy of a domain's site, from
// the connector, an edit or a sync: the domain serves the bundle, pages cached
// from the old one go, and the log says who put it there.
//
// Switching the domain to serve it is part of publishing, not a separate step:
// a site deployed while the domain went on serving its blog is a control that
// did nothing, reported as success. Preserving, not replacing, so the business
// details typed into a template survive.
func (a *App) servePublishedBundle(ctx context.Context, d domain.Domain, actor, what string) error {
	cfg, err := scopedWebsiteConfigPreserving(d, "custom", "")
	if err != nil {
		return err
	}
	if err := a.domains.SetSite(ctx, d.ID, cfg); err != nil {
		return err
	}
	render.CachePurgeAll()
	dbpkg.AuditLog("vayudomains.website.bundle", actor, d.Host, what)
	return nil
}

// followedDomains lists the active hosted domains that follow a repository.
func (a *App) followedDomains(ctx context.Context) []domain.Domain {
	if a.domains == nil {
		return nil
	}
	all, err := a.domains.List(ctx)
	if err != nil {
		return nil
	}
	var out []domain.Domain
	for _, d := range all {
		if _, ok := d.Follow(); ok && !d.IsPrimary && d.Status == domain.StatusActive {
			out = append(out, d)
		}
	}
	return out
}

// startSiteFollow keeps followed sites current. Never in a Tor Space: the
// sync is a clearnet fetch, and the anti-leak rule (ADR-0141) closes them all.
func (a *App) startSiteFollow(done <-chan struct{}) {
	if config.Cfg.OnionMode || a.domains == nil {
		return
	}
	tick := func() {
		for _, d := range a.followedDomains(context.Background()) {
			a.syncFollowedSiteLogged(context.Background(), d)
		}
	}
	go func() {
		t := time.NewTicker(siteFollowInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				tick()
			}
		}
	}()
}

// syncFollowedSiteLogged syncs and puts what changed on the timeline: a new
// build, or a failure that is new. A failure every five minutes for the same
// reason is one line, not two hundred a day.
func (a *App) syncFollowedSiteLogged(ctx context.Context, d domain.Domain) {
	s, err := a.siteSyncer(d)
	if err != nil {
		return
	}
	before := s.State()
	st, err := a.syncFollowedSite(ctx, d)
	switch {
	case errors.Is(err, errSiteSyncBusy):
	case err != nil:
		if before.Err != err.Error() {
			logging.LogWarn("site-follow", d.Host+": the site was not rebuilt: "+err.Error())
		}
	case st.Commit != before.Commit:
		logging.LogInfo("site-follow", d.Host+": live from "+st.Source.Repo+"@"+st.Commit[:8])
	}
}

// kickSiteSync starts a sync in the background, tracked on bgWG, and returns a
// channel that closes when it is done.
func (a *App) kickSiteSync(d domain.Domain) <-chan struct{} {
	done := make(chan struct{})
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		defer close(done)
		a.syncFollowedSiteLogged(context.Background(), d)
	}()
	return done
}

// siteFollowReport is what the connector says a domain follows and how its
// last build went; nil for a domain that follows nothing.
func (a *App) siteFollowReport(d domain.Domain) map[string]any {
	f, ok := d.Follow()
	if !ok {
		return nil
	}
	rep := map[string]any{"repo": f.Repo, "branch": f.Branch, "dir": f.Dir, "site": f.Site}
	s, err := a.siteSyncer(d)
	if err != nil {
		return rep
	}
	st := s.State()
	if st.Source != followSource(f) {
		rep["state"] = "not built yet"
		return rep
	}
	if st.Commit != "" {
		rep["commit"] = st.Commit
		rep["built_at"] = st.Synced
	}
	if !st.Checked.IsZero() {
		rep["checked_at"] = st.Checked
	}
	if st.Err != "" {
		// The live site is the last one that built; this is why the branch's
		// newest commit is not.
		rep["error"] = st.Err
	}
	if len(st.Skipped) > 0 {
		rep["left_out"] = st.Skipped
	}
	if a.siteSyncSlot(d).running.Load() {
		rep["building"] = true
	}
	return rep
}

// errFollowedSite refuses a hand edit to a site that is a build of a
// repository. The next push would rebuild it without the edit, which would
// then be gone with nothing to say so.
func errFollowedSite(d domain.Domain, f domain.Follow) error {
	where := f.Repo + "@" + f.Branch
	if f.Dir != "" {
		where += ", folder " + f.Dir
	}
	return siteLookupError(d.Host + " is built from " + where + ", so a change is made there and published with sync_site. " +
		"To edit the live site by hand instead, stop following with follow_repository and an empty repo")
}
