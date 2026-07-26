// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"context"
	"testing"
)

// TestBrandAccessor covers decoding a domain's stored config_json into a Brand:
// no config and malformed JSON both read as "no brand" (so the caller serves the
// primary settings unchanged), and a real config decodes its overrides.
func TestBrandAccessor(t *testing.T) {
	// No config_json → no brand.
	if _, ok := (Domain{ConfigJSON: ""}).Brand(); ok {
		t.Error("empty config_json should yield no brand")
	}
	if _, ok := (Domain{ConfigJSON: "   "}).Brand(); ok {
		t.Error("blank config_json should yield no brand")
	}
	// Malformed JSON is presentational-safe: no brand, never an error/panic.
	if _, ok := (Domain{ConfigJSON: "{not json"}).Brand(); ok {
		t.Error("malformed config_json should yield no brand")
	}
	// A config with an all-empty brand still reads as "no brand" so the resolve
	// path short-circuits to the primary settings.
	if _, ok := (Domain{ConfigJSON: `{"brand":{}}`}).Brand(); ok {
		t.Error("empty brand object should yield no brand")
	}
	// A real brand decodes its overrides.
	d := Domain{ConfigJSON: `{"brand":{"site_name":"Shop","accent_light":"#2563eb"}}`}
	b, ok := d.Brand()
	if !ok {
		t.Fatal("expected a brand")
	}
	if b.SiteName != "Shop" || b.AccentLight != "#2563eb" {
		t.Errorf("decoded brand = %+v", b)
	}
	if b.Tagline != "" {
		t.Errorf("unset field should stay empty, got %q", b.Tagline)
	}
}

// TestEncodeBrandConfig confirms an empty brand stores nothing (so Brand()'s
// short-circuit holds) and a non-empty brand round-trips through config_json.
func TestEncodeBrandConfig(t *testing.T) {
	if s, err := EncodeBrandConfig(Brand{}); err != nil || s != "" {
		t.Fatalf("empty brand: got (%q,%v), want (\"\",nil)", s, err)
	}
	in := Brand{SiteName: "Shop", Tagline: "Deals", ThemeColor: "#0f172a"}
	cfg, err := EncodeBrandConfig(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, ok := (Domain{ConfigJSON: cfg}).Brand()
	if !ok {
		t.Fatal("re-decode yielded no brand")
	}
	if out != in {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
}

// TestSetBrand exercises the write path: a secondary domain's brand is stored,
// resolvable, and clearable; the primary domain is refused.
func TestSetBrand(t *testing.T) {
	r := newTestRegistry(t)
	ctx := context.Background()
	if err := r.EnsurePrimary(ctx, "primary.example", "blog"); err != nil {
		t.Fatalf("seed primary: %v", err)
	}
	sec, err := r.Create(ctx, "shop.example", SiteBlog, false)
	if err != nil {
		t.Fatalf("create secondary: %v", err)
	}

	// Store a brand and read it back through Resolve (cache-invalidated by the write).
	want := Brand{SiteName: "Shop", Tagline: "Great deals", AccentLight: "#2563eb", ThemeColor: "#0f172a"}
	if err := r.SetBrand(ctx, sec.ID, want); err != nil {
		t.Fatalf("set brand: %v", err)
	}
	got, err := r.Resolve(ctx, "shop.example")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	b, ok := got.Brand()
	if !ok || b != want {
		t.Fatalf("resolved brand = %+v (ok=%v), want %+v", b, ok, want)
	}

	// Clearing stores '' so the resolve path serves primary settings unchanged.
	if err := r.SetBrand(ctx, sec.ID, Brand{}); err != nil {
		t.Fatalf("clear brand: %v", err)
	}
	got, _ = r.Resolve(ctx, "shop.example")
	if got.ConfigJSON != "" {
		t.Errorf("cleared brand left config_json = %q, want empty", got.ConfigJSON)
	}
	if _, ok := got.Brand(); ok {
		t.Error("cleared domain should report no brand")
	}

	// The primary's identity is the global Website settings — SetBrand refuses it.
	p, _ := r.Primary(ctx)
	if err := r.SetBrand(ctx, p.ID, want); err == nil {
		t.Error("SetBrand should refuse the primary domain")
	}
}
