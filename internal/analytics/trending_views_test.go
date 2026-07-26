// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TestTrendingArticlesByViews proves trending is computed from the SAME source
// as the admin "Top pages" panel (analytics_pageviews, event_type=1), restricted
// to published non-page articles, ranked by pageviews, with title/image mapped.
func TestTrendingArticlesByViews(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	for _, q := range []string{
		`CREATE TABLE articles(id INTEGER PRIMARY KEY, slug TEXT, title TEXT, feature_image TEXT, status TEXT NOT NULL DEFAULT 'published', is_page INTEGER NOT NULL DEFAULT 0, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL DEFAULT '', url_path TEXT NOT NULL, event_type INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`INSERT INTO articles(id,slug,title,feature_image,status,is_page) VALUES
		 (1,'post-a','Post A','/media/a.jpg','published',0),
		 (2,'post-b','Post B','','published',0),
		 (3,'the-page','A Page','','published',1),
		 (4,'a-draft','Draft','','draft',0);`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	id := 0
	add := func(path string, n, eventType int) {
		for i := 0; i < n; i++ {
			id++
			if _, err := db.Exec(`INSERT INTO analytics_pageviews(id,url_path,event_type) VALUES(?,?,?)`,
				fmt.Sprintf("pv%d", id), path, eventType); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}
	add("/post-b", 5, 1)    // top
	add("/post-a", 3, 1)    // second
	add("/the-page", 10, 1) // excluded: is_page
	add("/a-draft", 8, 1)   // excluded: draft
	add("/", 20, 1)         // excluded: not an article
	add("/post-a", 9, 2)    // engagement events (not pageviews) must NOT count

	got, err := New(db).TrendingArticlesByViews(context.Background(), 7, 10)
	if err != nil {
		t.Fatalf("TrendingArticlesByViews: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 article results (pages/drafts/non-articles excluded), got %d: %+v", len(got), got)
	}
	if got[0].Slug != "post-b" || got[0].Views != 5 || got[0].Title != "Post B" {
		t.Errorf("rank 1 = %+v, want post-b / 5 views / Post B", got[0])
	}
	if got[1].Slug != "post-a" || got[1].Views != 3 || got[1].Image != "/media/a.jpg" {
		t.Errorf("rank 2 = %+v, want post-a / 3 views / /media/a.jpg", got[1])
	}
}
