package main

// handlers_ai_generate.go — the editor's "write a post from a prompt" feature.
//
// Unlike the env-only local-Ollama assist (handlers_ai.go), this resolves an
// inference backend at request time from the provider the author picks: a local
// Ollama, or any OpenAI-compatible provider (OpenAI, OpenRouter, or a custom
// gateway) whose base URL + API key the operator stored — encrypted at rest — in
// VayuOS → API Keys. The generated Markdown is converted to real editor blocks
// server-side (through the same sanitized import path as everything else), so the
// author gets an editable draft, never raw model output injected as HTML.
//
// Ethics: the model only ever produces a *suggestion the author inserts*; it is
// never auto-saved or published (consistent with the "no autonomous actions"
// charter).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/secrets"
)

// aiGenHTTP is the client used for author-triggered generation. Generation is
// slow, so the timeout is generous; endpoints are operator-configured (a stored
// provider or VAYU_AI_URL), never a per-request user URL.
var aiGenHTTP = &http.Client{Timeout: 120 * time.Second}

// aiProviderOption is one selectable provider for the editor's AI panel.
type aiProviderOption struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	DefaultModel string `json:"defaultModel"`
	NeedsModel   bool   `json:"needsModel"`
}

// aiDefaultEndpoint returns the fallback base URL for an OpenAI-compatible
// provider when the operator left the endpoint blank.
func aiDefaultEndpoint(provider string) string {
	switch provider {
	case secrets.ProviderOpenAI:
		return "https://api.openai.com/v1"
	case secrets.ProviderOpenRouter:
		return "https://openrouter.ai/api/v1"
	default:
		return ""
	}
}

// aiDefaultModel returns a sensible default model per provider (overridable by
// the author). Empty means the author must supply a model.
func aiDefaultModel(provider string) string {
	switch provider {
	case secrets.ProviderOpenAI:
		return "gpt-4o-mini"
	case secrets.ProviderOpenRouter:
		return "openai/gpt-4o-mini"
	default:
		return ""
	}
}

// aiAvailableProviders lists the providers the operator has actually configured,
// so the editor only offers backends that will work.
func (a *App) aiAvailableProviders(ctx context.Context) []aiProviderOption {
	var out []aiProviderOption
	seen := map[string]bool{}
	add := func(o aiProviderOption) {
		if o.ID == "" || seen[o.ID] {
			return
		}
		seen[o.ID] = true
		out = append(out, o)
	}
	// Local Ollama: env-configured client, or a stored ollama endpoint.
	if a.aiAssist != nil && a.aiAssist.Enabled() {
		add(aiProviderOption{ID: secrets.ProviderOllama, Label: "Local AI (Ollama)", DefaultModel: a.aiAssist.Model()})
	}
	if a.secrets != nil {
		if _, endpoint := a.secrets.ProviderSecret(ctx, secrets.ProviderOllama); strings.TrimSpace(endpoint) != "" {
			add(aiProviderOption{ID: secrets.ProviderOllama, Label: "Local AI (Ollama)", DefaultModel: config.Cfg.AIModel})
		}
		for _, p := range []string{secrets.ProviderOpenRouter, secrets.ProviderOpenAI, secrets.ProviderCustom} {
			if key, _ := a.secrets.ProviderSecret(ctx, p); strings.TrimSpace(key) != "" {
				label := map[string]string{secrets.ProviderOpenRouter: "OpenRouter", secrets.ProviderOpenAI: "OpenAI", secrets.ProviderCustom: "Custom (OpenAI-compatible)"}[p]
				add(aiProviderOption{ID: p, Label: label, DefaultModel: aiDefaultModel(p), NeedsModel: aiDefaultModel(p) == ""})
			}
		}
	}
	return out
}

// resolveAIBackend turns a requested provider (+ optional model) into a concrete
// backend, or a reason it is unavailable.
func (a *App) resolveAIBackend(ctx context.Context, provider, model string) (aiassist.Backend, bool, string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)

	// Default / Ollama.
	if provider == "" || provider == secrets.ProviderOllama {
		if a.secrets != nil {
			if _, endpoint := a.secrets.ProviderSecret(ctx, secrets.ProviderOllama); strings.TrimSpace(endpoint) != "" {
				m := model
				if m == "" {
					m = config.Cfg.AIModel
				}
				return aiassist.Backend{Kind: aiassist.KindOllama, Endpoint: endpoint, Model: m}, true, ""
			}
		}
		if a.aiAssist != nil && a.aiAssist.Enabled() {
			m := model
			if m == "" {
				m = a.aiAssist.Model()
			}
			return aiassist.Backend{Kind: aiassist.KindOllama, Endpoint: config.Cfg.AIURL, Model: m}, true, ""
		}
		return aiassist.Backend{}, false, "No AI provider is configured. Add a local Ollama endpoint, or an OpenAI / OpenRouter key, in VayuOS → API Keys."
	}

	// OpenAI-compatible providers (OpenAI, OpenRouter, custom).
	switch provider {
	case secrets.ProviderOpenAI, secrets.ProviderOpenRouter, secrets.ProviderCustom:
		if a.secrets == nil {
			return aiassist.Backend{}, false, "The credential store is unavailable."
		}
		key, endpoint := a.secrets.ProviderSecret(ctx, provider)
		if strings.TrimSpace(key) == "" {
			return aiassist.Backend{}, false, "Add and enable an API key for this provider in VayuOS → API Keys first."
		}
		if strings.TrimSpace(endpoint) == "" {
			endpoint = aiDefaultEndpoint(provider)
		}
		if strings.TrimSpace(endpoint) == "" {
			return aiassist.Backend{}, false, "This provider needs a base URL — set it on its card in API Keys."
		}
		m := model
		if m == "" {
			m = aiDefaultModel(provider)
		}
		if m == "" {
			return aiassist.Backend{}, false, "Enter a model name for this provider."
		}
		return aiassist.Backend{Kind: aiassist.KindOpenAI, Endpoint: endpoint, APIKey: key, Model: m}, true, ""
	default:
		return aiassist.Backend{}, false, "Unknown provider."
	}
}

// handleOSEditorAIProviders reports which AI providers are usable, for the
// editor's provider picker. GET, session-authenticated.
func (a *App) handleOSEditorAIProviders(w http.ResponseWriter, r *http.Request) {
	providers := a.aiAvailableProviders(r.Context())
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"enabled":   len(providers) > 0,
		"providers": providers,
	})
}

// handleOSEditorGenerate writes a draft from a free-form prompt using the chosen
// provider and returns editor blocks. POST {prompt, provider, model}. CSRF is
// enforced by the route middleware; session-authenticated.
func (a *App) handleOSEditorGenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt   string `json:"prompt"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "no-prompt", "A prompt is required.", "")
		return
	}
	backend, ok, reason := a.resolveAIBackend(r.Context(), body.Provider, body.Model)
	if !ok {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ai-unavailable", reason, "")
		return
	}
	md, err := aiassist.GenerateOp(r.Context(), aiGenHTTP, backend, aiassist.OpDraft, body.Prompt)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "ai-error", err.Error(), "")
		return
	}
	blocks := blockrender.MarkdownToBlocks(md)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"blocks":   blocks,
		"markdown": md,
	})
}
