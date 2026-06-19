package main

import "testing"

// TestDetectImageType verifies content-based (magic-number) validation accepts
// the four supported raster formats and rejects everything else — crucially
// SVG, which is an XSS vector when served same-origin.
func TestDetectImageType(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00}
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00}
	gif87 := []byte("GIF87a....")
	gif89 := []byte("GIF89a....")
	webp := append([]byte("RIFF\x00\x00\x00\x00"), []byte("WEBP")...)
	riffWav := append([]byte("RIFF\x00\x00\x00\x00"), []byte("WAVE")...)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)

	cases := []struct {
		name    string
		data    []byte
		wantExt string
		wantOK  bool
	}{
		{"png", png, "png", true},
		{"jpeg", jpg, "jpg", true},
		{"gif87a", gif87, "gif", true},
		{"gif89a", gif89, "gif", true},
		{"webp", webp, "webp", true},
		{"riff-but-not-webp", riffWav, "", false},
		{"svg-rejected", svg, "", false},
		{"empty", nil, "", false},
		{"garbage", []byte("not an image"), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ext, _, ok := detectImageType(c.data)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && ext != c.wantExt {
				t.Fatalf("ext = %q, want %q", ext, c.wantExt)
			}
		})
	}
}

// TestSafeMediaName ensures the serve route only accepts names this server
// generates (32 hex + known ext) and rejects path-traversal / odd inputs.
func TestSafeMediaName(t *testing.T) {
	good := []string{
		"0123456789abcdef0123456789abcdef.png",
		"ffffffffffffffffffffffffffffffff.jpg",
		"00000000000000000000000000000000.gif",
		"abcdefabcdefabcdefabcdefabcdefab.webp",
	}
	bad := []string{
		"../../etc/passwd",
		"0123456789abcdef0123456789abcdef.svg", // svg not allowed
		"0123456789abcdef0123456789abcdef.png/../x",
		"SHORT.png",
		"0123456789abcdef0123456789abcdef.PNG", // uppercase ext
		"0123456789abcdef0123456789abcdeg.png", // 'g' not hex
		"0123456789abcdef0123456789abcdef.exe",
		"",
	}
	for _, n := range good {
		if !safeMediaName.MatchString(n) {
			t.Errorf("expected %q to be accepted", n)
		}
	}
	for _, n := range bad {
		if safeMediaName.MatchString(n) {
			t.Errorf("expected %q to be rejected", n)
		}
	}
}
