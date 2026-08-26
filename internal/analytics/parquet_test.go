// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/parquet-go/parquet-go"
)

func TestParquetExportRoundTrip(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1) // one connection = one :memory: database
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE analytics_pageviews(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, url_path TEXT NOT NULL, url_query TEXT NOT NULL DEFAULT '', page_title TEXT NOT NULL DEFAULT '', referrer TEXT NOT NULL DEFAULT '', hostname TEXT NOT NULL DEFAULT '', utm_source TEXT NOT NULL DEFAULT '', utm_medium TEXT NOT NULL DEFAULT '', utm_campaign TEXT NOT NULL DEFAULT '', utm_content TEXT NOT NULL DEFAULT '', utm_term TEXT NOT NULL DEFAULT '', event_type INTEGER NOT NULL DEFAULT 1, event_name TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, domain_id TEXT NOT NULL DEFAULT '', country TEXT NOT NULL DEFAULT '');`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("ddl: %v", err)
		}
	}
	s := New(db)
	ctx := context.Background()
	day := time.Now().UTC().Format("2006-01-02")
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`INSERT INTO analytics_pageviews(id,session_id,url_path,referrer,hostname,utm_source,event_type,domain_id,country) VALUES(?,?,?,?,?,?,?,?,'IN')`,
			string(rune('a'+i)), "sess", "/p", "https://ref.test/", "t", "nl", 1, ""); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var buf bytes.Buffer
	if err := s.ExportPageviewsParquet(ctx, day, day, &buf); err != nil {
		t.Fatalf("export: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty parquet output")
	}

	rows, err := parquet.Read[PageviewRecord](bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].URLPath != "/p" || rows[0].UTMSource != "nl" || rows[0].Country != "IN" || rows[0].EventType != 1 {
		t.Fatalf("round trip mismatch: %+v", rows[0])
	}
	if rows[0].CreatedAt == 0 {
		t.Fatal("created_at lost")
	}
}
