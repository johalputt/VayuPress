package blockrender

import (
	"strings"
	"testing"
)

// TestImageExternalURLRenders confirms a direct external image link is rendered
// as-is (no re-hosting) and carries referrerpolicy=no-referrer so it loads past
// simple hotlink protection and does not leak the reader's page URL.
func TestImageExternalURLRenders(t *testing.T) {
	out, _, err := Render(`[{"type":"image","url":"https://cdn.pixabay.com/photo/x.jpg","alt":"cat"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `src="https://cdn.pixabay.com/photo/x.jpg"`) {
		t.Errorf("external image should render as a direct link:\n%s", out)
	}
	if !strings.Contains(out, `referrerpolicy="no-referrer"`) {
		t.Errorf("external image should carry referrerpolicy=no-referrer:\n%s", out)
	}
}

// TestImageDangerousURLDropped confirms a dangerous URL scheme never produces an
// <img> (not even a src-stripped one).
func TestImageDangerousURLDropped(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "vbscript:x", "data:text/html,<script>"} {
		out, _, _ := Render(`[{"type":"image","url":"` + bad + `","alt":"x"}]`)
		if strings.Contains(out, "<img") {
			t.Errorf("dangerous image URL %q must not render an <img>:\n%s", bad, out)
		}
	}
}

// TestSafeImageURL covers the render-time scheme allowlist directly.
func TestSafeImageURL(t *testing.T) {
	ok := []string{"/media/a.png", "/x/y.jpg", "https://ex.com/a.png", "http://ex.com/a.png", "HTTPS://EX/a"}
	no := []string{"", "  ", "javascript:x", "data:image/png;base64,AAA", "ftp://x/a", "vbscript:x"}
	for _, u := range ok {
		if !safeImageURL(u) {
			t.Errorf("safeImageURL(%q) = false, want true", u)
		}
	}
	for _, u := range no {
		if safeImageURL(u) {
			t.Errorf("safeImageURL(%q) = true, want false", u)
		}
	}
}
