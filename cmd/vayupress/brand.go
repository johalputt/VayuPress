package main

import "net/http"

// brandMarkSVG is the VayuPress icon mark served as the favicon and inline brand
// glyph. It uses a CSS media query so browsers automatically serve the correct
// colour variant in dark mode. Kept in sync with docs/assets/vayupress-mark.svg.
const brandMarkSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 580 480" fill="none" role="img" aria-label="VayuPress">` +
	`<defs><style>` +
	`.vp-v{fill:#0b1220}.vp-w1{fill:#3b82f6}.vp-w2{fill:#2563eb}.vp-w3{fill:#1d4ed8}` +
	`@media(prefers-color-scheme:dark){.vp-v{fill:#e2e8f0}.vp-w1{fill:#60a5fa}.vp-w2{fill:#3b82f6}.vp-w3{fill:#2563eb}}` +
	`</style></defs>` +
	`<polygon class="vp-v" points="66,88 134,88 264,396 256,410 246,396 274,88 340,88 256,416"/>` +
	`<path class="vp-w3" d="M310,258 C358,236 450,228 566,242 C566,258 450,258 358,264 C350,264 330,266 310,278 Z"/>` +
	`<path class="vp-w2" d="M294,196 C346,166 450,156 568,170 C568,186 450,186 346,194 C336,196 316,200 294,216 Z"/>` +
	`<path class="vp-w1" d="M278,132 C336,96 448,86 570,100 C570,116 448,118 336,124 C324,126 304,132 278,152 Z"/>` +
	`</svg>`

// serveBrandMark serves the mode-aware brand mark as an immutable SVG asset.
func serveBrandMark(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
	_, _ = w.Write([]byte(brandMarkSVG))
}
