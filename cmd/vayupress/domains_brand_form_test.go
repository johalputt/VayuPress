package main

import (
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/domain"
)

// TestDomainsBrandFormEmbedsBrandsViaAttribute pins the CWE-116 fix: the brand
// map is passed through an html-escaped data attribute and JSON.parsed by the
// page script, never interpolated raw into the inline <script> (which CodeQL
// flagged as potentially unsafe quoting).
func TestDomainsBrandFormEmbedsBrandsViaAttribute(t *testing.T) {
	domains := []domain.Domain{
		{ID: "p", Host: "primary.example", IsPrimary: true},
		{ID: "s1", Host: "shop.example"},
	}
	brandJSON := `{"s1":{"site_name":"Shop"}}`
	form := domainsBrandForm(domains, brandJSON)
	if !strings.Contains(form, `id="dom-brands"`) || !strings.Contains(form, `data-brands="`) {
		t.Fatalf("brand form should carry the brands in a data attribute:\n%s", form)
	}
	// The raw JSON must be html-escaped inside the attribute (quotes escaped), so
	// it cannot break out of the attribute or the surrounding markup. html/template
	// owns the quoting, so every JSON double quote is rendered as the &#34; entity.
	if strings.Contains(form, `data-brands="`+brandJSON+`"`) {
		t.Errorf("brand JSON must be html-escaped in the attribute, not raw")
	}
	if !strings.Contains(form, `&#34;`) {
		t.Errorf("brand JSON quotes should be html-entity-escaped by the template:\n%s", form)
	}
	// A hostile brand value carrying a double quote must not be able to close the
	// attribute — the raw `">` breakout sequence must never survive into the markup.
	hostile := domainsBrandForm(domains, `{"s1":{"site_name":"\"><script>x"}}`)
	if strings.Contains(hostile, `"><script>`) {
		t.Fatalf("brand value broke out of the attribute quoting:\n%s", hostile)
	}

	// The page script reads the brands from the element and never interpolates
	// the raw JSON literally.
	script := domainsScript("nonce123")
	if strings.Contains(script, brandJSON) {
		t.Errorf("script must not interpolate raw brand JSON inline")
	}
	if !strings.Contains(script, "getElementById('dom-brands')") {
		t.Errorf("script should read brands from the data element")
	}

	// A single-domain install has no secondary → no brand form and no data node.
	if got := domainsBrandForm([]domain.Domain{{ID: "p", Host: "primary.example", IsPrimary: true}}, "{}"); got != "" {
		t.Errorf("single-domain install should render no brand form, got %q", got)
	}
}
