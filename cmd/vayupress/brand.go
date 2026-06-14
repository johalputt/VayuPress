package main

import (
	_ "embed"
	"net/http"
)

// The official VayuPress brand marks (recovered originals). Two variants are
// served so the UI and browser tab adapt to colour scheme:
//   - favicon-dark:  dark-navy "V" + blue wings, for light backgrounds
//   - favicon-light: white "V" + blue wings, for dark backgrounds (the app's
//     console and public site use a dark theme)
// Full lockups live in docs/assets/ for the README and documentation.

//go:embed assets/favicon-dark.png
var faviconDarkPNG []byte

//go:embed assets/favicon-light.png
var faviconLightPNG []byte

func servePNG(b []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		_, _ = w.Write(b)
	}
}
