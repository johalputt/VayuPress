// SPDX-License-Identifier: Apache-2.0

package main

// release_mirror.go — serving the release mirror from a hosted domain.
//
// update.Mirror holds and verifies releases; this file decides WHERE it answers,
// keeps it fed, and puts its state on the site's console. The mirror is a switch
// on one hosted domain (updates.vayupress.com for the official one), because the
// fallback client in every install asks a fixed hostname, and the page a person
// sees at that hostname is an ordinary uploaded bundle beside it.

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/update"
	"github.com/johalputt/vayupress/internal/vayushield/resilience"
)

const (
	// releaseMirrorInterval is how stale the mirror may get. Installs check
	// every six hours, so an hour keeps the mirror well ahead of them, and with
	// a conditional request an hour in which nothing was published costs GitHub
	// a single 304.
	releaseMirrorInterval = time.Hour
	// releaseMirrorSyncTimeout bounds one sync: ~60 MB per new release over a
	// slow route, with room to spare.
	releaseMirrorSyncTimeout = 10 * time.Minute
	// Each install fetches four files per update (binary, checksum, two
	// signatures), and a failed attempt retries. Twenty an hour per client is
	// several whole updates; a client pulling the binary in a loop is not an
	// install.
	releaseMirrorDownloadsPerHour = 20
	// Concurrent file transfers. Each is up to ~56 MB, and this box also serves
	// the operator's sites, so bandwidth is shared by admitting a few at a time
	// rather than letting a burst take the uplink.
	releaseMirrorSlots = 8
)

// releaseMirrorRoot sits beside the uploaded sites under the data root — not
// under CACHE_DIR, which is purged on content changes.
func releaseMirrorRoot() string {
	return filepath.Join(filepath.Dir(config.Cfg.MediaDir), "release-mirror")
}

// releaseMirrorServer is the app's handle on the mirror and its admission
// controls. nil on an App means the mirror does not exist in this process.
type releaseMirrorServer struct {
	m         *update.Mirror
	downloads *resilience.Limiter
	slots     chan struct{}
}

func newReleaseMirrorServer(root string) *releaseMirrorServer {
	return &releaseMirrorServer{
		m:         update.NewMirror(root, updateOwner, updateRepo),
		downloads: resilience.NewLimiter(releaseMirrorDownloadsPerHour/3600.0, releaseMirrorDownloadsPerHour, 1<<16),
		slots:     make(chan struct{}, releaseMirrorSlots),
	}
}

// isReleaseMirrorRequest reports whether r is a mirror-protocol request to a
// domain the operator switched the mirror on for.
//
// It is also the shield's bypass predicate for these paths, so it must stay
// cheap: a prefix test first, and then only the domain already resolved into
// the request context — no database read. The fallback client is a plain HTTP
// client and can never solve a browser challenge; the operator's own DENY
// verdicts are still applied before any bypass (vayushield.go).
func (a *App) isReleaseMirrorRequest(r *http.Request) bool {
	if a.relMirror == nil || config.Cfg.OnionMode || !update.IsMirrorPath(r.URL.Path) {
		return false
	}
	d, ok := activeDomain(r)
	return ok && !d.IsPrimary && d.Status == domain.StatusActive && d.ReleaseMirror() &&
		strings.EqualFold(stripPort(r.Host), d.Host)
}

// releaseMirrorMiddleware answers the mirror protocol on a mirror domain and
// passes every other request through untouched. It runs after the shield, so
// the operator's country and address refusals apply to it like anything else.
func (a *App) releaseMirrorMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.isReleaseMirrorRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		s := a.relMirror
		if update.IsMirrorDownloadPath(r.URL.Path) {
			key := auth.ClientIP(r)
			if a.vayuShield != nil {
				// Group an IPv6 client by its /64 exactly as the shield does, or
				// one routed prefix is 2^64 fresh budgets.
				key = a.vayuShield.EnforcementKey(key)
			}
			if !s.downloads.Allow(key) {
				w.Header().Set("Retry-After", "600")
				http.Error(w, "too many release downloads from this address — retry later", http.StatusTooManyRequests)
				return
			}
			select {
			case s.slots <- struct{}{}:
				defer func() { <-s.slots }()
			default:
				w.Header().Set("Retry-After", "60")
				http.Error(w, "the mirror is serving its maximum number of downloads — retry in a minute", http.StatusServiceUnavailable)
				return
			}
		}
		s.m.ServeHTTP(w, r)
	})
}

