// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/queue"
)

func textRepairApp(t *testing.T) *App {
	t.Helper()
	openMigratedDB(t)
	return &App{articles: &api.ArticleService{Repo: dbpkg.NewArticleRepo(dbpkg.DB), Queue: queue.NewSQLiteWriter(dbpkg.DB, 10000)}}
}

func seedPost(t *testing.T, id, slug, title, content string) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at) VALUES(?,?,?,?,?, 'published',?,?)`,
		id, title, slug, content, "go,db", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
}

// The pass restores what can be restored through the write queue, lists what
// cannot, leaves clean posts alone, and once at the end never runs again.
func TestTheStoredTextIsRepairedOnce(t *testing.T) {
	a := textRepairApp(t)
	seedPost(t, "a1", "restorable", "Itâ€™s here", "<p>a â€” b</p>")
	seedPost(t, "a2", "clean", "Plain", "<p>café — fine</p>")
	seedPost(t, "a3", "lost", "Lost", "<p>metabolism ÃÃÃÃÃÂ¢ triggers</p>")
	seedPost(t, "a4", "both", "Both", "<p>a â€” b, then ÃÃÂ¢</p>")
	if err := a.repairStoredText(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	queued := map[string][2]string{}
	rows, err := dbpkg.DB.Query(`SELECT article_json->>'$.slug', article_json->>'$.title', article_json->>'$.content' FROM write_jobs WHERE op='update'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var slug, title, content string
		_ = rows.Scan(&slug, &title, &content)
		queued[slug] = [2]string{title, content}
	}
	_ = rows.Close()
	if got := queued["restorable"]; got != [2]string{"It’s here", "<p>a — b</p>"} {
		t.Errorf("a restorable post is not queued restored: %q", got)
	}
	if got := queued["both"]; got[1] != "<p>a — b, then ÃÃÂ¢</p>" {
		t.Errorf("a post with both is not restored where it can be: %q", got)
	}
	if _, ok := queued["clean"]; ok {
		t.Error("a clean post was rewritten")
	}
	if _, ok := queued["lost"]; ok {
		t.Error("a post with nothing restorable was rewritten")
	}
	var lost []string
	rows, _ = dbpkg.DB.Query(`SELECT slug FROM text_repair_lost ORDER BY slug`)
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		lost = append(lost, s)
	}
	_ = rows.Close()
	if len(lost) != 2 || lost[0] != "both" || lost[1] != "lost" {
		t.Errorf("listed as unrestorable: %v", lost)
	}
	var scanned, restored int
	var done *time.Time
	_ = dbpkg.DB.QueryRow(`SELECT scanned, restored, done_at FROM text_repair WHERE id=1`).Scan(&scanned, &restored, &done)
	if scanned != 4 || restored != 2 || done == nil {
		t.Errorf("record: scanned %d restored %d done %v", scanned, restored, done)
	}
	seedPost(t, "a5", "later", "Later", "<p>x â€” y</p>")
	if err := a.repairStoredText(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE op='update' AND article_json->>'$.slug'='later'`).Scan(&n)
	if n != 0 {
		t.Error("a finished pass ran again (new writes are repaired as they arrive)")
	}
}

// The pass resumes where it stopped rather than starting over.
func TestTheRepairPassResumes(t *testing.T) {
	a := textRepairApp(t)
	seedPost(t, "b1", "first", "T", "<p>a â€” b</p>")
	seedPost(t, "b2", "second", "T", "<p>c â€” d</p>")
	if _, err := dbpkg.DB.Exec(`INSERT INTO text_repair(id, cursor) VALUES(1, 'b1')`); err != nil {
		t.Fatal(err)
	}
	if err := a.repairStoredText(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	var first, second int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE article_json->>'$.slug'='first'`).Scan(&first)
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE article_json->>'$.slug'='second'`).Scan(&second)
	if first != 0 || second != 1 {
		t.Errorf("resumed at the cursor: first %d, second %d", first, second)
	}
}

// The Posts page names the posts left to fix by hand, and a post fixed since
// leaves the list when the list is opened.
func TestThePostsPageListsUnrestorablePosts(t *testing.T) {
	a := textRepairApp(t)
	seedPost(t, "c1", "lost-one", "Lost", "<p>ÃÃÂ¢</p>")
	seedPost(t, "c2", "fixed-since", "Fixed", "<p>fine now</p>")
	for _, s := range []string{"lost-one", "fixed-since"} {
		if _, err := dbpkg.DB.Exec(`INSERT INTO text_repair_lost(slug, runs) VALUES(?, 1)`, s); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if n := a.postsWithLostText(ctx, false); n != 2 {
		t.Errorf("counted %d without opening the list", n)
	}
	if n := a.postsWithLostText(ctx, true); n != 1 {
		t.Errorf("opening the list kept %d, want the one still garbled", n)
	}
}
