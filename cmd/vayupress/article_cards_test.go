// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// planOf returns a query's plan as one line.
func planOf(t *testing.T, q string, args ...any) string {
	t.Helper()
	rows, err := dbpkg.Reader().Query(`EXPLAIN QUERY PLAN `+q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close() //nolint:errcheck
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, " | ")
}

// A listing chooses its posts without reading a post row: every column after
// content in the row costs the whole body's overflow pages, which is what put
// johal.in's tag pages at p95 1.9 s. Each choice is answered from a covering
// index, in order, for the whole install and for one hosted site.
func TestListingsChooseTheirPostsFromAnIndexAlone(t *testing.T) {
	openMigratedDB(t)
	for _, c := range []struct{ name, sql, index string }{
		{"tag page", tagPageIDsSQL + ` ORDER BY t.created_at DESC LIMIT ?`, "idx_article_tags_live"},
		{"tag page, one site", tagPageIDsSQL + ` AND t.domain_id=? ORDER BY t.created_at DESC LIMIT ?`, "idx_article_tags_live"},
		{"home feed", homeFeedIDsSQL + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`, "idx_articles_feed"},
		{"home feed, one site", homeFeedIDsSQL + ` AND domain_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`, "idx_articles_feed_domain"},
	} {
		args := []any{}
		if strings.HasPrefix(c.name, "tag") {
			args = append(args, "x")
		}
		if strings.HasSuffix(c.name, "one site") {
			args = append(args, "d1")
		}
		for strings.Count(c.sql, "?") > len(args) {
			args = append(args, 10)
		}
		p := planOf(t, c.sql, args...)
		if !strings.Contains(p, "USING COVERING INDEX "+c.index+" ") && !strings.HasSuffix(p, "USING COVERING INDEX "+c.index) {
			t.Errorf("%s: plan %q reads post rows, and every one of them walks its body", c.name, p)
		}
		if strings.HasPrefix(c.name, "tag") && strings.Contains(p, "articles") {
			t.Errorf("%s: plan %q reads articles to choose a tag's posts", c.name, p)
		}
		if strings.Contains(p, "TEMP B-TREE") {
			t.Errorf("%s: plan %q sorts in a temporary tree instead of reading the index in order", c.name, p)
		}
	}
}

func seedCardPost(t *testing.T, id, title, content, excerpt, image, updated string) {
	t.Helper()
	if _, err := dbpkg.DB.Exec(`INSERT INTO articles(id,title,slug,content,tags,status,created_at,updated_at,excerpt,feature_image) VALUES(?,?,?,?,?,'published',?,?,?,?)`,
		id, title, "slug-"+id, content, "go, sqlite", "2026-09-20 10:00:00", updated, excerpt, image); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func cardCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM article_cards WHERE article_id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A card is built from the body once, stored, and read from the store after;
// the operator's own excerpt and image win over the derived ones; the order
// asked for is kept, and a post that is gone is left out.
func TestAListingCardIsBuiltOnceAndKept(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	seedCardPost(t, "a1", "Derived", `<p>First words of the body.</p><img src="/media/one.png">`, "", "", "2026-09-20 10:00:00")
	seedCardPost(t, "a2", "Chosen", `<p>Body text.</p><img src="/media/body.png">`, "The operator's excerpt", "/media/cover.png", "2026-09-20 10:00:00")

	got := listingCards(ctx, []string{"a2", "gone", "a1"})
	if len(got) != 2 || got[0].Title != "Chosen" || got[1].Title != "Derived" {
		t.Fatalf("cards %+v, want Chosen then Derived", got)
	}
	if got[1].Excerpt != "First words of the body." || got[1].Image != "/media/one.png" {
		t.Errorf("derived card: excerpt %q, image %q", got[1].Excerpt, got[1].Image)
	}
	if got[0].Excerpt != "The operator's excerpt" || got[0].Image != "/media/cover.png" {
		t.Errorf("the operator's own excerpt or image lost to the body's: %+v", got[0])
	}
	if strings.Join(got[1].Tags, "|") != "go|sqlite" {
		t.Errorf("tags %q", got[1].Tags)
	}
	if cardCount(t, "a1") != 1 || cardCount(t, "a2") != 1 {
		t.Fatal("the cards built were not stored, so every listing reads the bodies again")
	}

	// Read from the store: the body is no longer consulted.
	if _, err := dbpkg.DB.Exec(`UPDATE article_cards SET excerpt='from the store' WHERE article_id='a1'`); err != nil {
		t.Fatal(err)
	}
	if again := listingCards(ctx, []string{"a1"}); len(again) != 1 || again[0].Excerpt != "from the store" {
		t.Errorf("a stored card was rebuilt from the body: %+v", again)
	}
}

// Any write to a post drops its card, through whatever path it came, so a
// listing never shows text the post no longer has.
func TestAWriteToAPostDropsItsCard(t *testing.T) {
	openMigratedDB(t)
	ctx := context.Background()
	seedCardPost(t, "b1", "Before", `<p>Old body.</p>`, "", "", "2026-09-20 10:00:00")
	seedCardPost(t, "b2", "Kept", `<p>Other body.</p>`, "", "", "2026-09-20 10:00:00")
	listingCards(ctx, []string{"b1", "b2"})

	if _, err := dbpkg.DB.Exec(`UPDATE articles SET title='After', content='<p>New body.</p>' WHERE id='b1'`); err != nil {
		t.Fatal(err)
	}
	if cardCount(t, "b1") != 0 {
		t.Fatal("an edited post kept its card")
	}
	if got := listingCards(ctx, []string{"b1"}); len(got) != 1 || got[0].Title != "After" || got[0].Excerpt != "New body." {
		t.Errorf("after the edit the listing shows %+v", got)
	}
	if cardCount(t, "b2") != 1 {
		t.Error("an edit to one post dropped another post's card")
	}

	if _, err := dbpkg.DB.Exec(`DELETE FROM articles WHERE id='b1'`); err != nil {
		t.Fatal(err)
	}
	if cardCount(t, "b1") != 0 {
		t.Error("a deleted post left its card behind")
	}
}

// A card read before an edit is not stored after it: the post it would show is
// no longer the one it was built from.
func TestACardBuiltBeforeAnEditIsNotStored(t *testing.T) {
	openMigratedDB(t)
	seedCardPost(t, "c1", "Now", `<p>Now.</p>`, "", "", "2026-09-21 09:00:00")
	var updated string
	if err := dbpkg.DB.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM articles WHERE id='c1'`).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	store := func(read string) {
		if _, err := dbpkg.DB.Exec(storeCardSQL, "c1", "Then", "slug-c1", "", "2026-09-20 10:00:00", "Then.", "", "c1", read); err != nil {
			t.Fatal(err)
		}
	}
	store("2026-09-20 10:00:00")
	if cardCount(t, "c1") != 0 {
		t.Fatal("a card built from the text before the edit was stored over the edit")
	}
	store(updated)
	if cardCount(t, "c1") != 1 {
		t.Fatal("a card built from the current text was refused")
	}
}
