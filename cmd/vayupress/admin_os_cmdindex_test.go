// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// The command bar's index is read by admin-os.js by these keys (render 03).
// It once shipped marshalling "Label", "Slug" and so on (untagged fields), so
// every row rendered blank and a query threw. This holds the server's keys
// against the script's reads.
func TestCommandBarIndexUsesTheKeysTheScriptReads(t *testing.T) {
	a := ownershipApp(t)
	seedOwnedArticle(t, "hello-palette", "u1", "")
	idx := cmdIndexFor(t, a, nil)
	need := map[string][]string{
		"posts":    {"label", "slug", "status", "updated"},
		"actions":  {"label", "icon", "hint"},
		"settings": {"label", "where", "href"},
	}
	for group, keys := range need {
		items := idx[group]
		if len(items) == 0 {
			t.Fatalf("group %q is empty; the test proves nothing about it", group)
		}
		for _, it := range items {
			for _, k := range keys {
				if v, ok := it[k].(string); !ok || v == "" {
					t.Errorf("%s item %v has no %q, which the command bar reads", group, it, k)
				}
			}
		}
	}
	// An action either opens a page or runs one request; never neither.
	for _, act := range idx["actions"] {
		if act["href"] == nil && act["post"] == nil {
			t.Errorf("action %v neither opens nor runs anything", act["label"])
		}
	}
	js, err := os.ReadFile("../../static/js/admin-os.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, read := range []string{"p.label", "p.slug", "p.status", "p.updated", "p.excerpt", "a.label", "a.post", "a.href", "st.label", "st.where", "st.href"} {
		if !strings.Contains(string(js), read) {
			t.Errorf("admin-os.js no longer reads %s; update this test with the key it reads now", read)
		}
	}
}

// Shown == reachable: an author is offered neither the backup nor a setting,
// both administrator pages, and the old hand-kept page list, whose hubs no
// longer exist, is gone.
func TestTheCommandBarOffersOnlyWhatTheViewerCanOpen(t *testing.T) {
	a := ownershipApp(t)
	labels := func(idx map[string][]map[string]any, group string) string {
		var out []string
		for _, it := range idx[group] {
			out = append(out, it["label"].(string))
		}
		return strings.Join(out, " | ")
	}
	admin := cmdIndexFor(t, a, nil)
	if !strings.Contains(labels(admin, "actions"), "Take a backup now") || !strings.Contains(labels(admin, "settings"), "Site name") {
		t.Fatalf("an administrator is not offered the backup or the site name:\n%s\n%s", labels(admin, "actions"), labels(admin, "settings"))
	}
	author := cmdIndexFor(t, a, &users.User{ID: "u2", Email: "a@example.com", Role: users.RoleAuthor})
	if got := labels(author, "actions"); strings.Contains(got, "backup") || !strings.Contains(got, "New post") {
		t.Errorf("an author's actions: %s", got)
	}
	if got := labels(author, "settings"); got != "" {
		t.Errorf("an author is offered settings they cannot open: %s", got)
	}
	if strings.Contains(fmt.Sprint(admin), "Growth hub") {
		t.Error("the retired page list is back")
	}
}

func cmdIndexFor(t *testing.T, a *App, u *users.User) map[string][]map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/os/api/cmd-index", nil)
	if u != nil {
		req = withUser(req, u)
	}
	rec := httptest.NewRecorder()
	a.handleOSCmdIndex(rec, req)
	var idx map[string][]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &idx); err != nil {
		t.Fatalf("index is not JSON: %v\n%s", err, rec.Body.String())
	}
	return idx
}

func TestAPostExcerptIsPlainOpeningWords(t *testing.T) {
	got := cmdExcerpt(`<h2>Hello</h2><p>Caf&eacute; &amp; <b>bar</b></p>`)
	if got != "Hello Café & bar" {
		t.Errorf("excerpt: %q", got)
	}
	long := cmdExcerpt("<p>" + strings.Repeat("word ", 60) + "</p>")
	if !strings.HasSuffix(long, "word…") || len([]rune(long)) > 161 {
		t.Errorf("a long post is not cut on a word: %q", long)
	}
}
