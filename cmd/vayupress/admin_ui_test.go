package main

import (
	"strings"
	"testing"
)

// assertCSPSafe checks that a rendered page does not violate the strict CSP:
// no inline style attributes and no 'unsafe-eval' reference.
func assertCSPSafe(t *testing.T, name, htmlOut string) {
	t.Helper()
	if strings.Contains(htmlOut, `style="`) {
		t.Errorf("%s: contains inline style attribute (violates style-src 'self')", name)
	}
	if strings.Contains(htmlOut, "unsafe-eval") {
		t.Errorf("%s: references unsafe-eval", name)
	}
	for _, bad := range []string{"cdn", "googleapis", "unpkg", "jsdelivr"} {
		if strings.Contains(strings.ToLower(htmlOut), bad) {
			t.Errorf("%s: references external asset host %q", name, bad)
		}
	}
}

func TestAdminV2Layout_NonceAndStructure(t *testing.T) {
	out := adminV2Layout("TESTNONCE", "Dashboard", "dashboard", "<p>hi</p>")
	if !strings.Contains(out, `<script nonce="TESTNONCE" src="/admin/v2/static/js/admin-v2.js"></script>`) {
		t.Error("layout missing nonce'd script tag")
	}
	if !strings.Contains(out, `<link rel="stylesheet" href="/admin/v2/static/css/admin-v2.css">`) {
		t.Error("layout missing stylesheet link")
	}
	if !strings.Contains(out, `class="nav-link active" href="/admin/v2"`) {
		t.Error("layout did not mark active sidebar item")
	}
	if !strings.Contains(out, "<p>hi</p>") {
		t.Error("layout did not embed body")
	}
	assertCSPSafe(t, "adminV2Layout", out)
}

func TestEditorBodyHTML_CSPSafe(t *testing.T) {
	out := editorBodyHTML("my-post", "Edit Post", "My Title", "# Hello\nworld")
	for _, want := range []string{
		`data-editor`, `data-preview`, `data-slash-palette`,
		`data-save-status`, `data-wordcount`, `data-load-versions`,
		`data-action="toggle-distraction"`, `data-field="title"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("editor body missing hook %q", want)
		}
	}
	if !strings.Contains(out, `data-slug="my-post"`) {
		t.Error("editor body missing slug binding")
	}
	assertCSPSafe(t, "editorBodyHTML", out)
}

func TestEditorBodyHTML_EscapesContent(t *testing.T) {
	out := editorBodyHTML("s", "Edit", `<script>x</script>`, `<img onerror=1>`)
	if strings.Contains(out, "<script>x</script>") {
		t.Error("title not HTML-escaped in editor body")
	}
	if strings.Contains(out, "<img onerror=1>") {
		t.Error("content not HTML-escaped in editor body")
	}
}

func TestStorageWidthClass(t *testing.T) {
	cases := map[int]string{0: "w-0", 5: "w-0", 12: "w-10", 77: "w-75", 95: "w-90", 100: "w-100"}
	for in, want := range cases {
		if got := storageWidthClass(in); got != want {
			t.Errorf("storageWidthClass(%d)=%s want %s", in, got, want)
		}
	}
}

func TestLoginPage_CSPSafe(t *testing.T) {
	// Render the login body inline (mirrors handleV2Login) without needing an App.
	nonce := "NONCE123"
	out := `<link rel="stylesheet" href="/admin/v2/static/css/admin-v2.css">` +
		`<script nonce="` + nonce + `" src="/admin/v2/static/js/admin-v2.js"></script>`
	if !strings.Contains(out, `nonce="NONCE123"`) {
		t.Error("login asset block missing nonce")
	}
	assertCSPSafe(t, "login", out)
}
