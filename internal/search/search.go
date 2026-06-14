// Package search provides a unified article search interface with a
// Meilisearch primary implementation and a SQLite full-text fallback (ADR-0050).
package search

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
)

// Hit is a single search result.
type Hit struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
}

// Result wraps hits with metadata about the query.
type Result struct {
	Hits     []Hit  `json:"hits"`
	Query    string `json:"query"`
	Fallback bool   `json:"fallback,omitempty"`
}

// Service is the search contract. Implementations may delegate to Meilisearch
// or fall back to SQLite LIKE queries.
type Service interface {
	Search(ctx context.Context, q string, limit int) (Result, error)
	Index(ctx context.Context, id, title, slug, content string, tags []string, createdAt int64) error
	Delete(ctx context.Context, id string) error
	Ping(ctx context.Context) error
	// DocCount reports the number of documents the search backend currently
	// holds. Used by the reconciler to detect drift from the article store.
	DocCount(ctx context.Context) (int, error)
}

// =============================================================================
// Meilisearch implementation with SQLite fallback
// =============================================================================

type meiliService struct {
	cb     *gobreaker.CircuitBreaker
	client *http.Client
	db     *sql.DB
}

// NewMeiliService returns a Service backed by Meilisearch with a SQLite
// fallback. The circuit breaker opens after 60% failure rate on ≥3 requests.
func NewMeiliService(client *http.Client, db *sql.DB) Service {
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        "search",
		MaxRequests: 3,
		Interval:    10 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(c gobreaker.Counts) bool {
			return c.Requests >= 3 && float64(c.TotalFailures)/float64(c.Requests) >= 0.60
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logging.LogJSON(logging.LogFields{
				Level: "warn", Component: "search-cb",
				Msg: fmt.Sprintf("circuit breaker %s → %s", from, to),
			})
		},
	})
	return &meiliService{cb: cb, client: client, db: db}
}

func (s *meiliService) Search(ctx context.Context, q string, limit int) (Result, error) {
	if s.cb == nil || s.cb.State() != gobreaker.StateClosed {
		return s.fallback(ctx, q, limit)
	}
	body, _ := json.Marshal(map[string]interface{}{
		"q": q, "limit": limit,
		"attributesToRetrieve": []string{"title", "slug", "tags", "created_at"},
	})
	req, err := http.NewRequestWithContext(ctx, "POST",
		config.Cfg.MeiliHost+"/indexes/articles/search", bytes.NewReader(body))
	if err != nil {
		return s.fallback(ctx, q, limit)
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return s.fallback(ctx, q, limit)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return s.fallback(ctx, q, limit)
	}
	// Meilisearch returns its own JSON; decode and re-shape.
	var raw struct {
		Hits []struct {
			Title     string   `json:"title"`
			Slug      string   `json:"slug"`
			Tags      []string `json:"tags"`
			CreatedAt int64    `json:"created_at"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return s.fallback(ctx, q, limit)
	}
	hits := make([]Hit, 0, len(raw.Hits))
	for _, h := range raw.Hits {
		hits = append(hits, Hit{
			Title: h.Title, Slug: h.Slug, Tags: h.Tags,
			CreatedAt: time.Unix(h.CreatedAt, 0).UTC(),
		})
	}
	return Result{Hits: hits, Query: q}, nil
}

func (s *meiliService) fallback(ctx context.Context, q string, limit int) (Result, error) {
	pattern := "%" + q + "%"
	rows, err := s.db.QueryContext(ctx,
		`SELECT title,slug,tags,created_at FROM articles WHERE title LIKE ? OR content LIKE ? OR tags LIKE ? ORDER BY created_at DESC LIMIT ?`,
		pattern, pattern, pattern, limit,
	)
	if err != nil {
		return Result{}, fmt.Errorf("search fallback: %w", err)
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		var tagsCSV string
		rows.Scan(&h.Title, &h.Slug, &tagsCSV, &h.CreatedAt)
		h.Tags = splitCSV(tagsCSV)
		hits = append(hits, h)
	}
	if hits == nil {
		hits = []Hit{}
	}
	return Result{Hits: hits, Query: q, Fallback: true}, nil
}

func (s *meiliService) Index(ctx context.Context, id, title, slug, content string, tags []string, createdAt int64) error {
	doc := map[string]interface{}{
		"id": id, "title": title, "slug": slug,
		"content":    content,
		"tags":       tags,
		"created_at": createdAt,
	}
	_, err := s.cb.Execute(func() (interface{}, error) {
		return nil, s.do(ctx, "POST", "/indexes/articles/documents", []map[string]interface{}{doc})
	})
	if err != nil {
		atomic.AddInt64(&metrics.MetricMeiliErrors, 1)
	}
	return err
}

func (s *meiliService) Ping(ctx context.Context) error {
	return s.do(ctx, "GET", "/health", nil)
}

// DocCount queries Meilisearch's index stats for the live document count. When
// Meilisearch is unavailable it falls back to the SQLite article-store count,
// which is the search-of-record in fallback mode (so drift is zero by design).
func (s *meiliService) DocCount(ctx context.Context) (int, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", config.Cfg.MeiliHost+"/indexes/articles/stats", nil)
	if err == nil {
		if config.Cfg.MeiliMasterKey != "" {
			req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
		}
		resp, derr := s.client.Do(req)
		if derr == nil {
			defer resp.Body.Close()
			if resp.StatusCode < 400 {
				var stats struct {
					NumberOfDocuments int `json:"numberOfDocuments"`
				}
				if json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&stats) == nil {
					return stats.NumberOfDocuments, nil
				}
			}
		}
	}
	// Fallback: the SQLite store is authoritative when Meili is unreachable.
	var n int
	if qerr := s.db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&n); qerr != nil {
		return 0, qerr
	}
	return n, nil
}

func (s *meiliService) Delete(ctx context.Context, id string) error {
	_, err := s.cb.Execute(func() (interface{}, error) {
		return nil, s.do(ctx, "DELETE", "/indexes/articles/documents/"+id, nil)
	})
	if err != nil {
		atomic.AddInt64(&metrics.MetricMeiliErrors, 1)
	}
	return err
}

func (s *meiliService) do(ctx context.Context, method, path string, body interface{}) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, config.Cfg.MeiliHost+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("meili %d: %s", resp.StatusCode, b)
	}
	return nil
}

// ConfigureIndex applies the canonical ranking/filter settings to Meilisearch.
// It is best-effort and logs on failure.
func ConfigureIndex(ctx context.Context, svc Service) {
	ms, ok := svc.(*meiliService)
	if !ok {
		return
	}
	ms.do(ctx, "PATCH", "/indexes/articles/settings", map[string]interface{}{
		"rankingRules":         []string{"words", "proximity", "attribute", "sort", "exactness"},
		"searchableAttributes": []string{"title", "tags", "content"},
		"filterableAttributes": []string{"tags", "created_at"},
		"sortableAttributes":   []string{"created_at", "updated_at"},
	}) //nolint:errcheck
}

// WaitReady blocks until Meilisearch responds to /health or maxAttempts is
// exhausted. Returns true if Meilisearch became available.
func WaitReady(ctx context.Context, svc Service, maxAttempts int) bool {
	ms, ok := svc.(*meiliService)
	if !ok {
		return false
	}
	for i := 0; i < maxAttempts; i++ {
		if err := ms.do(ctx, "GET", "/health", nil); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(5 * time.Second):
		}
	}
	return false
}

func splitCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
