// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/ads"
	"github.com/johalputt/vayupress/internal/settings"
)

// adsTxtGoogleTail is Google's certification authority id, the fourth field
// AdSense asks for on its ads.txt line.
const adsTxtGoogleTail = ", DIRECT, f08c47fec0942fa0"

// adsTxtBody is a site's ads.txt: the line its AdSense publisher ID gives,
// then its own lines. Blank when it has neither.
func adsTxtBody(client, extra string) string {
	var lines []string
	if ads.ValidPublisherID(client) {
		lines = append(lines, "google.com, "+strings.TrimPrefix(strings.TrimSpace(client), "ca-")+adsTxtGoogleTail)
	}
	for _, l := range strings.Split(extra, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// handleAdsTxt serves /ads.txt for the site a request is for, from that
// site's own settings: one site's publisher ID never authorises another's
// inventory. A site serving an uploaded bundle that carries its own ads.txt
// is answered with that file, since its author wrote it.
func (a *App) handleAdsTxt(w http.ResponseWriter, r *http.Request) {
	if a.serveCustomIfActive(w, r, "/ads.txt") {
		return
	}
	if a.siteSettings == nil {
		http.NotFound(w, r)
		return
	}
	sc := siteSettingsScope(a.contentScope(r))
	body := adsTxtBody(a.siteSettings.Get(r.Context(), sc, settings.KeyAdsenseClient), a.siteSettings.Get(r.Context(), sc, settings.KeyAdsTxtLines))
	if body == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(body))
}
