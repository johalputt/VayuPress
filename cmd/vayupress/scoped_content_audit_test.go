// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// ADR-0154 Phase 5 — the adversarial pass over the per-site content surface.
//
// Attacked, and found holding (recorded because a clean result is a result):
//   - /os/d/{id} refusing the PRIMARY. scopedDomainMiddleware redirects it to
//     /os/website before any handler runs, so the content page can never be
//     opened for the primary — which matters more here than anywhere else,
//     because the primary's articles carry domain_id "" while its registry row
//     has a real id. Without that guard the console would have listed zero posts
//     for the operator's own site and, worse, "move to this site" would have
//     stamped posts with an id no article query uses, orphaning them.
//   - A blank registry id, likewise refused rather than resolved to the
//     primary's sentinel.
//
// Two findings below did not hold.

// FINDING 1 — a new route family is exactly the kind of change that quietly
// reopens the ADR-0152 confinement gate. The per-site console is the OPERATOR'S
// surface; a confined client reaching it would see, and be able to move, content
// belonging to sites that are not theirs.
func TestTheContentRoutesAreNotReachableByAConfinedClient(t *testing.T) {
	for _, p := range []string{
		"/os/d/abc123/content",
		"/os/d/abc123/api/content/move",
		"/os/d/abc123/api/content/new",
	} {
		if clientPathAllowed(p) {
			t.Errorf("a confined client can reach %s — they could enumerate and reassign "+
				"content across every site on the install", p)
		}
	}
}

// FINDING 2 — moving a slug that does not exist reported success.
//
// SetDomain is `UPDATE articles SET domain_id=? WHERE slug=?` with no
// RowsAffected check, so a typo matched nothing, the endpoint returned 200, and
// the page said "Moved ✓ — reloading". The operator then reloads, does not see
// the post, and cannot tell whether the move failed or the listing is broken.
// A write that changed nothing must not report that it did.
func TestMovingAPostThatDoesNotExistIsNotReportedAsSuccess(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // :memory: is per-connection; an unpinned pool sees an empty schema
	if _, err := db.Exec(`CREATE TABLE articles(
		id TEXT PRIMARY KEY, title TEXT, slug TEXT UNIQUE, content TEXT, tags TEXT,
		created_at DATETIME, updated_at DATETIME, status TEXT, author_id TEXT,
		domain_id TEXT NOT NULL DEFAULT '', is_page INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO articles(id,title,slug,content,domain_id) VALUES('1','Real','real','x','')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	repo := dbpkg.NewArticleRepo(db)
	ctx := context.Background()

	if err := repo.SetDomain(ctx, "real", "s1"); err != nil {
		t.Fatalf("moving a real post failed: %v", err)
	}
	err = repo.SetDomain(ctx, "no-such-post", "s1")
	if err == nil {
		t.Fatal("moving a slug that does not exist reported SUCCESS. The console then says " +
			"\"Moved ✓\" and reloads, the post is not in the list, and the operator cannot tell " +
			"whether the move failed or the listing is wrong")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no") {
		t.Errorf("the error does not say the post was not found: %v", err)
	}
	// Stated because it was measured: mutating the fix's `err == nil` guard on
	// RowsAffected — so a driver error would be reported as not-found — does NOT
	// fail this test. go-sqlite3 always reports the count, so that branch is
	// unexercised and cannot be covered without a fake driver written solely to
	// reach it. The guard stays because refusing a move that actually succeeded
	// is the worse failure; the coverage claim does not.
}
