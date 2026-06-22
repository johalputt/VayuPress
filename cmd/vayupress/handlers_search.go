package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
)

// reindexResult records the outcome of the most recent search reconciliation so
// operators can audit it via the drift endpoint after the fact.
type reindexResult struct {
	RanAt      time.Time `json:"ran_at"`
	Scanned    int       `json:"scanned"`
	Indexed    int       `json:"indexed"`
	Failed     int       `json:"failed"`
	DurationMs int64     `json:"duration_ms"`
}

// reindexAllArticles streams every article from the store and re-indexes it into
// the search backend. It is the rebuild half of the reconciler: it makes the
// search index converge to the article store regardless of prior drift.
func (a *App) reindexAllArticles(ctx context.Context) (*reindexResult, error) {
	start := time.Now()
	res := &reindexResult{RanAt: start.UTC()}
	rows, err := dbpkg.DB.QueryContext(ctx,
		`SELECT id,title,slug,content,tags,created_at FROM articles WHERE COALESCE(status,'published')='published' ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		if ctx.Err() != nil {
			break
		}
		var id, title, slug, content, tagsStr string
		var createdAt time.Time
		if scanErr := rows.Scan(&id, &title, &slug, &content, &tagsStr, &createdAt); scanErr != nil {
			res.Failed++
			continue
		}
		res.Scanned++
		clean := htmlTagRe.ReplaceAllString(a.policy.Sanitize(content), "")
		if idxErr := a.search.Index(ctx, id, title, slug, clean, api.SplitTags(tagsStr), createdAt.Unix()); idxErr != nil {
			res.Failed++
			continue
		}
		res.Indexed++
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return res, rowsErr
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res, nil
}

// startSearchReconciler runs a periodic background drift check and, when the
// search index has drifted from the article store, rebuilds it. It is policy-
// gated: reconciliation is a write-side effect against the search backend, so
// it only runs while the system is in a mode that permits normal activity
// (Normal or Degraded) — never in read-only, maintenance, or quarantined modes.
// Disabled when SEARCH_RECONCILE_MIN is 0.
func (a *App) startSearchReconciler(done <-chan struct{}) {
	interval := time.Duration(config.Cfg.SearchReconcileMin) * time.Minute
	if interval <= 0 || a.search == nil {
		logging.LogInfo("search-reconciler", "periodic reconciler disabled")
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				a.reconcileSearchOnce()
			}
		}
	}()
	logging.LogInfo("search-reconciler", "periodic reconciler started")
}

// reconcileSearchOnce performs one gated drift check + conditional rebuild.
func (a *App) reconcileSearchOnce() {
	if m := mode.Global.Current(); m != mode.ModeNormal && m != mode.ModeDegraded {
		logging.LogJSON(logging.LogFields{
			Level: "info", Component: "search-reconciler", Severity: "info",
			Msg: "reconcile skipped by system mode", Error: string(m),
		})
		return
	}
	// Single-flight with the manual reindex endpoint.
	if !atomic.CompareAndSwapInt32(&a.reindexRunning, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&a.reindexRunning, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var storeCount int
	if err := dbpkg.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM articles`).Scan(&storeCount); err != nil {
		logging.LogError("search-reconciler", "store count failed", err.Error())
		return
	}
	indexCount, err := a.search.DocCount(ctx)
	if err == nil && storeCount == indexCount {
		return // in sync — nothing to do
	}
	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "search-reconciler", Severity: "info",
		Msg: "drift detected, rebuilding index",
	})
	res, rerr := a.reindexAllArticles(ctx)
	if rerr != nil {
		logging.LogError("search-reconciler", "rebuild failed", rerr.Error())
		return
	}
	a.lastReindexMu.Lock()
	a.lastReindex = res
	a.lastReindexMu.Unlock()
	logging.LogInfo("search-reconciler", "rebuild complete")
}

// handleSearchReindex rebuilds the entire search index from the article store.
// CSRF-protected and single-flighted: a second concurrent request is rejected
// with 409 rather than running two competing rebuilds.
func (a *App) handleSearchReindex(w http.ResponseWriter, r *http.Request) {
	if a.search == nil {
		writeAPIError(w, r, 503, "search_unavailable", "search service not configured", "https://docs.vayupress.com/operations/search")
		return
	}
	if !atomic.CompareAndSwapInt32(&a.reindexRunning, 0, 1) {
		writeAPIError(w, r, 409, "reindex_running", "a reindex is already in progress", "https://docs.vayupress.com/operations/search")
		return
	}
	defer atomic.StoreInt32(&a.reindexRunning, 0)

	// Bound the rebuild so a hung search backend can't pin the request forever.
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := a.reindexAllArticles(ctx)
	if err != nil {
		writeAPIError(w, r, 500, "reindex_failed", err.Error(), "https://docs.vayupress.com/operations/search")
		return
	}
	a.lastReindexMu.Lock()
	a.lastReindex = res
	a.lastReindexMu.Unlock()
	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "search-reconciler",
		Msg: "reindex complete", RequestID: getRequestID(r),
	})
	writeJSON(w, r, 200, res)
}

// handleSearchDrift reports the difference between the article store and the
// search index document count, so operators can decide whether a rebuild is
// warranted without running one.
func (a *App) handleSearchDrift(w http.ResponseWriter, r *http.Request) {
	if a.search == nil {
		writeAPIError(w, r, 503, "search_unavailable", "search service not configured", "https://docs.vayupress.com/operations/search")
		return
	}
	var storeCount int
	if err := dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&storeCount); err != nil {
		writeAPIError(w, r, 500, "store_count_failed", err.Error(), "https://docs.vayupress.com/operations/search")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	indexCount, err := a.search.DocCount(ctx)
	resp := map[string]interface{}{
		"store_count": storeCount,
		"index_count": indexCount,
		"drift":       storeCount - indexCount,
		"in_sync":     err == nil && storeCount == indexCount,
	}
	if err != nil {
		resp["index_error"] = err.Error()
	}
	a.lastReindexMu.Lock()
	if a.lastReindex != nil {
		resp["last_reindex"] = a.lastReindex
	}
	a.lastReindexMu.Unlock()
	writeJSON(w, r, 200, resp)
}
