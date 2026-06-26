package main

import (
	"net/http/httptest"
	"testing"

	"github.com/johalputt/vayupress/internal/theme"
)

// TestPreviewDraftRoundTrip proves a stored draft can be fetched by id and that
// unknown ids are reported missing.
func TestPreviewDraftRoundTrip(t *testing.T) {
	id := previewDraftPut("body{color:red}")
	if id == "" {
		t.Fatal("expected a draft id")
	}
	css, ok := previewDraftGet(id)
	if !ok || css != "body{color:red}" {
		t.Fatalf("round-trip failed: ok=%v css=%q", ok, css)
	}
	if _, ok := previewDraftGet("does-not-exist"); ok {
		t.Error("unknown draft id should not resolve")
	}
}

// TestPreviewDraftCap proves the store never grows past its hard cap, so a busy
// customizer session can't leak memory.
func TestPreviewDraftCap(t *testing.T) {
	for i := 0; i < previewDraftMax+50; i++ {
		previewDraftPut("x")
	}
	previewDraftMu.Lock()
	n := len(previewDraftStore)
	previewDraftMu.Unlock()
	if n > previewDraftMax {
		t.Errorf("draft store exceeded cap: %d > %d", n, previewDraftMax)
	}
}

// TestResolvePreviewTokens proves the preview resolver mirrors Apply's selection
// logic for the two paths that don't need the DB: explicit tokens and a named
// preset. An empty body resolves to nothing.
func TestResolvePreviewTokens(t *testing.T) {
	var a App
	r := httptest.NewRequest("POST", "/os/api/theme/preview-draft", nil)

	custom := theme.Tokens{Name: "Mine", AccentDark: "#abcdef"}
	got, ok := a.resolvePreviewTokens(r, "", &custom, nil)
	if !ok || got.Name != "Mine" || got.AccentDark != "#abcdef" {
		t.Errorf("explicit tokens not returned verbatim: %+v ok=%v", got, ok)
	}

	got, ok = a.resolvePreviewTokens(r, "Default", nil, nil)
	if !ok || got.Name != "Default" {
		t.Errorf("preset path failed: %+v ok=%v", got, ok)
	}

	if _, ok := a.resolvePreviewTokens(r, "no-such-preset", nil, nil); ok {
		t.Error("unknown preset should not resolve")
	}
	if _, ok := a.resolvePreviewTokens(r, "", nil, nil); ok {
		t.Error("empty body should not resolve")
	}
}

// TestPreviewDraftCompilesFullDesign proves a draft compiled from a design theme
// preset (which ships component CSS) yields a stylesheet far richer than a bare
// palette — guarding that the live preview reflects the WHOLE design, not just
// colours.
func TestPreviewDraftCompilesFullDesign(t *testing.T) {
	apex, ok := findPreset("Apex")
	if !ok {
		t.Skip("Apex preset not present")
	}
	css, err := theme.CompileCSS(apex)
	if err != nil {
		t.Fatalf("compile Apex: %v", err)
	}
	id := previewDraftPut(css)
	got, ok := previewDraftGet(id)
	if !ok {
		t.Fatal("draft not stored")
	}
	if len(got) < 2000 {
		t.Errorf("compiled design CSS unexpectedly small (%d bytes) — preview may only carry colours", len(got))
	}
}
