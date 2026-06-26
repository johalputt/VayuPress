package main

// flags_embed.go — country flag SVGs (flag-icons, MIT) compiled into the binary.
//
// Embedding (rather than serving from STATIC_DIR on disk) guarantees flags are
// always available the moment the new binary is deployed, with no dependency on
// a separate static-asset copy step. They are served same-origin under
// /os/static/flags/<cc>.svg, so the strict CSP (img-src 'self') still covers
// them and no third-party request is ever made.

import (
	"embed"
	"strings"
)

//go:embed flags/*.svg
var flagFS embed.FS

// flagCodeSet is the set of two-letter codes that have an embedded flag SVG,
// built once at startup from the embedded filesystem.
var flagCodeSet = func() map[string]bool {
	set := map[string]bool{}
	entries, err := flagFS.ReadDir("flags")
	if err != nil {
		return set
	}
	for _, e := range entries {
		if n := e.Name(); isFlagFile(n) {
			set[strings.TrimSuffix(n, ".svg")] = true
		}
	}
	return set
}()
