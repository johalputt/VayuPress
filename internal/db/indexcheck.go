package db

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
)

// indexcheck.go is a startup guardrail against full-table-scan regressions.
//
// VayuPress is built to serve 1M+ posts on a small VPS, which depends on every
// hot read being index-backed. A future feature could silently reintroduce a
// `tags LIKE`/`COALESCE(...)`/unindexed-ORDER-BY full scan that only manifests as
// a 502 once the catalogue is large. To catch that early, we run EXPLAIN QUERY
// PLAN on a curated list of the hottest reads at startup and log a loud warning
// (and bump a metric) if any of them resolves to a full table scan instead of an
// index search/scan. It is read-only, runs once in the background, and never
// blocks boot or fails startup.

// hotQuery is a representative read whose query plan must stay index-backed. The
// args are placeholder values used only to let EXPLAIN bind the statement; they
// are never executed against real data.
type hotQuery struct {
	name string
	sql  string
	args []any
}

// hotQueries mirrors the heaviest catalogue reads in the codebase. Keep this in
// sync when a new hot list/filter/count query is added. Inherently-unindexable
// reads (e.g. the LIKE-based SQLite search fallback) are intentionally excluded.
var hotQueries = []hotQuery{
	{"posts-status-counts", `SELECT status, COUNT(1) FROM articles WHERE is_page=0 GROUP BY status`, nil},
	{"posts-list", `SELECT title,slug,tags,updated_at,status FROM articles WHERE is_page=0 AND status='published' ORDER BY created_at DESC LIMIT 100 OFFSET 0`, nil},
	{"home-count", `SELECT COUNT(1) FROM articles WHERE status='published' AND is_page=0`, nil},
	{"home-list", `SELECT title,slug,content,tags,created_at FROM articles WHERE status='published' AND is_page=0 ORDER BY created_at DESC LIMIT 30`, nil},
	{"pages-list", `SELECT title,slug,status,updated_at FROM articles WHERE is_page=1 ORDER BY updated_at DESC LIMIT 1000`, nil},
	{"article-by-slug", `SELECT id,title,slug,content,tags,created_at,updated_at,status FROM articles WHERE slug=?`, []any{"x"}},
	{"recent-articles", `SELECT title,slug,created_at FROM articles ORDER BY created_at DESC LIMIT 15`, nil},
	{"json-list-published", `SELECT id,title,slug,content,tags,created_at,updated_at,status FROM articles WHERE status='published' ORDER BY created_at DESC LIMIT 20 OFFSET 0`, nil},
	{"tag-page-list", `SELECT a.title,a.slug,a.content,a.tags,a.created_at FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND a.status='published' ORDER BY t.created_at DESC LIMIT 200`, []any{"x"}},
	{"tag-page-count", `SELECT COUNT(1) FROM article_tags t CROSS JOIN articles a ON a.id=t.article_id WHERE t.tag_norm=? AND a.status='published'`, []any{"x"}},
	{"tag-index-counts", `SELECT tag, COUNT(1) FROM article_tags GROUP BY tag`, nil},
	{"contact-messages-list", `SELECT id,name,email,message,page,is_read,created_at FROM contact_messages ORDER BY created_at DESC LIMIT 500`, nil},
	{"contact-unread-count", `SELECT COUNT(1) FROM contact_messages WHERE is_read=0`, nil},
	{"comments-by-article", `SELECT id,article_id,status,created_at FROM comments WHERE article_id=? AND status='approved' ORDER BY created_at`, []any{"x"}},
}

// StartIndexSelfCheck runs the index self-check once, shortly after boot, on a
// background goroutine. It is a no-op until then and exits immediately on
// shutdown. Running after a short delay keeps it off the critical startup path.
func StartIndexSelfCheck(doneCh <-chan struct{}) {
	go func() {
		select {
		case <-doneCh:
			return
		case <-time.After(45 * time.Second):
		}
		RunIndexSelfCheck()
	}()
}

// RunIndexSelfCheck runs EXPLAIN QUERY PLAN on every hot query and logs a warning
// for any that resolves to a full table scan. It returns the number of full-scan
// warnings emitted. A query that cannot be explained (e.g. a table that does not
// exist on a partial/older schema) is skipped, not treated as a failure.
func RunIndexSelfCheck() int {
	if DB == nil {
		return 0
	}
	warnings := 0
	for _, q := range hotQueries {
		plan, err := explainQueryPlan(q.sql, q.args...)
		if err != nil {
			// Missing table/column on a partial schema, or a bind mismatch — skip.
			continue
		}
		if detail, scan := firstFullScan(plan); scan {
			warnings++
			atomic.AddInt64(&metrics.MetricFullScanWarnings, 1)
			logging.LogJSON(logging.LogFields{
				Level:     "warn",
				Component: "index-selfcheck",
				Msg:       fmt.Sprintf("hot query %q resolves to a FULL TABLE SCAN (%q) — add or adjust an index before this 502s at scale (ADR-0095)", q.name, detail),
			})
		}
	}
	if warnings == 0 {
		logging.LogInfo("index-selfcheck", fmt.Sprintf("ok — all %d hot queries are index-backed", len(hotQueries)))
	} else {
		logging.LogJSON(logging.LogFields{
			Level:     "warn",
			Component: "index-selfcheck",
			Msg:       fmt.Sprintf("%d hot quer(y/ies) fall back to a full table scan — see warnings above (ADR-0095)", warnings),
		})
	}
	return warnings
}

// explainQueryPlan returns the EXPLAIN QUERY PLAN "detail" lines for sql. It runs
// on the read pool and is strictly read-only.
func explainQueryPlan(sql string, args ...any) ([]string, error) {
	rows, err := Reader().Query("EXPLAIN QUERY PLAN "+sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var details []string
	for rows.Next() {
		// EXPLAIN QUERY PLAN yields (id, parent, notused, detail); the detail is
		// the last column. Scan into a generic slice to be resilient to column
		// shape differences across SQLite builds.
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		if len(cells) == 0 {
			continue
		}
		if d, ok := cells[len(cells)-1].(string); ok {
			details = append(details, d)
		} else if b, ok := cells[len(cells)-1].([]byte); ok {
			details = append(details, string(b))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return details, nil
}

// firstFullScan reports whether any plan line is a full table scan — a line that
// begins with "SCAN " but uses no index (neither a named index, a covering
// index, nor the integer primary key). An index scan ("SCAN x USING INDEX …") is
// fine. Returns the offending detail line for the log message.
func firstFullScan(plan []string) (string, bool) {
	for _, d := range plan {
		t := strings.TrimSpace(d)
		if !strings.HasPrefix(t, "SCAN ") {
			continue
		}
		if strings.Contains(t, "USING INDEX") ||
			strings.Contains(t, "USING COVERING INDEX") ||
			strings.Contains(t, "USING INTEGER PRIMARY KEY") {
			continue
		}
		return t, true
	}
	return "", false
}
