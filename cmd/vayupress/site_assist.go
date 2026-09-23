// SPDX-License-Identifier: Apache-2.0

package main

// site_assist.go — AI help in the site editor, through the install's own AI
// provider (resolveAIBackend: a local Ollama, or an OpenAI-compatible key the
// operator stored). It only suggests: the editor shows the text beside the
// field and the operator chooses to use it, as the post editor's assistant
// does. No provider configured, no button.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/aiassist"
)

// siteAssistOps maps what the editor asks for to the assistant's existing
// operations, rather than writing new prompts: "improve" is clarity and flow
// without new facts; "describe" is the meta-description summary.
var siteAssistOps = map[string]string{
	"improve":  aiassist.OpImprove,
	"describe": aiassist.OpSummarize,
}

// siteAssistTimeout bounds a suggestion someone is waiting on in the editor.
const siteAssistTimeout = 60 * time.Second

// siteAssistAvailable reports whether the editor should offer AI help.
func (a *App) siteAssistAvailable(ctx context.Context) bool {
	_, ok, _ := a.resolveAIBackend(ctx, "", "")
	return ok
}

// handleSiteDocAssist answers POST …/assist {op, text} with a suggestion.
func (a *App) handleSiteDocAssist(target siteDocTarget) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := a.siteDocTargetOr404(w, r, target); !ok {
			return
		}
		var body struct {
			Op   string `json:"op"`
			Text string `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
			return
		}
		op, ok := siteAssistOps[body.Op]
		if !ok {
			writeAPIError(w, r, http.StatusBadRequest, "unknown-op", "op must be improve or describe", "")
			return
		}
		if strings.TrimSpace(body.Text) == "" {
			writeAPIError(w, r, http.StatusBadRequest, "no-text", "There is no text to work from yet.", "")
			return
		}
		backend, ok, why := a.resolveAIBackend(r.Context(), "", "")
		if !ok {
			writeAPIError(w, r, http.StatusServiceUnavailable, "ai-unavailable", why, "")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), siteAssistTimeout)
		defer cancel()
		out, _, err := aiassist.GenerateOpDetail(ctx, aiGenHTTP, backend, op, body.Text)
		if err != nil {
			writeAPIError(w, r, http.StatusBadGateway, "ai-error", aiFailureMessage(err, ctx.Err() == context.DeadlineExceeded), "")
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]string{"result": strings.TrimSpace(out)})
	}
}
