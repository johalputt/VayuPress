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
	// it cannot break out of the attribute or the surrounding markup.
	if strings.Contains(form, `data-brands="`+brandJSON+`"`) {
		t.Errorf("brand JSON must be html-escaped in the attribute, not raw")
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
