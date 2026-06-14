package main

import "net/http"

// brandMarkSVG is the VayuPress icon mark (navy "V" + blue triple-swoosh wing),
// served as the favicon and the inline brand glyph. Kept in sync with
// docs/assets/vayupress-mark.svg.
const brandMarkSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" fill="none" role="img" aria-label="VayuPress">` +
	`<defs><linearGradient id="vpWing" x1="290" y1="330" x2="480" y2="150" gradientUnits="userSpaceOnUse">` +
	`<stop offset="0" stop-color="#2563eb"/><stop offset="1" stop-color="#3b82f6"/></linearGradient></defs>` +
	`<path d="M118 150 L256 402 L360 214" stroke="#0b1220" stroke-width="62" stroke-linejoin="miter" stroke-linecap="butt" fill="none"/>` +
	`<g stroke="url(#vpWing)" stroke-width="26" stroke-linecap="round" fill="none">` +
	`<path d="M298 250 C356 176 426 156 470 168"/>` +
	`<path d="M298 286 C354 218 424 202 466 214"/>` +
	`<path d="M298 322 C350 262 414 250 448 260"/></g></svg>`

// serveBrandMark serves the brand mark as an immutable SVG asset.
func serveBrandMark(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	_, _ = w.Write([]byte(brandMarkSVG))
}
