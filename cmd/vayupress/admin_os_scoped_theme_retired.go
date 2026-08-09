// SPDX-License-Identifier: Apache-2.0

package main

import "net/http"

// handleOSScopedThemeRetired retires /os/d/{id}/theme.
//
// The address served Theme Studio, mounted a second time, and every write it
// offered went to the operator's own install: the page's script posts to
// absolute /os/api/theme/* and /os/api/settings routes, and beneath that
// theme_tokens is CHECK(id=1) with a process-global CSS variable, so there is no
// per-site theme to write even if the routes were scoped.
//
// A REDIRECT, not a 404, for the same reason handleOSDomainManage is one: the
// URL is in operators' bookmarks and in this console's own history, and a dead
// link teaches nothing. It lands on the per-site settings page, which is the
// surface that genuinely scopes.
func (a *App) handleOSScopedThemeRetired(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		http.Redirect(w, r, "/os/domains", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/os/d/"+d.ID+"/settings", http.StatusMovedPermanently)
}
