// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBizSitePreviewShowsSelectedDesign guards the "Preview shows the wrong
// (bistro) design" bug: the /site preview must honour ?preview=<design> so an
// operator sees the design they selected before saving, with matching CSS.
func TestBizSitePreviewShowsSelectedDesign(t *testing.T) {
	a := &App{} // no settings store → saved/active design is the default (bistro)

	// No preview → default design.
	rec := httptest.NewRecorder()
	a.handleBizSite(rec, httptest.NewRequest(http.MethodGet, "/site", nil))
	if !strings.Contains(rec.Body.String(), "vb--bistro") {
		t.Fatalf("default preview should render bistro, body lacked vb--bistro")
	}

	// ?preview=studio → studio markup AND a studio-versioned stylesheet link.
	rec = httptest.NewRecorder()
	a.handleBizSite(rec, httptest.NewRequest(http.MethodGet, "/site?preview=studio", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "vb--studio") {
		t.Errorf("preview=studio did not render the studio design")
	}
	if strings.Contains(body, `class="vb vb--bistro"`) {
		t.Errorf("preview=studio still rendered the bistro body class")
	}
	if !strings.Contains(body, "/site.css?v=studio") {
		t.Errorf("preview page did not version the stylesheet to studio")
	}

	// The stylesheet endpoint must serve the studio design for that preview.
	rec = httptest.NewRecorder()
	a.handleBizSiteCSS(rec, httptest.NewRequest(http.MethodGet, "/site.css?v=studio", nil))
	if !strings.Contains(rec.Body.String(), "vb--studio") {
		t.Errorf("/site.css?v=studio did not serve the studio stylesheet")
	}

	// An unknown preview key falls back to the saved/active design (no crash,
	// no accidental bistro override of a real selection).
	rec = httptest.NewRecorder()
	a.handleBizSite(rec, httptest.NewRequest(http.MethodGet, "/site?preview=does-not-exist", nil))
	if !strings.Contains(rec.Body.String(), "vb--bistro") {
		t.Errorf("unknown preview key should fall back to the saved design")
	}
}
