// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/config"
)

// fakeModel is an Ollama-compatible endpoint on loopback — the path a local
// model is reached by — recording the prompt it was sent.
func fakeModel(t *testing.T, reply string) (*httptest.Server, *string) {
	t.Helper()
	var prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Prompt string `json:"prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		prompt = in.Prompt
		_ = json.NewEncoder(w).Encode(map[string]any{"response": reply, "done": true})
	}))
	t.Cleanup(srv.Close)
	return srv, &prompt
}

// Without a provider the editor offers no AI; with one, a suggestion comes
// back through the install's own provider, using the existing operations.
func TestTheEditorsAIHelpUsesTheInstallsProvider(t *testing.T) {
	a := siteApp(t)
	d := hostedSite(t, a, "harbour.example")
	h := editorRouter(a, d, true)
	if _, j := editorCall(t, h, http.MethodGet, "/sd", nil); j["ai"] != false {
		t.Errorf("with no provider the editor was told AI is available: %v", j["ai"])
	}
	if rec, j := editorCall(t, h, http.MethodPost, "/sd/assist", map[string]string{"op": "improve", "text": "x"}); rec.Code != http.StatusServiceUnavailable || errCode(j) != "ai-unavailable" {
		t.Errorf("assist with no provider: %d %s", rec.Code, rec.Body)
	}

	srv, prompt := fakeModel(t, "  Fresh fish, landed this morning.  ")
	old := config.Cfg.AIURL
	config.Cfg.AIURL = srv.URL
	t.Cleanup(func() { config.Cfg.AIURL = old })
	a.aiAssist = aiassist.New(aiassist.Config{URL: srv.URL, Model: "test"}, nil)

	if _, j := editorCall(t, h, http.MethodGet, "/sd", nil); j["ai"] != true {
		t.Errorf("with a provider the editor was not told AI is available: %v", j["ai"])
	}
	rec, j := editorCall(t, h, http.MethodPost, "/sd/assist", map[string]string{"op": "improve", "text": "fresh fish daily"})
	if rec.Code != http.StatusOK || j["result"] != "Fresh fish, landed this morning." {
		t.Fatalf("improve: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(*prompt, "without changing its meaning or adding new facts") || !strings.HasSuffix(*prompt, "fresh fish daily") {
		t.Errorf("improve did not use the assistant's no-new-facts operation: %q", *prompt)
	}
	editorCall(t, h, http.MethodPost, "/sd/assist", map[string]string{"op": "describe", "text": "Crab. Oysters."})
	if !strings.Contains(*prompt, "meta description") {
		t.Errorf("describe did not use the meta-description operation: %q", *prompt)
	}
	for _, bad := range []map[string]string{{"op": "draft", "text": "x"}, {"op": "improve", "text": "  "}} {
		if rec, _ := editorCall(t, h, http.MethodPost, "/sd/assist", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%v: %d, want 400", bad, rec.Code)
		}
	}
}
