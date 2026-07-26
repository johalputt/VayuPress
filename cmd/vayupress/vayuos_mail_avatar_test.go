// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

// TestDetectAvatarType pins the magic-byte image sniffing (the client Content-Type
// is never trusted): real PNG/JPEG/GIF/WebP are accepted, anything else rejected.
func TestDetectAvatarType(t *testing.T) {
	ok := map[string][]byte{
		"image/png":  {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0},
		"image/jpeg": {0xFF, 0xD8, 0xFF, 0xE0, 0, 0},
		"image/gif":  []byte("GIF89a....."),
		"image/webp": append([]byte("RIFF"), append([]byte{0, 0, 0, 0}, []byte("WEBPxxxx")...)...),
	}
	for want, b := range ok {
		if got, valid := detectAvatarType(b); !valid || got != want {
			t.Errorf("detectAvatarType(%s) = (%q,%v), want (%q,true)", want, got, valid, want)
		}
	}
	bad := [][]byte{
		nil,
		[]byte("not an image"),
		{0x25, 0x50, 0x44, 0x46}, // %PDF
		[]byte("<svg>"),          // SVG is script-capable → rejected
		{0x89, 'P'},              // truncated PNG magic
	}
	for _, b := range bad {
		if _, valid := detectAvatarType(b); valid {
			t.Errorf("detectAvatarType(%q) accepted a non-image", b)
		}
	}
}
