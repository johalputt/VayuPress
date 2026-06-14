//go:build integration

package main

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/microcosm-cc/bluemonday"

	"github.com/johalputt/vayupress/internal/search"
)

// countingSearch records how many documents were indexed so the reconciler test
// can assert it converged the index to the article store.
type countingSearch struct {
	indexed int64
	docs    int
}

func (c *countingSearch) Search(_ context.Context, _ string, _ int) (search.Result, error) {
	return search.Result{Hits: []search.Hit{}}, nil
}
func (c *countingSearch) Index(_ context.Context, _, _, _, _ string, _ []string, _ int64) error {
	atomic.AddInt64(&c.indexed, 1)
	return nil
}
func (c *countingSearch) Delete(_ context.Context, _ string) error { return nil }
func (c *countingSearch) Ping(_ context.Context) error             { return nil }
func (c *countingSearch) DocCount(_ context.Context) (int, error)  { return c.docs, nil }

func TestSearchDriftEndpoint(t *testing.T) {
	srv, key := newTestHarness(t)

	// Two articles in the store; noopSearch reports an index count of 0.
	for _, slug := range []string{"drift-a", "drift-b"} {
		resp := doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
			"title": "Drift " + slug, "slug": slug, "content": "<p>hi</p>", "tags": []string{},
		})
		resp.Body.Close()
	}

	resp := doRequest(t, srv, "GET", "/api/v1/admin/search/drift", key, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("drift: want 200, got %d", resp.StatusCode)
	}
	var body struct {
		StoreCount int  `json:"store_count"`
		IndexCount int  `json:"index_count"`
		Drift      int  `json:"drift"`
		InSync     bool `json:"in_sync"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.StoreCount != 2 || body.IndexCount != 0 || body.Drift != 2 || body.InSync {
		t.Fatalf("unexpected drift report: %+v", body)
	}
}

func TestReindexAllArticlesConverges(t *testing.T) {
	// newTestHarness initialises dbpkg.DB and seeds via the article API.
	srv, key := newTestHarness(t)
	for _, slug := range []string{"r1", "r2", "r3"} {
		resp := doRequest(t, srv, "POST", "/api/v1/articles", key, map[string]interface{}{
			"title": "Re " + slug, "slug": slug, "content": "<p>x</p>", "tags": []string{"t"},
		})
		resp.Body.Close()
	}

	cs := &countingSearch{}
	app := &App{policy: bluemonday.UGCPolicy(), search: cs}
	res, err := app.reindexAllArticles(context.Background())
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if res.Scanned != 3 || res.Indexed != 3 || res.Failed != 0 {
		t.Fatalf("unexpected reindex result: %+v", res)
	}
	if atomic.LoadInt64(&cs.indexed) != 3 {
		t.Fatalf("want 3 indexed, got %d", cs.indexed)
	}
}
