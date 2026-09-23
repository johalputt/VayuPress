// SPDX-License-Identifier: Apache-2.0

package main

// site_publish.go — drafting, publishing and restoring a site document
// (ADR-0161), for the primary site and every hosted one. The console editor
// and the connector both come through here, so a document reaches the public
// page by one path with one validator.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/sitedoc"
)

// siteRevisionsKept is how many published revisions each site keeps. Older
// ones are deleted as new ones are published: enough to undo a month of
// daily edits, and a bound on a table every publish writes to.
const siteRevisionsKept = 50

// errSiteSlugTaken reports a page slug that something else on the site
// already answers at, so the page could never be seen.
type errSiteSlugTaken struct{ slug, why string }

func (e errSiteSlugTaken) Error() string {
	return fmt.Sprintf("the page address /%s is already %s — choose another", e.slug, e.why)
}

// checkSiteSlugs refuses a page slug the site cannot serve. Pages are found
// in the 404 fallback, after every route and every post, so a slug that
// either of those owns would publish a page nobody can reach.
//
// "Owned by a route" is asked of the REAL router: whatever it would route
// GET /<slug> to, other than the post catch-all, wins over the page. A list
// of reserved words kept here would be a second copy of the route table, and
// the first route added without updating it would be a page silently lost.
func (a *App) checkSiteSlugs(ctx context.Context, scope string, d sitedoc.Document) error {
	mux, _ := a.rootHandler().(*chi.Mux)
	for _, p := range d.Pages {
		if p.Slug == "" {
			continue
		}
		if mux != nil {
			if pattern := mux.Find(chi.NewRouteContext(), http.MethodGet, "/"+p.Slug); pattern != "" && pattern != "/{slug}" {
				return errSiteSlugTaken{p.Slug, "used by VayuPress (" + pattern + ")"}
			}
		}
		var n int
		if err := dbpkg.Reader().QueryRowContext(ctx,
			`SELECT COUNT(1) FROM articles WHERE slug=? AND domain_id=?`, p.Slug, scope).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errSiteSlugTaken{p.Slug, "the address of a post or page on this site"}
		}
	}
	return nil
}

// saveSiteDraft stores the one unpublished draft a site has.
func saveSiteDraft(ctx context.Context, scope string, d sitedoc.Document, author string) (time.Time, error) {
	raw, err := sitedoc.Marshal(d)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now().UTC()
	_, err = dbpkg.WDB.ExecContext(ctx,
		`INSERT INTO site_drafts(domain_id,doc,author,updated_at) VALUES(?,?,?,?) `+
			`ON CONFLICT(domain_id) DO UPDATE SET doc=excluded.doc, author=excluded.author, updated_at=excluded.updated_at`,
		scope, string(raw), author, now)
	return now, err
}

// siteDraft is a site's unpublished draft, if it has one.
func siteDraft(ctx context.Context, scope string) (sitedoc.Document, time.Time, bool) {
	var raw string
	var at time.Time
	if err := dbpkg.Reader().QueryRowContext(ctx,
		`SELECT doc, updated_at FROM site_drafts WHERE domain_id=?`, scope).Scan(&raw, &at); err != nil {
		return sitedoc.Document{}, time.Time{}, false
	}
	d, err := sitedoc.Parse([]byte(raw))
	if err != nil {
		return sitedoc.Document{}, time.Time{}, false
	}
	return d, at, true
}

// publishSite makes d the site's live document: a new revision, the draft
// cleared, and revisions beyond siteRevisionsKept deleted — in one
// transaction, so a failure leaves the site as it was.
func (a *App) publishSite(ctx context.Context, scope string, d sitedoc.Document, author string) (int64, error) {
	raw, err := sitedoc.Marshal(d)
	if err != nil {
		return 0, err
	}
	if err := a.checkSiteSlugs(ctx, scope, d); err != nil {
		return 0, err
	}
	tx, err := dbpkg.WDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	res, err := tx.ExecContext(ctx,
		`INSERT INTO site_revisions(domain_id,doc,author,published_at) VALUES(?,?,?,?)`,
		scope, string(raw), author, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_drafts WHERE domain_id=?`, scope); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM site_revisions WHERE domain_id=? AND id NOT IN (SELECT id FROM site_revisions WHERE domain_id=? ORDER BY id DESC LIMIT ?)`,
		scope, scope, siteRevisionsKept); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// siteRevision is one published revision of a site.
type siteRevision struct {
	ID          int64     `json:"id"`
	Author      string    `json:"author"`
	PublishedAt time.Time `json:"published_at"`
}

func siteRevisions(ctx context.Context, scope string) ([]siteRevision, error) {
	rows, err := dbpkg.Reader().QueryContext(ctx,
		`SELECT id, author, published_at FROM site_revisions WHERE domain_id=? ORDER BY id DESC LIMIT ?`,
		scope, siteRevisionsKept)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	var out []siteRevision
	for rows.Next() {
		var rv siteRevision
		if err := rows.Scan(&rv.ID, &rv.Author, &rv.PublishedAt); err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

// siteRevisionDoc is one revision's document. The scope is part of the
// lookup: a revision id belongs to the site it was published on, and naming
// another site's id through this site's URL finds nothing.
func siteRevisionDoc(ctx context.Context, scope string, id int64) (sitedoc.Document, error) {
	var raw string
	err := dbpkg.Reader().QueryRowContext(ctx,
		`SELECT doc FROM site_revisions WHERE id=? AND domain_id=?`, id, scope).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return sitedoc.Document{}, errNoSuchRevision
	}
	if err != nil {
		return sitedoc.Document{}, err
	}
	return sitedoc.Parse([]byte(raw))
}

var errNoSuchRevision = errors.New("no such revision on this site")

func parseRevisionID(s string) (int64, bool) {
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id > 0
}
