// SPDX-License-Identifier: Apache-2.0

package main

// asset_minify.go — the console's stylesheets are served minified
// (render.MinifyCSS), memoised by content so each version is minified once.

import (
	"crypto/sha256"
	"sync"

	"github.com/johalputt/vayupress/internal/render"
)

// minifiedCSS memoises render.MinifyCSS by content, so a stylesheet is minified once
// per version and an updated file on disk is picked up by its new hash.
var minifiedCSS sync.Map // [32]byte -> []byte

func minifiedCSSFor(src []byte) []byte {
	key := sha256.Sum256(src)
	if v, ok := minifiedCSS.Load(key); ok {
		return v.([]byte)
	}
	m := render.MinifyCSS(src)
	minifiedCSS.Store(key, m)
	return m
}
