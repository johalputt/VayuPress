//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

// TestThemePreviewLiveFlow exercises the Theme Studio live-preview pipeline end
// to end through the real router: POST a token payload to /os/api/theme/preview-draft,
// then load the preview page and stylesheet by the returned id. This is the path
// the customizer's live iframe depends on.
func TestThemePreviewLiveFlow(t *testing.T) {
	srv, key := newTestHarness(t)
	csrf := auth.GenerateCSRFToken()

	// 1. Create a draft from a token payload (mirrors what the Studio JS sends).
	payload, _ := json.Marshal(map[string]interface{}{
		"tokens": map[string]interface{}{
			"Name":       "Probe",
			"BgDark":     "#101014",
			"TextDark":   "#e8e8ea",
			"AccentDark": "#7c5cff",
			"options":    map[string]string{"corners": "sharp"},
		},
	})
	req, _ := http.NewRequest("POST", srv.URL+"/os/api/theme/preview-draft", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", key)
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: csrf})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preview-draft request: %v", err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("preview-draft want 200, got %d: %s", resp.StatusCode, b)
	}
	var draft struct {
		ID      string `json:"id"`
		CSSHref string `json:"css_href"`
	}
	json.NewDecoder(resp.Body).Decode(&draft)
	resp.Body.Close()
	if draft.ID == "" || draft.CSSHref == "" {
		t.Fatalf("preview-draft returned empty id/href: %+v", draft)
	}

	// 2. The preview page must render the public sample markup and link the
	//    draft stylesheet, and allow same-origin framing.
	pg := doRequest(t, srv, "GET", "/os/theme/preview?draft="+draft.ID, key, nil)
	if pg.StatusCode != 200 {
		t.Fatalf("preview page want 200, got %d", pg.StatusCode)
	}
	if xfo := pg.Header.Get("X-Frame-Options"); xfo != "SAMEORIGIN" {
		t.Errorf("preview page X-Frame-Options = %q, want SAMEORIGIN (else iframe is blocked)", xfo)
	}
	if csp := pg.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'self'") {
		t.Errorf("preview page CSP must allow same-origin framing, got %q", csp)
	}
	pgBuf := new(strings.Builder)
	io.Copy(pgBuf, pg.Body)
	pg.Body.Close()
	pgHTML := pgBuf.String()
	for _, want := range []string{"vayu-hero", "vayu-post-card", "/os/theme/preview.css?draft=" + draft.ID, "/os/static/js/theme-preview-frame.js"} {
		if !strings.Contains(pgHTML, want) {
			t.Errorf("preview page missing %q", want)
		}
	}

	// 3. The stylesheet must compile and be served as CSS.
	css := doRequest(t, srv, "GET", "/os/theme/preview.css?draft="+draft.ID, key, nil)
	if css.StatusCode != 200 {
		t.Fatalf("preview.css want 200, got %d", css.StatusCode)
	}
	if ct := css.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("preview.css content-type = %q, want text/css", ct)
	}
	cssBuf := new(strings.Builder)
	io.Copy(cssBuf, css.Body)
	css.Body.Close()
	if cssBuf.Len() < 200 {
		t.Errorf("preview.css unexpectedly small (%d bytes)", cssBuf.Len())
	}
}