// releaseMirrorDomains lists the domains the mirror is switched on for.
func (a *App) releaseMirrorDomains(ctx context.Context) []domain.Domain {
	if a.domains == nil {
		return nil
	}
	all, err := a.domains.List(ctx)
	if err != nil {
		return nil
	}
	var out []domain.Domain
	for _, d := range all {
		if !d.IsPrimary && d.ReleaseMirror() {
			out = append(out, d)
		}
	}
	return out
}

// syncReleaseMirror runs one sync and logs its outcome. The state it leaves is
// what the console and the connector report; the log is for the timeline.
func (a *App) syncReleaseMirror(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, releaseMirrorSyncTimeout)
	defer cancel()
	client := &http.Client{Timeout: releaseMirrorSyncTimeout, Transport: safeOutboundTransport()}
	err := a.relMirror.m.Sync(ctx, client)
	switch {
	case errors.Is(err, update.ErrMirrorBusy):
	case err != nil:
		logging.LogWarn("release-mirror", "sync incomplete: "+err.Error())
	default:
		st := a.relMirror.m.State()
		logging.LogInfo("release-mirror", "in sync — holding "+strconv.Itoa(len(st.Releases))+" verified release"+plural(len(st.Releases))+", "+humanBytes(st.Bytes()))
	}
	return err
}

