// SPDX-License-Identifier: Apache-2.0

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
	body := rr.Body.String()
	if !strings.Contains(body, "addEventListener") {
		t.Error("service worker body looks empty")
	}
	// New-mail notifications are shown via the worker so the installed PWA and
	// mobile browsers work; the click must route to the mailbox URL.
	if !strings.Contains(body, "notificationclick") {
		t.Error("service worker must handle notificationclick to open the mailbox on tap")
	}
	// It must remain a zero-cache worker — never persist a console response.
	if !strings.Contains(body, "caches.keys()") || strings.Contains(body, "caches.open(") {
		t.Error("service worker must stay zero-cache (purge caches, never open one)")
	}
	// Navigations must be fetched with redirect:'manual' so a server redirect (the
	// Clearnet↔Tor world switch, logout, login) is not turned into a `redirected`
	// response the browser refuses to use for a navigation.
	if !strings.Contains(body, "redirect: 'manual'") {
		t.Error("navigation fetch must use redirect:'manual' or server redirects break in the installed PWA")
	}
}
