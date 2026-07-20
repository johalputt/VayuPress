package main

import "testing"

// TestDetectFaviconType locks the accepted logo/favicon formats. The PNG/ICO-only
// limit was the usual reason "the logo won't change" — a JPEG/WebP/GIF logo was
// silently rejected — so those raster formats are now accepted. SVG stays refused
// (an SVG served same-origin can carry active content → XSS).
func TestDetectFaviconType(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		mime string
		ok   bool
	}{
		{"png", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0}, "image/png", true},
		{"ico", []byte{0x00, 0x00, 0x01, 0x00, 0, 0}, "image/x-icon", true},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, "image/jpeg", true},
		{"gif89", []byte("GIF89a-more-bytes"), "image/gif", true},
		{"gif87", []byte("GIF87a-more-bytes"), "image/gif", true},
		{"webp", append([]byte("RIFF\x00\x00\x00\x00WEBP"), 0, 0), "image/webp", true},
		{"svg-rejected", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), "", false},
		{"plain-text", []byte("not an image at all"), "", false},
		{"empty", []byte{}, "", false},
		{"riff-not-webp", []byte("RIFF\x00\x00\x00\x00AVI "), "", false},
		{"too-short", []byte{0xFF, 0xD8}, "", false},
	}
	for _, c := range cases {
		mime, ok := detectFaviconType(c.b)
		if ok != c.ok || mime != c.mime {
			t.Errorf("%s: detectFaviconType = (%q,%v), want (%q,%v)", c.name, mime, ok, c.mime, c.ok)
		}
	}
}
