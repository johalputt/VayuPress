// SPDX-License-Identifier: Apache-2.0

package main

// admin_shared.go — small admin helpers that outlived Admin v2 (removed in
// v1.6.0, ADR-0069 Stage 3). These are used by the VayuOS surfaces and the
// operator console; they were previously colocated with the v2 UI.

import (
	"net/http"

	"github.com/johalputt/vayupress/internal/mode"
)

// handleSEORegenerate rebuilds the sitemap, feed and robots artefacts on demand.
// Refused while the runtime is in a read-only or quarantined mode.
func (a *App) handleSEORegenerate(w http.ResponseWriter, r *http.Request) {
	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeJSON(w, r, http.StatusServiceUnavailable, map[string]string{"error": "cannot regenerate in " + string(cur) + " mode"})
		return
	}
	generateSitemap()
	generateRSS()
	generateRobots()
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status":      "ok",
		"regenerated": []string{"sitemap.xml", "feed.xml", "robots.txt"},
	})
}
