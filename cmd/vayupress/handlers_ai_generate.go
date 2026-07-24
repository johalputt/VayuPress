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
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/secrets"
)

// aiGenHTTP is the client used for author-triggered generation. Generation is
// slow, so the timeout is generous; endpoints are operator-configured (a stored
// provider or VAYU_AI_URL), never a per-request user URL. It routes through the
// SSRF-hardened, egress-guarded transport so a Tor Space never dials an AI
// provider from the onion server's real IP (ADR-0141) and a hostile provider
// URL cannot be steered at an internal host.
var aiGenHTTP = &http.Client{Timeout: 120 * time.Second, Transport: safeOutboundTransport()}

// Abuse controls for author-triggered generation. Any author-role console user
// can reach the generate route, and every call spends the operator's stored
// (often paid) provider key against a third-party endpoint. Without bounds a
// single compromised or careless account could run up unbounded inference cost
// and pin many concurrent outbound connections. We therefore cap both the
// per-user request rate and the process-wide concurrency.
const (
	aiGenPerUserPerMin = 10 // drafts one console user may request per minute
	aiGenMaxConcurrent = 4  // generations running at once across all users
)

var (
	// aiGenPerUser is a fixed-window per-user limiter (keyed on the console user
	// id), reusing the same limiter primitive as the analytics/contact paths.
	aiGenPerUser = newIngestLimiter(aiGenPerUserPerMin, time.Minute)
	// aiGenSlots is a counting semaphore bounding concurrent generations.
	aiGenSlots = make(chan struct{}, aiGenMaxConcurrent)
)

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
	if a.secrets == nil {
		return out
	}
	if _, endpoint := a.secrets.ProviderSecret(ctx, secrets.ProviderOllama); strings.TrimSpace(endpoint) != "" {
		add(aiProviderOption{ID: secrets.ProviderOllama, Label: "Local AI (Ollama)", DefaultModel: config.Cfg.AIModel})
	}
	// OpenAI / OpenRouter: a stored key alone is enough — each has a built-in
	// default base URL (aiDefaultEndpoint), so it will always resolve.
	for _, p := range []string{secrets.ProviderOpenRouter, secrets.ProviderOpenAI} {
		if key, _ := a.secrets.ProviderSecret(ctx, p); strings.TrimSpace(key) != "" {
			label := map[string]string{secrets.ProviderOpenRouter: "OpenRouter", secrets.ProviderOpenAI: "OpenAI"}[p]
			add(aiProviderOption{ID: p, Label: label, DefaultModel: aiDefaultModel(p), NeedsModel: aiDefaultModel(p) == ""})
		}
	}
	// Custom OpenAI-compatible gateways. The generic "custom" slot is a catch-all
	// that may also hold non-LLM secrets (e.g. a Pushover token) and has no
	// built-in default base URL. Offer one entry PER enabled custom credential
	// that actually carries both a key and a base URL, identified by its
	// credential id ("custom:<id>"), so the author reaches the exact gateway they
	// mean and generation never routes to a keyless, URL-less, or most-recent
	// unrelated credential.
	if creds, err := a.secrets.List(ctx); err == nil {
		for _, c := range creds {
			if c.Provider != secrets.ProviderCustom || !c.Enabled || !c.HasSecret {
				continue
			}
			if strings.TrimSpace(c.Endpoint) == "" {
				continue
			}
			add(aiProviderOption{ID: "custom:" + c.ID, Label: "Custom: " + c.Label, NeedsModel: true})
		}
	}
	return out
}

// resolveAIBackend turns a requested provider (+ optional model) into a concrete
// backend, or a reason it is unavailable.
func (a *App) resolveAIBackend(ctx context.Context, provider, model string) (aiassist.Backend, bool, string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)

	// A specific custom gateway selected by credential id: "custom:<id>". The
	// credential is resolved exactly (never the most-recent custom row), so with
	// several custom credentials the author reaches the one they picked.
	if strings.HasPrefix(provider, "custom:") {
		if a.secrets == nil {
			return aiassist.Backend{}, false, "The credential store is unavailable."
		}
		id := strings.TrimPrefix(provider, "custom:")
		key, endpoint, found := a.secrets.SecretByID(ctx, id)
		if !found || strings.TrimSpace(key) == "" {
			return aiassist.Backend{}, false, "That custom provider is no longer available. Pick another in the AI panel."
		}
		if strings.TrimSpace(endpoint) == "" {
			return aiassist.Backend{}, false, "This provider needs a base URL — set it on its card in API Keys."
		}
		if model == "" {
			return aiassist.Backend{}, false, "Enter a model name for this provider."
		}
		return aiassist.Backend{Kind: aiassist.KindOpenAI, Endpoint: endpoint, APIKey: key, Model: model}, true, ""
	}

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

// aiModelsHTTP lists a provider's models — a quick metadata call, so its timeout
// is much tighter than the generation client's.
var aiModelsHTTP = &http.Client{Timeout: 15 * time.Second}

// aiCuratedModels is the fallback catalogue shown when a provider's live model
// list can't be fetched (offline, older gateway, or a provider with no list API).
func aiCuratedModels(provider string) []string {
	switch {
	case provider == secrets.ProviderOpenRouter:
		return []string{"openai/gpt-4o-mini", "openai/gpt-4o", "anthropic/claude-3.5-sonnet", "google/gemini-flash-1.5", "meta-llama/llama-3.1-70b-instruct"}
	case provider == secrets.ProviderOpenAI:
		return []string{"gpt-4o-mini", "gpt-4o", "gpt-4.1-mini", "gpt-3.5-turbo"}
	case provider == secrets.ProviderOllama:
		return []string{"llama3.2", "llama3.1", "qwen2.5", "mistral", "gemma2"}
	default:
		return nil
	}
}

