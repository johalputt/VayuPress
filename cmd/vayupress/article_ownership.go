// SPDX-License-Identifier: Apache-2.0

package main

// article_ownership.go — who may rewrite or destroy an existing article.
//
// AUDIT FINDING (Section 1). handleOSEditorSave and handleOSPostDelete are
// install-wide destructive primitives that consulted nothing but the slug.
// `a.articles.Update` calls the explicitly UNSCOPED `Repo.Get`, and the delete
// runs `DELETE FROM articles WHERE slug=?` with no ownership and no domain
// predicate. Both routes sit under `authorAPIAreas`, so author level reaches
// them, and `handleOSPosts` lists every hosted customer's posts to an author —
// so discovery was not even blind.
//
// The consequence: one author account could silently rewrite or permanently
// destroy every post on the install AND on every hosted customer domain,
// comments included, live immediately, with no snapshot to restore from
// (versions.Store.Save has no non-test call site). The console's own contract
// says the opposite — accessAuthor is documented as "author — own content".
// admin_os_mysite.go had already written the diagnosis down, and gave it as the
// reason clients are not sold an editor.
//
// The rule below is deliberately the narrowest one that closes the tenant break
// without taking away work people are doing today:
//
//   - Editor and above are unchanged. Editing across authors is their job, and
//     tightening them would break the console's ordinary workflow.
//   - An author may write an article attributed to them.
//   - An author may write an UNATTRIBUTED article only on the primary domain
//     (domain_id ""). Content imported or migrated before multi-author
//     attribution existed carries no author_id, and refusing it outright would
//     make an author's own legacy posts uneditable on installs that have run for
//     years. A hosted customer's post is never in that category, which is the
//     half that actually matters.
//   - An author may never touch an article belonging to another domain, however
//     it is attributed.

import (
	"net/http"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// accessLevelOf returns the console access level the middleware resolved for
// this request, defaulting to accessAuthor — the same permissive default
// osPathMinLevel applies, so a handler reached without the middleware is judged
// no more harshly than the router would judge it.
func accessLevelOf(r *http.Request) int {
	if v := r.Context().Value(ctxAccessKey); v != nil {
		if lvl, ok := v.(int); ok {
			return lvl
		}
	}
	return accessAuthor
}

// articleWriteRefusal returns the reason the caller may NOT write the article at
// slug, or "" when the write is allowed. A missing article returns "" so the
// caller keeps its own not-found handling and this never becomes an existence
// oracle for slugs on other people's domains.
func (a *App) articleWriteRefusal(r *http.Request, slug string) string {
	if accessLevelOf(r) >= accessEditor {
		return ""
	}
	if dbpkg.DB == nil {
		return ""
	}
	var authorID, domainID string
	if err := dbpkg.Reader().QueryRowContext(r.Context(),
		`SELECT COALESCE(author_id,''), COALESCE(domain_id,'') FROM articles WHERE slug=?`,
		slug).Scan(&authorID, &domainID); err != nil {
		return "" // no such article — the caller's own 404 path handles it
	}

	if domainID != "" {
		return "this post belongs to another site on this install; only an editor or an administrator can change it"
	}
	me := currentUserIDOf(r)
	if authorID != "" && me != "" && authorID == me {
		return ""
	}
	if authorID == "" {
		return "" // unattributed primary-site content — see the note above
	}
	return "this post is written by someone else; ask an editor to make the change"
}

// refuseArticleWrite writes the 403 and reports whether it did, so both call
// sites read as one line.
func (a *App) refuseArticleWrite(w http.ResponseWriter, r *http.Request, slug string) bool {
	if reason := a.articleWriteRefusal(r, slug); reason != "" {
		dbpkg.AuditLog("article.write.refused", dbpkg.AuditActor(r), slug, reason)
		writeAPIError(w, r, http.StatusForbidden, "not-your-post", reason, "")
		return true
	}
	return false
}
