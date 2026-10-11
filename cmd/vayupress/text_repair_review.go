// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/textfix"
)

// text_repair_review.go is where an author decides about text that reads as
// garbled on its way in (migration 110). The Posts page lists the posts, each
// post's inspector shows every change the repair would make, and the author
// restores the text or keeps it as written. Nothing is restored unasked: a
// post about encodings quotes such text on purpose.

// postsToReview counts the posts offered for review. Listing them (prune)
// first drops the offers whose post is gone, renamed or deleted since; only
// then, so an ordinary visit to the Posts page writes nothing.
func (a *App) postsToReview(ctx context.Context, prune bool) int {
	if dbpkg.DB == nil {
		return 0
	}
	if prune {
		_, _ = dbpkg.WDB.ExecContext(ctx, `DELETE FROM text_repair_offer WHERE slug NOT IN (SELECT slug FROM articles)`)
	}
	var n int
	_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM text_repair_offer o JOIN articles a ON a.slug=o.slug WHERE o.state='offered' AND a.is_page=0`).Scan(&n)
	return n
}

// textChangesToReview reads what the repair would change in a post offered
// for review, from its text as it is now; nil when it is not offered.
func textChangesToReview(ctx context.Context, slug, title string) []textfix.Change {
	var content string
	if err := dbpkg.Reader().QueryRowContext(ctx,
		`SELECT a.content FROM articles a JOIN text_repair_offer o ON o.slug=a.slug WHERE a.slug=? AND o.state='offered'`, slug).Scan(&content); err != nil {
		return nil
	}
	return dbpkg.TextChanges(title, content)
}

// textReviewShown is how many changes the inspector lists before saying how
// many more there are.
const textReviewShown = 12

// osTextReview is the inspector's section for a post offered for review: each
// change as it reads now and as it would read, and the two choices. slugEsc
// must already be HTML-escaped.
func osTextReview(slugEsc string, changes []textfix.Change) string {
	var b strings.Builder
	b.WriteString(`<section id="post-text-` + slugEsc + `" aria-labelledby="post-text-h-` + slugEsc + `">`)
	b.WriteString(`<h3 class="sa-insp__head" id="post-text-h-` + slugEsc + `">Text to review</h3>`)
	b.WriteString(`<p class="sa-insp__hint">This reads as text garbled on its way in. Restore it, or keep it as written if it is meant: a post about encodings quotes such text on purpose. Restoring keeps the text before as a version.</p>`)
	// Each change as a pair, as the facts above are: as it reads now, and as
	// it would read.
	b.WriteString(`<dl class="sa-insp__facts" aria-label="Reads now, and would read">`)
	for i, c := range changes {
		if i == textReviewShown {
			break
		}
		b.WriteString(`<dt>` + html.EscapeString(c.From) + `</dt><dd>` + html.EscapeString(c.To) + `</dd>`)
	}
	b.WriteString(`</dl>`)
	if more := len(changes) - textReviewShown; more > 0 {
		b.WriteString(`<p class="sa-insp__hint">And ` + itoaSafe(more) + ` more.</p>`)
	}
	b.WriteString(`<div class="sa-insp__actions">` +
		`<button type="button" class="btn btn--sm btn--primary" hx-post="/os/api/posts/` + slugEsc + `/text-fragment" hx-vals='{"do":"restore"}' hx-target="#post-text-` + slugEsc + `" hx-swap="outerHTML" hx-disabled-elt="this">Restore the text</button>` +
		`<button type="button" class="btn btn--sm" hx-post="/os/api/posts/` + slugEsc + `/text-fragment" hx-vals='{"do":"keep"}' hx-target="#post-text-` + slugEsc + `" hx-swap="outerHTML" hx-disabled-elt="this">Keep as written</button>` +
		`</div></section>`)
	return b.String()
}

// handleOSPostTextFragment applies the author's choice for a post offered for
// review and returns what replaces its section: restore (through the write
// queue, like any edit, so the text before is kept as a version, and the
// offer leaves with the garbled text), or keep (not offered again while the
// post holds the same runs).
func (a *App) handleOSPostTextFragment(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	do := r.FormValue("do")
	if !api.IsValidSlug(slug) || (do != "restore" && do != "keep") {
		http.Error(w, "a valid slug and do (restore|keep) are required", http.StatusBadRequest)
		return
	}
	note := "Kept as written. It is not offered again unless its text changes."
	switch do {
	case "restore":
		if a.articles == nil {
			http.Error(w, "posts are not available", http.StatusServiceUnavailable)
			return
		}
		art, err := a.articles.Repo.Get(r.Context(), slug)
		if err != nil {
			http.Error(w, "no post with that address", http.StatusNotFound)
			return
		}
		title, _ := textfix.Repair(art.Title)
		content, _ := textfix.Repair(art.Content)
		if _, err := a.articles.Update(r.Context(), slug, &title, &content, nil); err != nil {
			http.Error(w, "the post could not be saved: "+err.Error(), http.StatusInternalServerError)
			return
		}
		note = "Restored. The text before is kept as a version."
	case "keep":
		if err := dbpkg.KeepTextAsWritten(r.Context(), slug); err != nil {
			http.Error(w, "the choice could not be saved", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<p id="post-text-`+html.EscapeString(slug)+`" class="sa-insp__hint" role="status">`+note+`</p>`)
}