// aiResolveEndpoint resolves a provider (including "custom:<id>") to a concrete
// inference endpoint + key WITHOUT requiring a model — used to list models.
func (a *App) aiResolveEndpoint(ctx context.Context, provider string) (kind, endpoint, apiKey string, ok bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if strings.HasPrefix(provider, "custom:") {
		if a.secrets == nil {
			return "", "", "", false
		}
		key, ep, found := a.secrets.SecretByID(ctx, strings.TrimPrefix(provider, "custom:"))
		if !found || strings.TrimSpace(ep) == "" {
			return "", "", "", false
		}
		return aiassist.KindOpenAI, ep, key, true
	}
	if provider == "" || provider == secrets.ProviderOllama {
		if a.secrets != nil {
			if _, ep := a.secrets.ProviderSecret(ctx, secrets.ProviderOllama); strings.TrimSpace(ep) != "" {
				return aiassist.KindOllama, ep, "", true
			}
		}
		if a.aiAssist != nil && a.aiAssist.Enabled() {
			return aiassist.KindOllama, config.Cfg.AIURL, "", true
		}
		return "", "", "", false
	}
	switch provider {
	case secrets.ProviderOpenAI, secrets.ProviderOpenRouter, secrets.ProviderCustom:
		if a.secrets == nil {
			return "", "", "", false
		}
		key, ep := a.secrets.ProviderSecret(ctx, provider)
		if strings.TrimSpace(key) == "" {
			return "", "", "", false
		}
		if strings.TrimSpace(ep) == "" {
			ep = aiDefaultEndpoint(provider)
		}
		if strings.TrimSpace(ep) == "" {
			return "", "", "", false
		}
		return aiassist.KindOpenAI, ep, key, true
	}
	return "", "", "", false
}

// aiProviderModels returns the model ids the provider offers: fetched live from
// the provider's own catalogue (Ollama /api/tags, OpenAI-compatible /models),
// falling back to a curated list when the live call is unavailable.
func (a *App) aiProviderModels(ctx context.Context, provider string) []string {
	curated := aiCuratedModels(provider)
	kind, endpoint, apiKey, ok := a.aiResolveEndpoint(ctx, provider)
	if !ok {
		return curated
	}
	base := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	var reqURL string
	if kind == aiassist.KindOllama {
		reqURL = base + "/api/tags"
	} else {
		reqURL = base + "/models"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return curated
	}
	if k := strings.TrimSpace(apiKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := aiModelsHTTP.Do(req)
	if err != nil {
		return curated
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return curated
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"` // OpenAI-compatible
		Models []struct {
			Name string `json:"name"`
		} `json:"models"` // Ollama
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return curated
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, m := range payload.Data {
		add(m.ID)
	}
	for _, m := range payload.Models {
		add(m.Name)
	}
	if len(out) == 0 {
		return curated
	}
	sort.Strings(out)
	if len(out) > 300 {
		out = out[:300]
	}
	return out
}

// handleOSEditorAIModels lists the selected provider's models for the editor's
// model picker. GET ?provider=<id>, session-authenticated.
func (a *App) handleOSEditorAIModels(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	models := a.aiProviderModels(r.Context(), provider)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"models":  models,
		"default": aiDefaultModel(strings.ToLower(strings.TrimSpace(provider))),
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

	// Per-user rate limit: cap how fast one console account can spend the
	// operator's provider key. Keyed on the user id (never PII).
	rlKey := "anon"
	if u := currentUser(r); u != nil && u.ID != "" {
		rlKey = u.ID
	}
	if !aiGenPerUser.allow(rlKey) {
		writeAPIError(w, r, http.StatusTooManyRequests, "rate-limited", "You're generating drafts too quickly — wait a moment and try again.", "")
		return
	}

	backend, ok, reason := a.resolveAIBackend(r.Context(), body.Provider, body.Model)
	if !ok {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ai-unavailable", reason, "")
		return
	}

	// Process-wide concurrency cap: wait for a slot, but never longer than the
	// request itself (the global request timeout cancels r.Context()).
	select {
	case aiGenSlots <- struct{}{}:
		defer func() { <-aiGenSlots }()
	case <-r.Context().Done():
		writeAPIError(w, r, http.StatusServiceUnavailable, "ai-busy", "The AI service is busy right now — please try again in a moment.", "")
		return
	}

	md, err := aiassist.GenerateOp(r.Context(), aiGenHTTP, backend, aiassist.OpDraft, body.Prompt)
	if err != nil {
		// Never reflect the upstream/transport error to the caller: it can carry
		// the operator's configured provider endpoint (including internal hosts).
		// Log the detail server-side for operators; return a generic message.
		logging.LogError("ai-generate", "provider generation failed", err.Error())
		writeAPIError(w, r, http.StatusBadGateway, "ai-error", "The AI provider request failed. Check the provider settings in VayuOS → API Keys.", "")
		return
	}
	blocks := blockrender.MarkdownToBlocks(md)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"blocks":   blocks,
		"markdown": md,
	})
}
