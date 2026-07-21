package main

import (
	"strings"
	"testing"
)

// TestMaintenancePageHTML checks the public maintenance page is a self-contained
// premium page (no external CSS/JS that maintenance would block) and that a
// custom operator message is HTML-escaped, not injected raw.
func TestMaintenancePageHTML(t *testing.T) {
	page := maintenancePageHTML(`<script>alert(1)</script>`)
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Error("operator message must be HTML-escaped, not injected raw")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("expected the message to be escaped")
	}
	for _, want := range []string{"We’ll be right back", "http-equiv=\"refresh\"", "powered by VayuPress"} {
		if !strings.Contains(page, want) {
			t.Errorf("maintenance page missing %q", want)
		}
	}
	// Self-contained: no external stylesheet/script the maintenance gate would block.
	if strings.Contains(page, "/static/") || strings.Contains(page, "/theme.css") || strings.Contains(page, "<script src") {
		t.Error("maintenance page must be fully self-contained")
	}
	// Default (empty) message falls back to the friendly copy.
	if !strings.Contains(maintenancePageHTML(""), "back online shortly") {
		t.Error("empty message should use the default copy")
	}
}
