// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// deskPost writes a post tagged "go" with an explicit time, so the newest
// posts can be chosen to be the ones a desk must skip.
func deskPost(t *testing.T, id, status, domain string, page bool, at time.Time) {
	t.Helper()
	tx, err := dbpkg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,domain_id,is_page,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		id, "T "+id, id, "body", "go", status, domain, page, at, at); err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.SyncArticleTagsByIDTx(tx, id, at, []string{"go"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// A desk lists a topic's newest published posts of this site. Each seed is
// newer than the posts the desk should show, so it would take their place if
// its rule were not applied: another site's posts (enough to fill the whole
// candidate window), a draft, and a page.
func TestADeskListsOnlyThisSitesPublishedPosts(t *testing.T) {
	openMigratedDB(t)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range 3 {
		deskPost(t, fmt.Sprintf("a%d", i), "published", "a", false, t0.Add(time.Duration(i)*time.Minute))
	}
	for i := range 12 {
		deskPost(t, fmt.Sprintf("b%d", i), "published", "b", false, t0.Add(time.Hour+time.Duration(i)*time.Minute))
	}
	deskPost(t, "a-draft", "draft", "a", false, t0.Add(2*time.Hour))
	deskPost(t, "a-page", "published", "a", true, t0.Add(3*time.Hour))

	slugs := func(scope string, scoped bool) string {
		var s []string
		for _, p := range (&App{}).halcyonTopicPosts(context.Background(), "go", 3, scope, scoped) {
			s = append(s, p.Slug)
		}
		return strings.Join(s, " ")
	}
	if got, want := slugs("a", true), "a2 a1 a0"; got != want {
		t.Errorf("site a's desk = %q, want %q (no other site's post, no draft, no page)", got, want)
	}
	if got, want := slugs("", false), "b11 b10 b9"; got != want {
		t.Errorf("the install's desk = %q, want %q (no draft, no page)", got, want)
	}
}
