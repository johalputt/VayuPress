package main

import (
	"strings"
	"testing"
)

func TestColorRowCSPSafeAndWiring(t *testing.T) {
	out := colorRow(themeColorField{Field: "AccentDark", Label: "Accent", Vari: "accent"})
	assertCSPSafe(t, "colorRow", out)
	if !strings.Contains(out, `data-token="AccentDark"`) {
		t.Error("colorRow missing canonical token field name")
	}
	if !strings.Contains(out, `data-token-var="accent"`) {
		t.Error("colorRow missing preview variable wiring")
	}
	if !strings.Contains(out, `type="color"`) {
		t.Error("colorRow not a colour input")
	}
}

// Light-mode tokens carry no preview variable.
func TestColorRowNoPreviewVar(t *testing.T) {
	out := colorRow(themeColorField{Field: "BgLight", Label: "Background", Vari: ""})
	if strings.Contains(out, "data-token-var") {
		t.Error("light token should not declare a preview variable")
	}
}

func TestTextRowEscapesAndCSPSafe(t *testing.T) {
	out := textRow("FontSans", `<script>alert(1)</script>`, `"><img onerror=x>`)
	assertCSPSafe(t, "textRow", out)
	if strings.Contains(out, "<script>alert(1)") || strings.Contains(out, "<img onerror") {
		t.Error("textRow did not escape hostile label/placeholder")
	}
	if !strings.Contains(out, `data-token="FontSans"`) {
		t.Error("textRow missing token field name")
	}
}

// The previewable --vp-* vars must be covered across the Brand (dark accents)
// and dark-mode colour sets combined — together they map to the eight preview
// variables the live preview wires up.
func TestDarkColorsCoverPreviewVars(t *testing.T) {
	want := map[string]bool{
		"bg": true, "surface": true, "text": true, "muted": true,
		"accent": true, "accent2": true, "hi": true, "green": true,
	}
	fields := append(brandColorFields(), themeDarkColors()...)
	for _, f := range fields {
		if f.Vari == "" {
			continue // light-mode accents in the brand set carry no preview var
		}
		if !want[f.Vari] {
			t.Errorf("unexpected preview var %q for field %q", f.Vari, f.Field)
		}
		delete(want, f.Vari)
	}
	if len(want) != 0 {
		t.Errorf("Brand + dark colour sets missing preview vars: %v", want)
	}
}
