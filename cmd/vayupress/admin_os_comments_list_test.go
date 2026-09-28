// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/comments"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/users"
)

func seedComment(t *testing.T, id, article, status, created string) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO comments(id,article_id,author,email,body,status,created_at) VALUES(?,?,?,?,?,?,?)`,
		id, article, "Reader "+id[:2], id[:2]+"@readers.example", "Comment "+id[:2], status, created); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func commentReq(method, target, id, form string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(form))
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestTheCommentsPageIsAList — comments as a table, one view per state with
// its own address, states as a dot and a word, the first comment in the
// inspector and the rest a row away.
func TestTheCommentsPageIsAList(t *testing.T) {
	openMigratedDB(t)
	seedListPost(t, "on-a-post", "On a post", "published", 0, 0, "2026-09-20 10:00:00")
	seedComment(t, "aa0000000000000000000001", "id-on-a-post", "pending", "2026-09-28 09:00:00")
	seedComment(t, "bb0000000000000000000002", "id-on-a-post", "approved", "2026-09-27 09:00:00")
	seedComment(t, "cc0000000000000000000003", "id-on-a-post", "spam", "2026-09-26 09:00:00")
	a := &App{siteSettings: settings.New(dbpkg.DB), commentStore: comments.New(dbpkg.DB)}
	page := func(target string) string {
		rec := httptest.NewRecorder()
		a.handleOSComments(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec.Body.String()
	}

	body := page("/os/comments")
	if !strings.Contains(body, `data-page-kind="list"`) {
		t.Error("Comments does not say it is a list")
	}
	if n := strings.Count(body, "data-list-row"); n != 3 {
		t.Errorf("%d rows, want every comment", n)
	}
	for _, want := range []string{`id="cc-pending">1</span>`, `id="cc-approved">1</span>`, `href="/os/comments?status=pending"`,
		`data-list-src="/os/comments/inspector/bb0000000000000000000002"`, `id="cact-aa0000000000000000000001"`, "/on-a-post"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "status-pill") || strings.Contains(body, "●") {
		t.Error("a comment's state is still a filled pill")
	}
	if n := strings.Count(body, `aria-selected="true"`); n != 1 {
		t.Errorf("%d rows selected on load, want the first", n)
	}

	waiting := page("/os/comments?status=pending")
	if n := strings.Count(waiting, "data-list-row"); n != 1 || !strings.Contains(waiting, "aa0000000000000000000001") {
		t.Errorf("the Waiting view lists %d rows, want only the waiting comment", n)
	}
}

// TestACommentIsAnsweredOnlyOnceApproved — the inspector offers a reply box
// on an approved comment and says why there is none on a waiting one, and the
// reply endpoint holds to the same rule.
func TestACommentIsAnsweredOnlyOnceApproved(t *testing.T) {
	openMigratedDB(t)
	seedListPost(t, "on-a-post", "On a post", "published", 0, 0, "2026-09-20 10:00:00")
	const waiting, live = "aa0000000000000000000001", "bb0000000000000000000002"
	seedComment(t, waiting, "id-on-a-post", "pending", "2026-09-28 09:00:00")
	seedComment(t, live, "id-on-a-post", "approved", "2026-09-27 09:00:00")
	a := &App{commentStore: comments.New(dbpkg.DB)}

	insp := func(id string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		a.handleOSCommentInspector(rec, commentReq(http.MethodGet, "/os/comments/inspector/"+id, id, ""))
		return rec
	}
	if got := insp(live).Body.String(); !strings.Contains(got, `/reply-fragment"`) {
		t.Errorf("an approved comment offers no reply:\n%s", got)
	}
	if got := insp(waiting).Body.String(); strings.Contains(got, "reply-fragment") || !strings.Contains(got, "Approve it to reply") {
		t.Errorf("a waiting comment must say why it cannot be answered yet:\n%s", got)
	}
	for id, code := range map[string]int{"ff0000000000000000000009": http.StatusNotFound, "not-an-id": http.StatusBadRequest} {
		if got := insp(id).Code; got != code {
			t.Errorf("inspector for %s: %d, want %d", id, got, code)
		}
	}

	operator := &users.User{ID: "u1", Email: "owner@install.example", Name: "Owner", Role: users.RoleAdmin}
	reply := func(id, text string, u *users.User) *httptest.ResponseRecorder {
		req := commentReq(http.MethodPost, "/os/api/comments/"+id+"/reply-fragment", id, url.Values{"body": {text}}.Encode())
		if u != nil {
			req = req.WithContext(context.WithValue(req.Context(), ctxUserKey, u))
		}
		rec := httptest.NewRecorder()
		a.handleOSCommentReplyFragment(rec, req)
		return rec
	}
	if got := reply(waiting, "Thanks", operator).Code; got != http.StatusConflict {
		t.Errorf("a reply to a waiting comment: %d, want 409", got)
	}
	if got := reply(live, "Thanks", nil).Code; got != http.StatusUnauthorized {
		t.Errorf("a reply with nobody signed in: %d, want 401", got)
	}
	if got := reply(live, "   ", operator).Code; got != http.StatusBadRequest {
		t.Errorf("an empty reply: %d, want 400", got)
	}
	rec := reply(live, "Thanks for reading.", operator)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Thanks for reading.") {
		t.Fatalf("a reply to an approved comment: %d %s", rec.Code, rec.Body)
	}
	var author, email, status, parent string
	if err := dbpkg.DB.QueryRow(`SELECT author,email,status,parent_id FROM comments WHERE body='Thanks for reading.'`).Scan(&author, &email, &status, &parent); err != nil {
		t.Fatal(err)
	}
	if author != "Owner" || email != "owner@install.example" || status != "approved" || parent != live {
		t.Errorf("the reply is %s <%s>, %s, under %s; want the signed-in operator, live, under the comment", author, email, status, parent)
	}
}
