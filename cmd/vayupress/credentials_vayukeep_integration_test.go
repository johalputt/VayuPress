// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/auth"
)

// The backup passphrase is the Backups page's: the generic credential form
// refuses it rather than save a second one the engine would read instead.
func TestTheCredentialFormLeavesTheBackupPassphraseAlone(t *testing.T) {
	srv, key := newTestHarness(t)
	csrf := auth.GenerateCSRFToken("")
	for _, provider := range []string{"vayukeep", " VayuKeep "} {
		payload, _ := json.Marshal(map[string]any{"provider": provider, "label": "Other", "secret": "a-new-passphrase-1", "enabled": true})
		req, _ := http.NewRequest("POST", srv.URL+"/os/api/credentials/save", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		req.Header.Set("X-CSRF-Token", csrf)
		req.AddCookie(&http.Cookie{Name: "vp_csrf", Value: csrf})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "set on the Backups page") {
			t.Errorf("provider %q: %d %s, want it refused and sent to the Backups page", provider, resp.StatusCode, body)
		}
	}
}
