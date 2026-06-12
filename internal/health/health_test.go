package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/metrics"
)

func init() {
	config.Cfg.APIKey = "test"
	config.Cfg.DBPath = ":memory:"
	config.Cfg.CacheDir = "/tmp"
	config.Cfg.WorkerCount = 2
	config.Cfg.QueueSaturationWarn = 100
	config.Cfg.StorageQuotaGB = 10
	dbpkg.Init() //nolint:errcheck

	Version = "1.0.0-test"
	ConfigVersion = "1.0"
	BootTime = time.Now().Add(-5 * time.Minute)

	WriteJSON = func(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	WriteAPIError = func(w http.ResponseWriter, r *http.Request, code int, errCode, msg, docs string) {
		w.WriteHeader(code)
	}
}

func get(handler http.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func TestHandleHealthLiveness(t *testing.T) {
	rr := get(HandleHealthLiveness)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "alive" {
		t.Fatalf("want status=alive, got %v", body["status"])
	}
	if body["schema_version"] != healthSchemaVersion {
		t.Fatalf("schema_version mismatch: %v", body["schema_version"])
	}
}

func TestHandleHealthDB(t *testing.T) {
	rr := get(HandleHealthDB)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHealthReady_NoWorkers(t *testing.T) {
	orig := metrics.WorkerLiveness
	metrics.WorkerLiveness = 0
	defer func() { metrics.WorkerLiveness = orig }()

	rr := get(HandleHealthReady)
	if rr.Code != 503 {
		t.Fatalf("want 503 when no workers, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no workers") {
		t.Fatalf("expected 'no workers' reason, got: %s", rr.Body.String())
	}
}

func TestHandleHealthReady_WithWorkers(t *testing.T) {
	metrics.WorkerLiveness = 1
	defer func() { metrics.WorkerLiveness = 0 }()

	rr := get(HandleHealthReady)
	if rr.Code != 200 {
		t.Fatalf("want 200 with workers alive, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHealthEthics(t *testing.T) {
	rr := get(HandleHealthEthics)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	var body map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&body)
	if body["compliant"] != true {
		t.Fatalf("want compliant=true, got %v", body["compliant"])
	}
}

func TestHandleHealthMeilisearch_Down(t *testing.T) {
	MeiliDoFn = nil
	rr := get(HandleHealthMeilisearch)
	if rr.Code != 503 {
		t.Fatalf("nil MeiliDoFn: want 503, got %d", rr.Code)
	}
}

func TestHandleHealthMeilisearch_Up(t *testing.T) {
	MeiliDoFn = func(method, path string, body interface{}) error { return nil }
	defer func() { MeiliDoFn = nil }()
	rr := get(HandleHealthMeilisearch)
	if rr.Code != 200 {
		t.Fatalf("healthy meili: want 200, got %d", rr.Code)
	}
}

func TestHandleHealthStorage(t *testing.T) {
	rr := get(HandleHealthStorage)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}

func TestHandleHealthQueue(t *testing.T) {
	rr := get(HandleHealthQueue)
	if rr.Code != 200 {
		t.Fatalf("want 200, got %d", rr.Code)
	}
}
