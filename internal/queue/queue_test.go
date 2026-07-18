package queue

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

func init() {
	config.Cfg.DBPath = ":memory:"
	config.Cfg.WorkerCount = 1
	config.Cfg.MaintenanceMode = false
	config.Cfg.QueueSaturationWarn = 100
	config.Cfg.ReplayBatchLimit = 10
	config.Cfg.MaxReplayCount = 3
	dbpkg.Init() //nolint:errcheck
}

func writeJSON(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, code int, errCode, msg, docs string) {
	w.WriteHeader(code)
}

func TestHandleQueueStatus(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/queue", nil)
	rr := httptest.NewRecorder()
	HandleQueueStatus(rr, req, writeJSON)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := body["pending"]; !ok {
		t.Fatal("response missing 'pending' field")
	}
	if _, ok := body["maintenance_mode"]; !ok {
		t.Fatal("response missing 'maintenance_mode' field")
	}
}

func TestHandleQueueReplay_Empty(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/v1/queue/replay", nil)
	rr := httptest.NewRecorder()
	HandleQueueReplay(rr, req, writeJSON, writeAPIError)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("want status=ok, got %v", body["status"])
	}
}

func TestProcessOneJob_MaintenanceMode(t *testing.T) {
	config.Cfg.MaintenanceMode = true
	defer func() { config.Cfg.MaintenanceMode = false }()

	empty := processOneJob(0)
	if !empty {
		t.Fatal("maintenance mode: processOneJob should return empty=true")
	}
}

func TestProcessOneJob_EmptyQueue(t *testing.T) {
	empty := processOneJob(0)
	if !empty {
		t.Fatal("empty queue: processOneJob should return empty=true")
	}
}

func TestProcessOneJob_InsertAndProcess(t *testing.T) {
	a := dbpkg.Article{
		ID: "test-id-1", Title: "Test", Slug: "test-slug-1",
		Content: "<p>hello</p>", Tags: []string{"test"},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	payload, _ := json.Marshal(a)
	_, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	SetCacheWriteFn(func(_, _ string) {})

	empty := processOneJob(0)
	if empty {
		t.Fatal("queue had a job; should not return empty=true")
	}
	// Job should now be completed or have progressed
	var status string
	dbpkg.DB.QueryRow(`SELECT status FROM write_jobs WHERE id=(SELECT MAX(id) FROM write_jobs)`).Scan(&status)
	if status == "pending" {
		t.Fatalf("job should not still be pending after processing; got %q", status)
	}
}

// TestProcessOneJob_InsertPagePersistsIsPage proves the Stage 3b fix: a page
// (IsPage=true) enqueued for insert is written with is_page=1 atomically by the
// worker — no follow-up UPDATE, so no window where the row exists as a post.
func TestProcessOneJob_InsertPagePersistsIsPage(t *testing.T) {
	a := dbpkg.Article{
		ID: "test-page-1", Title: "About", Slug: "about-page-1",
		Content: "<p>about</p>", IsPage: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	payload, _ := json.Marshal(a)
	if _, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		t.Fatalf("insert job: %v", err)
	}
	SetCacheWriteFn(func(_, _ string) {})

	if processOneJob(0) {
		t.Fatal("queue had a job; should not return empty=true")
	}
	var isPage int
	if err := dbpkg.DB.QueryRow(`SELECT is_page FROM articles WHERE slug=?`, a.Slug).Scan(&isPage); err != nil {
		t.Fatalf("page row not found after processing: %v", err)
	}
	if isPage != 1 {
		t.Fatalf("is_page = %d, want 1 (page must be written with the flag set atomically)", isPage)
	}
}

func TestBackoffCap(t *testing.T) {
	// Ensure maxBackoffSeconds is capped (regression guard for ADR-0035)
	if maxBackoffSeconds > 300 {
		t.Fatalf("maxBackoffSeconds should be <= 300, got %d", maxBackoffSeconds)
	}
}
