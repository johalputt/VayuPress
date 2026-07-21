package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOSManifest(t *testing.T) {
	rr := httptest.NewRecorder()
	(&App{}).handleOSManifest(rr, httptest.NewRequest(http.MethodGet, "/os/manifest.webmanifest", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "manifest") {
		t.Errorf("Content-Type = %q, want a manifest type", ct)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if m["start_url"] != "/os" || m["scope"] != "/os/" {
		t.Errorf("start_url/scope = %v/%v, want /os and /os/", m["start_url"], m["scope"])
	}
	if _, ok := m["icons"].([]any); !ok {
		t.Error("manifest must declare icons")
	}
}

func TestOSServiceWorker(t *testing.T) {
	rr := httptest.NewRecorder()
	(&App{}).handleOSServiceWorker(rr, httptest.NewRequest(http.MethodGet, "/os/sw.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Header().Get("Service-Worker-Allowed"); got != "/os/" {
		t.Errorf("Service-Worker-Allowed = %q, want /os/", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	if !strings.Contains(rr.Body.String(), "addEventListener") {
		t.Error("service worker body looks empty")
	}
}
