package render

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestFooterDefaultBar verifies that with no operator config the footer still
// renders a clean default bottom bar: an auto copyright line with the current
// year, the Powered-by credit, and the runtime badge — and no column markup.
func TestFooterDefaultBar(t *testing.T) {
	out := string(footerHTML(SiteSettings{Name: "Acme"}))
	year := strconv.Itoa(time.Now().UTC().Year())
	if !strings.Contains(out, "© "+year+" Acme.") {
		t.Errorf("default copyright with year/site missing: %s", out)
	}
	if !strings.Contains(out, "Powered by") || !strings.Contains(out, "vayupress.com") {
		t.Error("powered-by credit missing")
	}
	if !strings.Contains(out, "vayu-footer-badge") {
		t.Error("runtime badge missing")
	}
	if strings.Contains(out, "vayu-footer-cols") {
		t.Error("no columns should render for an empty config")
	}
}

// TestFooterPremiumRender verifies columns, social, legal links and the token
// expansion render, and that an unsafe href is dropped — proving the footer is
// a real, escaped, multi-section premium footer (not just a copyright line).
func TestFooterPremiumRender(t *testing.T) {
	cfg := `{
      "tagline":"Sovereign publishing.",
      "copyright":"© {year} {site} — built in the open.",
      "columns":[{"title":"Explore","links":[{"label":"Home","href":"/"},{"label":"Bad","href":"javascript:alert(1)"}]}],
      "social":[{"label":"RSS","href":"/feed.xml"}],
      "legal":[{"label":"Privacy","href":"/privacy"},{"label":"Terms","href":"/terms"}]
    }`
	out := string(footerHTML(SiteSettings{Name: "Acme", FooterJSON: cfg}))
	year := strconv.Itoa(time.Now().UTC().Year())

	for _, want := range []string{
		"vayu-footer-main", "vayu-footer-cols",
		"Sovereign publishing.",
		"Explore", `href="/"`, ">Home<",
		"vayu-footer-social", "RSS",
		"vayu-footer-legal", "Privacy", `href="/privacy"`, "Terms",
		"© " + year + " Acme — built in the open.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("premium footer missing %q\n---\n%s", want, out)
		}
	}
	// The javascript: link must be filtered out by safeNavHref.
	if strings.Contains(out, "javascript:") {
		t.Errorf("unsafe href leaked into footer: %s", out)
	}
}
