// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func tagCount(t *testing.T, tag, domain string) int {
	t.Helper()
	q, args := tagCountSQL, []any{tag}
	if domain != "" {
		q, args = q+` AND t.domain_id=?`, append(args, domain)
	}
	var n int
	if err := dbpkg.DB.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func addPost(t *testing.T, id, status, domain string, tagsFirst bool) {
	t.Helper()
	tx, err := dbpkg.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	insert := func() {
		if _, err := tx.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,domain_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			id, "T "+id, "s-"+id, "body", "Go", status, domain, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if !tagsFirst {
		insert()
	}
	if err := dbpkg.SyncArticleTagsByIDTx(tx, id, now, []string{"Go"}); err != nil {
		t.Fatal(err)
	}
	if tagsFirst {
		insert()
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// A tag's count is read from its tag rows alone (migration 106), so the rows
// must follow their post whatever writes it: published or not, which site,
// deleted by any path. One seed per trigger, each named by what it keeps.
func TestTagRowsFollowTheirPost(t *testing.T) {
	openMigratedDB(t)
	addPost(t, "pub", "published", "", false)
	if n := tagCount(t, "go", ""); n != 1 {
		t.Fatalf("a published post is not counted: %d", n)
	}
	addPost(t, "draft", "draft", "", false)
	if n := tagCount(t, "go", ""); n != 1 {
		t.Errorf("tags written after a draft count it (the tag insert trigger): %d", n)
	}
	addPost(t, "early", "draft", "", true)
	if n := tagCount(t, "go", ""); n != 1 {
		t.Errorf("tags written before their draft count it (the post insert trigger): %d", n)
	}
	if _, err := dbpkg.DB.Exec(`UPDATE articles SET status='published' WHERE id='draft'`); err != nil {
		t.Fatal(err)
	}
	if n := tagCount(t, "go", ""); n != 2 {
		t.Errorf("a draft published is not counted (the update trigger, status): %d", n)
	}
	if _, err := dbpkg.DB.Exec(`UPDATE articles SET domain_id='d1' WHERE id='pub'`); err != nil {
		t.Fatal(err)
	}
	if n := tagCount(t, "go", "d1"); n != 1 {
		t.Errorf("a post moved to another site is not counted there (the update trigger, domain): %d", n)
	}
	// The console and the API delete with a bare DELETE: neither removes tags.
	if _, err := dbpkg.DB.Exec(`DELETE FROM articles WHERE id='draft'`); err != nil {
		t.Fatal(err)
	}
	var left int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM article_tags WHERE article_id='draft'`).Scan(&left)
	if left != 0 || tagCount(t, "go", "") != 1 {
		t.Errorf("a deleted post's tags outlive it (the delete trigger): %d rows", left)
	}
	repo := dbpkg.NewArticleRepo(dbpkg.DB)
	got, total, err := repo.List(context.Background(), 1, 10, "GO")
	if err != nil || total != 1 || len(got) != 1 || got[0].ID != "pub" {
		t.Errorf("the listing by tag: %d %+v %v", total, got, err)
	}
}

// What migration 106 does once to an install that had none of this: rows of
// deleted posts go, and every row says whether its post is live and where.
// The statements run are the shipped file's own.
func TestTheTagRowRepairOfMigration106(t *testing.T) {
	openMigratedDB(t)
	addPost(t, "pub", "published", "", false)
	addPost(t, "draft", "draft", "", false)
	addPost(t, "site", "published", "d1", false)
	for _, s := range []string{
		`DROP TRIGGER article_tags_on_delete`, `DELETE FROM articles WHERE id='pub'`,
		`UPDATE article_tags SET live=1, domain_id=''`,
	} {
		if _, err := dbpkg.DB.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.ReadFile("../../internal/db/migrations/106-article-tags-live.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(file), "\n") {
		if strings.HasPrefix(line, "DELETE ") || strings.HasPrefix(line, "UPDATE ") {
			if _, err := dbpkg.DB.Exec(line); err != nil {
				t.Fatalf("%s: %v", line, err)
			}
		}
	}
	var orphans int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM article_tags WHERE article_id='pub'`).Scan(&orphans)
	if orphans != 0 {
		t.Errorf("a deleted post's tag rows survive the repair")
	}
	if n := tagCount(t, "go", ""); n != 1 {
		t.Errorf("after the repair the tag counts %d, want the one published post", n)
	}
	if n := tagCount(t, "go", "d1"); n != 1 {
		t.Errorf("after the repair the other site counts %d, want 1", n)
	}
}
