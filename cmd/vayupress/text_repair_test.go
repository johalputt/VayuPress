// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// offers reads the posts offered for review and the posts kept as written.
func offers(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	rows, err := dbpkg.DB.Query(`SELECT slug, state FROM text_repair_offer`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var slug, state string
		_ = rows.Scan(&slug, &state)
		out[slug] = state
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	return out
}

// The pass changes no post: it offers each with text that can be restored,
// lists what cannot, leaves clean posts alone, and once at the end never runs
// again.
func TestTheStoredTextIsOfferedOnce(t *testing.T) {
	a := textRepairApp(t)
	seedPost(t, "a1", "restorable", "It\u00e2\u20ac\u2122s here", "<p>a \u00e2\u20ac\u201d b</p>")
	seedPost(t, "a2", "clean", "Plain", "<p>café — fine</p>")
	seedPost(t, "a3", "lost", "Lost", "<p>metabolism ÃÃÃÃÃÂ¢ triggers</p>")
	seedPost(t, "a4", "both", "Both", "<p>a \u00e2\u20ac\u201d b, then ÃÃÂ¢</p>")
	if err := a.repairStoredText(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	var writes int
	_ = dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs`).Scan(&writes)
	if writes != 0 {
		t.Errorf("the pass queued %d writes; it must change no post", writes)
	}
	got := offers(t)
	if got["restorable"] != "offered" || got["both"] != "offered" {
		t.Errorf("restorable posts not offered: %v", got)
	}
	if _, ok := got["clean"]; ok {
		t.Error("a clean post was offered")
	}
	if _, ok := got["lost"]; ok {
		t.Error("a post with nothing restorable was offered")
	}
	var lost []string
	rows, err := dbpkg.DB.Query(`SELECT slug FROM text_repair_lost ORDER BY slug`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		lost = append(lost, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if len(lost) != 2 || lost[0] != "both" || lost[1] != "lost" {
		t.Errorf("listed as unrestorable: %v", lost)
	}
	var scanned, offered int
	var done *time.Time
	_ = dbpkg.DB.QueryRow(`SELECT scanned, restored, done_at FROM text_repair WHERE id=1`).Scan(&scanned, &offered, &done)
	if scanned != 4 || offered != 2 || done == nil {
		t.Errorf("record: scanned %d offered %d done %v", scanned, offered, done)
	}
	seedPost(t, "a5", "later", "Later", "<p>x \u00e2\u20ac\u201d y</p>")
	if err := a.repairStoredText(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := offers(t)["later"]; ok {
		t.Error("a finished pass ran again (new writes are offered as they arrive)")
	}
}

// The pass resumes where it stopped rather than starting over.
func TestTheRepairPassResumes(t *testing.T) {
	a := textRepairApp(t)
	seedPost(t, "b1", "first", "T", "<p>a \u00e2\u20ac\u201d b</p>")
	seedPost(t, "b2", "second", "T", "<p>c \u00e2\u20ac\u201d d</p>")
	if _, err := dbpkg.DB.Exec(`INSERT INTO text_repair(id, cursor) VALUES(1, 'b1')`); err != nil {
		t.Fatal(err)
	}
	if err := a.repairStoredText(context.Background(), nil, 0); err != nil {
		t.Fatal(err)
	}
	got := offers(t)
	if _, ok := got["first"]; ok || got["second"] != "offered" {
		t.Errorf("resumed at the cursor: %v", got)
	}
}

// A post kept as written stays off the list while it holds the same runs; a
// write that brings different ones offers it again, and one that leaves none
// takes it off.
func TestAKeptPostIsOfferedAgainOnlyForNewRuns(t *testing.T) {
	textRepairApp(t)
	ctx := context.Background()
	// The guide to mojibake on johal.in: its examples are meant.
	example := "<p>\"\u00c3\u00a9\" displaying as \"\u00c3\u0192\u00c2\u00a9\"</p>"
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "guide", dbpkg.TextChanges("Guide", example)); err != nil {
		t.Fatal(err)
	}
	if got := offers(t)["guide"]; got != "offered" {
		t.Fatalf("the guide was not offered: %q", got)
	}
	if err := dbpkg.KeepTextAsWritten(ctx, "guide"); err != nil {
		t.Fatal(err)
	}
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "guide", dbpkg.TextChanges("Guide", example+"<p>more prose</p>")); err != nil {
		t.Fatal(err)
	}
	if got := offers(t)["guide"]; got != "kept" {
		t.Errorf("an edit that kept the same runs offered the post again: %q", got)
	}
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "guide", dbpkg.TextChanges("Guide", example+"<p>a \u00e2\u20ac\u201d b</p>")); err != nil {
		t.Fatal(err)
	}
	if got := offers(t)["guide"]; got != "offered" {
		t.Errorf("a new garbled run did not offer the post again: %q", got)
	}
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "guide", dbpkg.TextChanges("Guide", "<p>clean</p>")); err != nil {
		t.Fatal(err)
	}
	if _, ok := offers(t)["guide"]; ok {
		t.Error("a post with nothing to restore is still listed")
	}
}

// The inspector shows each change, escaped, and the two choices; Restore
// queues the restored text as an edit and Keep records the choice. Neither
// is shown for a post not offered.
func TestThePostsInspectorOffersTheRepair(t *testing.T) {
	a := textRepairApp(t)
	ctx := context.Background()
	title, body := "It\u00e2\u20ac\u2122s <b>", "<p>a \u00e2\u20ac\u201d b</p>"
	seedPost(t, "d1", "offered", title, body)
	seedPost(t, "d2", "plain", "Plain", "<p>fine</p>")
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "offered", dbpkg.TextChanges(title, body)); err != nil {
		t.Fatal(err)
	}
	if n := a.postsToReview(ctx, false); n != 1 {
		t.Errorf("counted %d to review", n)
	}
	p, ok := a.postDetailFor(ctx, "offered")
	if !ok {
		t.Fatal("no detail")
	}
	page := osPostInspector(p)
	for _, want := range []string{"Text to review", "<dt>\u00e2\u20ac\u2122</dt><dd>’</dd>", `/os/api/posts/offered/text-fragment`, `"do":"restore"`, `"do":"keep"`} {
		if !strings.Contains(page, want) {
			t.Errorf("inspector lacks %q", want)
		}
	}
	if p, _ := a.postDetailFor(ctx, "plain"); strings.Contains(osPostInspector(p), "Text to review") {
		t.Error("a post not offered shows the review")
	}

	rec := httptest.NewRecorder()
	a.handleOSPostTextFragment(rec, postFragReq("offered", "do=restore"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Restored") {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	var qTitle, qBody string
	if err := dbpkg.DB.QueryRow(`SELECT article_json->>'$.title', article_json->>'$.content' FROM write_jobs WHERE op='update' AND article_json->>'$.slug'='offered'`).Scan(&qTitle, &qBody); err != nil {
		t.Fatal(err)
	}
	if qTitle != "It’s <b>" || qBody != "<p>a — b</p>" {
		t.Errorf("restore queued %q, %q", qTitle, qBody)
	}
	if _, ok := offers(t)["offered"]; ok {
		t.Error("a restored post is still offered")
	}

	seedPost(t, "d3", "keep-me", "Keep", body)
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "keep-me", dbpkg.TextChanges("Keep", body)); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	a.handleOSPostTextFragment(rec, postFragReq("keep-me", "do=keep"))
	if rec.Code != http.StatusOK || offers(t)["keep-me"] != "kept" {
		t.Errorf("keep: %d, state %q", rec.Code, offers(t)["keep-me"])
	}
	// Kept is off the list: not counted, not listed, no review in its inspector.
	seedPost(t, "d4", "still-offered", "Still", body)
	if err := dbpkg.OfferTextRepair(ctx, dbpkg.WDB, "still-offered", dbpkg.TextChanges("Still", body)); err != nil {
		t.Fatal(err)
	}
	if n := a.postsToReview(ctx, false); n != 1 {
		t.Errorf("counted %d to review, want only the one still offered", n)
	}
	var listed []string
	rows, err := dbpkg.DB.Query(`SELECT slug FROM articles WHERE ` + dbpkg.TextOfferWhere())
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		listed = append(listed, s)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0] != "still-offered" {
		t.Errorf("the review list holds %v", listed)
	}
	if p, _ := a.postDetailFor(ctx, "keep-me"); strings.Contains(osPostInspector(p), "Text to review") {
		t.Error("a post kept as written still shows the review")
	}
	rec = httptest.NewRecorder()
	a.handleOSPostTextFragment(rec, postFragReq("keep-me", "do=delete"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown choice got %d", rec.Code)
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
