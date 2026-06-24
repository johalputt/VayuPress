package analytics

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

func newExtStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	ddl := []string{
		`CREATE TABLE analytics_sessions(id TEXT PRIMARY KEY, visitor_id TEXT NOT NULL, browser TEXT NOT NULL DEFAULT '', os TEXT NOT NULL DEFAULT '', device TEXT NOT NULL DEFAULT '', screen TEXT NOT NULL DEFAULT '', language TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '', region TEXT NOT NULL DEFAULT '', city TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, url_path TEXT NOT NULL, url_query TEXT NOT NULL DEFAULT '', page_title TEXT NOT NULL DEFAULT '', referrer TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '', utm_source TEXT NOT NULL DEFAULT '', utm_medium TEXT NOT NULL DEFAULT '', utm_campaign TEXT NOT NULL DEFAULT '', utm_content TEXT NOT NULL DEFAULT '', utm_term TEXT NOT NULL DEFAULT '', event_type INTEGER NOT NULL DEFAULT 1, event_name TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
		`CREATE TABLE analytics_event_data(id INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL, property_key TEXT NOT NULL, property_value TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`,
	}
	for _, q := range ddl {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	return New(db)
}

func TestCollectStoresNoPII(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()
	ip := "203.0.113.7"
	ua := "Mozilla/5.0 (Windows NT 10.0) Chrome/120.0 Safari/537.36"
	if err := s.Collect(ctx, CollectRequest{URL: "/about?utm_source=x", Referrer: "https://news.example.com/path", Hostname: "blog.test", EventType: 1}, ip, ua); err != nil {
		t.Fatalf("collect: %v", err)
	}

	// The raw IP and User-Agent must never be persisted in any column.
	var sid, vid, browser, os, device string
	if err := s.db.QueryRow(`SELECT id,visitor_id,browser,os,device FROM analytics_sessions LIMIT 1`).Scan(&sid, &vid, &browser, &os, &device); err != nil {
		t.Fatalf("session row: %v", err)
	}
	if strings.Contains(vid, ip) || strings.Contains(sid, ip) {
		t.Fatalf("visitor/session id leaks IP")
	}
	if browser != "Chrome" || os != "Windows" || device != "Desktop" {
		t.Fatalf("coarse UA parse wrong: %s/%s/%s", browser, os, device)
	}
	// Ensure the full UA string is nowhere in the sessions table.
	var blob string
	_ = s.db.QueryRow(`SELECT COALESCE(group_concat(visitor_id||browser||os||device||screen||language),'') FROM analytics_sessions`).Scan(&blob)
	if strings.Contains(blob, "Mozilla") {
		t.Fatalf("UA string leaked into storage")
	}

	// Referrer must be reduced to a bare host (no path/scheme).
	var ref, path, query string
	if err := s.db.QueryRow(`SELECT referrer,url_path,url_query FROM analytics_pageviews LIMIT 1`).Scan(&ref, &path, &query); err != nil {
		t.Fatalf("pv row: %v", err)
	}
	if ref != "news.example.com" {
		t.Fatalf("referrer not reduced to host: %q", ref)
	}
	if path != "/about" || query != "utm_source=x" {
		t.Fatalf("path/query split wrong: %q %q", path, query)
	}
}

func TestVisitorIDStableAndDistinct(t *testing.T) {
	t.Parallel()
	ip, ua, host := "198.51.100.4", "curl/8", "blog.test"
	a := visitorID(ip, ua, host)
	b := visitorID(ip, ua, host)
	if a != b {
		t.Fatalf("same inputs must yield same id within a day")
	}
	if visitorID("10.0.0.1", ua, host) == a {
		t.Fatalf("different IP must yield different id")
	}
	if !strings.HasPrefix(a, "v") {
		t.Fatalf("unexpected id format %q", a)
	}
}

func TestOverviewAndTopPages(t *testing.T) {
	t.Parallel()
	s := newExtStore(t)
	ctx := context.Background()
	// Two pageviews from one visitor, one from another.
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/post", Hostname: "h", EventType: 1}, "1.1.1.1", "Chrome")
	_ = s.Collect(ctx, CollectRequest{URL: "/", Hostname: "h", EventType: 1}, "2.2.2.2", "Firefox")

	ov, err := s.OverviewSince(ctx, 14)
	if err != nil {
		t.Fatalf("overview: %v", err)
	}
	if ov.TotalPageviews != 3 {
		t.Fatalf("pageviews=%d want 3", ov.TotalPageviews)
	}
	if ov.UniqueVisitors != 2 {
		t.Fatalf("unique visitors=%d want 2", ov.UniqueVisitors)
	}
	pages, err := s.TopPages(ctx, 14, 10)
	if err != nil {
		t.Fatalf("toppages: %v", err)
	}
	if len(pages) == 0 || pages[0].Path != "/" || pages[0].Pageviews != 2 {
		t.Fatalf("top page wrong: %+v", pages)
	}
}
