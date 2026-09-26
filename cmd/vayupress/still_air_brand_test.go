// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Still Air ships as the console's name beside VayuOS (operator, 2026-09-26).
// The name is written out here rather than read from osDesign, so emptying or
// renaming the constant fails this test instead of passing with it.
func TestVayuOSIsNamedStillAirWhereTheOperatorLooks(t *testing.T) {
	const lockup = `VayuOS <span class="sa-edition__name">Still Air</span>`

	shell := stillAirShellHead("n", "Home", "dashboard", saSession(accessAdmin))
	i := strings.Index(shell, `class="sa-rail__foot"`)
	if i < 0 {
		t.Fatal("the shell has no rail foot")
	}
	if foot := shell[i:]; !strings.Contains(foot[:strings.Index(foot, "</aside>")], lockup) {
		t.Error("the rail foot does not name the console VayuOS Still Air")
	}

	login := osLoginPage("", "", "")
	j := strings.Index(login, `class="login-footer"`)
	if j < 0 || !strings.Contains(login[j:], lockup) {
		t.Error("the sign-in page does not name the console VayuOS Still Air")
	}

	rr := httptest.NewRecorder()
	(&App{}).handleOSManifest(rr, httptest.NewRequest(http.MethodGet, "/os/manifest.webmanifest", nil))
	var m struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "VayuOS" {
		t.Errorf("manifest name = %q; the installed app's icon label must stay VayuOS", m.Name)
	}
	if !strings.HasPrefix(m.Description, "VayuOS Still Air") {
		t.Errorf("manifest description = %q; want it to open with VayuOS Still Air", m.Description)
	}
}