// startReleaseMirror keeps the mirror fed while any domain serves it. It never
// runs in a Tor Space: the sync is a clearnet fetch, and the anti-leak rule
// (ADR-0141) closes every one.
func (a *App) startReleaseMirror(done <-chan struct{}) {
	if a.relMirror == nil || config.Cfg.OnionMode {
		return
	}
	tick := func() {
		if len(a.releaseMirrorDomains(context.Background())) == 0 {
			return
		}
		_ = a.syncReleaseMirror(context.Background())
	}
	go func() {
		select {
		case <-done:
			return
		case <-time.After(2 * time.Minute):
		}
		tick()
		t := time.NewTicker(releaseMirrorInterval)
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

// kickReleaseMirrorSync starts a sync in the background, tracked on bgWG. A sync
// takes up to minutes and a request has thirty seconds, so the console polls the
// status instead of waiting on the response.
func (a *App) kickReleaseMirrorSync() {
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		_ = a.syncReleaseMirror(context.Background())
	}()
}

// handleOSDomainReleaseMirror switches the mirror on or off for one domain.
func (a *App) handleOSDomainReleaseMirror(w http.ResponseWriter, r *http.Request) {
	if !a.releaseMirrorAdminReady(w, r) {
		return
	}
	var body struct {
		On *flexBool `json:"on"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512)).Decode(&body); err != nil || body.On == nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", `send {"on": true} or {"on": false}`, "")
		return
	}
	d, err := a.domains.ByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "no such domain", "")
		return
	}
	on := body.On.Bool()
	if err := a.setReleaseMirror(r.Context(), d, on, dbpkg.AuditActor(r)); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "release-mirror-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"status": "ok", "on": on})
}

// setReleaseMirror is the one writer both the console and the connector use:
// store the switch, record who flipped it, and start the first sync when it
// goes on so the domain is not advertising an empty mirror for an hour.
func (a *App) setReleaseMirror(ctx context.Context, d domain.Domain, on bool, actor string) error {
	if err := a.domains.SetReleaseMirror(ctx, d.ID, on); err != nil {
		return err
	}
	dbpkg.AuditLog("vayudomains.release_mirror", actor, d.Host, "on="+strconv.FormatBool(on))
	if on {
		a.kickReleaseMirrorSync()
	}
	return nil
}

// handleOSDomainReleaseMirrorSync starts a sync now — only for a domain the
// mirror is switched on for. A sync with the switch off downloads and verifies
// ~120 MB that the domain does not serve, while the page it was pressed on went
// on reading "off": it looked like an action and changed nothing visible.
func (a *App) handleOSDomainReleaseMirrorSync(w http.ResponseWriter, r *http.Request) {
	if !a.releaseMirrorAdminReady(w, r) {
		return
	}
	d, err := a.domains.ByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "no such domain", "")
		return
	}
	if !d.ReleaseMirror() {
		writeAPIError(w, r, http.StatusConflict, "release-mirror-off",
			"Switch the mirror on for this domain first — tick the box and press Save, which starts the first sync.", "")
		return
	}
	a.kickReleaseMirrorSync()
	writeJSON(w, r, http.StatusAccepted, map[string]any{"status": "started"})
}

// handleOSReleaseMirrorStatus is the console's view: everything the public
// status carries plus the error text, which only the operator may read.
func (a *App) handleOSReleaseMirrorStatus(w http.ResponseWriter, r *http.Request) {
	if !a.releaseMirrorAdminReady(w, r) {
		return
	}
	writeJSON(w, r, http.StatusOK, a.releaseMirrorReport())
}

func (a *App) releaseMirrorAdminReady(w http.ResponseWriter, r *http.Request) bool {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return false
	}
	if a.domains == nil || a.relMirror == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "the release mirror is not available on this install", "")
		return false
	}
	if config.Cfg.OnionMode {
		writeAPIError(w, r, http.StatusConflict, "tor-space",
			"a Tor Space makes no clearnet requests, so it cannot fetch releases to mirror", "")
		return false
	}
	return true
}

// releaseMirrorReport is the operator-facing state, shared by the console and
// the connector so the two cannot describe the mirror differently.
type releaseMirrorReport struct {
	CheckedAt *time.Time            `json:"checked_at"`
	SyncedAt  *time.Time            `json:"synced_at"`
	LastError string                `json:"last_error,omitempty"`
	Bytes     int64                 `json:"bytes"`
	Releases  []releaseMirrorRelRow `json:"releases"`
}

type releaseMirrorRelRow struct {
	Version    string    `json:"version"`
	Prerelease bool      `json:"prerelease"`
	VerifiedAt time.Time `json:"verified_at"`
	Files      int       `json:"files"`
	// BinaryChecks is what the installable binary passed — the line that says
	// whether an install will take this release.
	BinaryChecks []string `json:"binary_checks"`
}

func (a *App) releaseMirrorReport() releaseMirrorReport {
	st := a.relMirror.m.State()
	rep := releaseMirrorReport{LastError: st.LastError, Bytes: st.Bytes(), Releases: []releaseMirrorRelRow{}}
	if !st.CheckedAt.IsZero() {
		t := st.CheckedAt
		rep.CheckedAt = &t
	}
	if !st.SyncedAt.IsZero() {
		t := st.SyncedAt
		rep.SyncedAt = &t
	}
	for _, rel := range st.Releases {
		row := releaseMirrorRelRow{Version: rel.Tag, Prerelease: rel.Prerelease, VerifiedAt: rel.VerifiedAt, Files: len(rel.Assets)}
		for _, f := range rel.Assets {
			if strings.EqualFold(f.Name, updateRepo) {
				row.BinaryChecks = f.Checks
			}
		}
		rep.Releases = append(rep.Releases, row)
	}
	return rep
}

// mcpReleaseMirror is get_site's view of the mirror for one domain.
func (a *App) mcpReleaseMirror(d domain.Domain) map[string]any {
	out := map[string]any{"on": d.ReleaseMirror()}
	if a.relMirror != nil && d.ReleaseMirror() {
		out["state"] = a.releaseMirrorReport()
	}
	return out
}

// releaseMirrorAccordion is the site console's row for the mirror.
func (a *App) releaseMirrorAccordion(d domain.Domain) string {
	return monAcc(saIcon("package"), "Release mirror", "Serve verified VayuPress updates to installs that cannot reach GitHub",
		a.releaseMirrorChip(d), false, a.releaseMirrorCard(d))
}

// releaseMirrorChip is the accordion's state while collapsed.
func (a *App) releaseMirrorChip(d domain.Domain) string {
	if !d.ReleaseMirror() || a.relMirror == nil {
		return chipFor(false, "", "off")
	}
	st := a.relMirror.m.State()
	switch {
	case st.LastError != "":
		return chipFor(false, "", "sync failing")
	case len(st.Releases) == 0:
		return chipFor(false, "", "waiting for first sync")
	default:
		return chipFor(true, "serving "+st.Releases[0].Tag, "")
	}
}

// releaseMirrorCard is the console card: what the switch does, the switch, and
// what the mirror holds right now.
func (a *App) releaseMirrorCard(d domain.Domain) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<div class="card">
  <h2 class="card-title">Release mirror</h2>
  <p class="text-sm muted">An install whose server cannot reach GitHub fetches its updates from
    <b>` + esc(update.OfficialMirror) + `</b> instead. Switched on, <b>` + esc(d.Host) + `</b> answers
    those requests from releases this install has downloaded and verified itself — the same
    signature and checksum checks an install runs before it updates. It never forwards a request to
    GitHub, and every install still verifies what it downloads.</p>
  <p class="text-sm muted">Everything else on this domain keeps serving what it serves now, so the
    uploaded site stays the page people see. The mirror refreshes every hour.</p>`)
	if !strings.EqualFold("https://"+d.Host, update.OfficialMirror) {
		b.WriteString(`
  <p class="text-sm muted">Installs look for the mirror at ` + esc(update.OfficialMirror) + ` by
    default. A mirror on this domain is used only by installs whose <code>VAYU_UPDATE_MIRROR</code>
    points here.</p>`)
	}
	if config.Cfg.OnionMode {
		b.WriteString(`
  <p class="text-sm muted"><b>Unavailable in a Tor Space</b> — it makes no clearnet requests, so it
    cannot fetch releases.</p>
</div>`)
		return b.String()
	}
	checked, syncAttr := "", ` disabled title="Switch the mirror on and save first"`
	if d.ReleaseMirror() {
		checked, syncAttr = " checked", ""
	}
	b.WriteString(`
  <label class="field"><span class="field-label">
    <input type="checkbox" id="release-mirror-on"` + checked + `> Serve the release mirror on this domain</span></label>
  <div class="vm-row">
    <button type="button" class="btn btn--primary btn--sm" data-release-mirror-save>Save</button>
    <button type="button" class="btn btn--sm" data-release-mirror-sync` + syncAttr + `>Sync now</button>
    <span id="release-mirror-status" class="text-sm muted" role="status" aria-live="polite" data-checked="` +
		esc(a.releaseMirrorCheckedStamp()) + `"></span>
  </div>`)
	if a.relMirror != nil {
		b.WriteString(releaseMirrorStateHTML(a.releaseMirrorReport()))
	}
	b.WriteString(`
</div>`)
	return b.String()
}

// releaseMirrorCheckedStamp is the last-checked time in the exact form the
// status endpoint's JSON carries it, so the console can tell when it moves.
func (a *App) releaseMirrorCheckedStamp() string {
	if a.relMirror == nil {
		return ""
	}
	if t := a.relMirror.m.State().CheckedAt; !t.IsZero() {
		return t.Format(time.RFC3339Nano)
	}
	return ""
}

// releaseMirrorStateHTML renders what the mirror holds and how the last sync
// went. The error is shown verbatim: it is the operator's own panel, and the
// exact refusal is what tells them whether to wait or to look at a release.
func releaseMirrorStateHTML(rep releaseMirrorReport) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`
  <div class="settings-block-title">What it holds</div>`)
	if rep.CheckedAt == nil {
		b.WriteString(`
  <p class="text-sm muted">Nothing yet — it has not synced. The first sync starts when the mirror is
    switched on.</p>`)
		return b.String()
	}
	last := "never"
	if rep.SyncedAt != nil {
		last = relTimeAgo(*rep.SyncedAt)
	}
	b.WriteString(`
  <p class="text-sm muted">Checked ` + esc(relTimeAgo(*rep.CheckedAt)) + ` · last clean sync ` + esc(last) +
		` · ` + esc(humanBytes(rep.Bytes)) + ` on disk</p>`)
	if rep.LastError != "" {
		b.WriteString(`
  <p class="text-sm"><b>The last sync did not complete.</b> ` + esc(rep.LastError) + `</p>`)
	}
	if len(rep.Releases) == 0 {
		b.WriteString(`
  <p class="text-sm muted">No release is held, so an install falling back to this mirror finds
    nothing to update to.</p>`)
		return b.String()
	}
	b.WriteString(`
  <ul class="text-sm">`)
	for _, r := range rep.Releases {
		kind := "stable"
		if r.Prerelease {
			kind = "pre-release"
		}
		b.WriteString(`
    <li><b>` + esc(r.Version) + `</b> (` + kind + `) — ` + strconv.Itoa(r.Files) + ` files, binary verified by ` +
			esc(strings.Join(r.BinaryChecks, ", ")) + `, ` + esc(relTimeAgo(r.VerifiedAt)) + `</li>`)
	}
	b.WriteString(`
  </ul>`)
	return b.String()
}
