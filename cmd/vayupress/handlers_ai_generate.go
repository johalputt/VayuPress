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
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/aiassist"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/secrets"
	"github.com/johalputt/vayupress/internal/seo"
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
		// Draft shaping. All optional — an omitted field leaves the provider's own
		// default in place rather than imposing one.
		Tone     string  `json:"tone"`
		Audience string  `json:"audience"`
		Length   string  `json:"length"`
		Language string  `json:"language"`
		Shape    string  `json:"shape"`
		Temp     float64 `json:"temperature"`
		MaxWords int     `json:"max_words"`
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

	// Sampling and length. Both are clamped rather than trusted: this route spends
	// the operator's provider credit, so an absurd token cap is a cost bug.
	if body.Temp > 0 {
		backend.Temperature = clampFloat(body.Temp, 0.1, 2.0)
	}
	if body.MaxWords > 0 {
		// ~1.4 tokens per word for English prose, plus headroom for Markdown
		// scaffolding, so a "1000 words" request is not cut off at 900.
		backend.MaxTokens = clampInt(int(float64(clampInt(body.MaxWords, 100, 4000))*1.8), 256, 8000)
	}
	// Attribution, so an operator can tell this site's spend apart from everything
	// else on the same key. Never sent in Tor mode: the whole point there is that
	// no outbound request identifies the site.
	if !safefetch.ClearnetBlocked() && config.Cfg.Domain != "" {
		backend.Referer = seo.Origin(config.Cfg.Domain)
		backend.Title = config.Cfg.Domain
	}
	// Shaping instructions ride in the prompt, because they must work on every
	// OpenAI-compatible provider and none of them share a structured field for
	// "write in this tone".
	prompt := decorateDraftPrompt(body.Prompt, draftShape{
		Tone: body.Tone, Audience: body.Audience, Length: body.Length,
		Language: body.Language, Shape: body.Shape, MaxWords: body.MaxWords,
	})

	// Process-wide concurrency cap: wait for a slot, but never longer than the
	// request itself (the global request timeout cancels r.Context()).
	select {
	case aiGenSlots <- struct{}{}:
		defer func() { <-aiGenSlots }()
	case <-r.Context().Done():
		writeAPIError(w, r, http.StatusServiceUnavailable, "ai-busy", "The AI service is busy right now — please try again in a moment.", "")
		return
	}

	md, meta, err := aiassist.GenerateOpDetail(r.Context(), aiGenHTTP, backend, aiassist.OpDraft, prompt)
	if err != nil {
		// Always log the full detail server-side.
		logging.LogError("ai-generate", "provider generation failed", err.Error())
		// What the provider itself said about the request is safe to show, and is
		// the only thing that lets an operator fix their own install — "no credits
		// on this model" is actionable, "the request failed" is not. aiassist types
		// those separately from transport errors and scrubs URLs out of them;
		// anything else stays generic, because its text can name the endpoint.
		var pe *aiassist.ProviderError
		if errors.As(err, &pe) {
			writeAPIError(w, r, http.StatusBadGateway, "ai-error",
				"The model could not write this draft: "+pe.Message, "")
			return
		}
		writeAPIError(w, r, http.StatusBadGateway, "ai-error",
			"Could not reach the AI provider. Check the endpoint and key in VayuOS → API Keys.", "")
		return
	}
	blocks := blockrender.MarkdownToBlocks(md)
	if len(blocks) == 0 {
		// The model answered but nothing survived parsing. Report it as a failure
		// rather than returning an empty success the editor has to interpret.
		logging.LogError("ai-generate", "model output parsed to zero blocks", "len="+strconv.Itoa(len(md)))
		writeAPIError(w, r, http.StatusBadGateway, "ai-empty",
			"The model replied, but the reply contained no usable text. Try again, or pick a different model.", "")
		return
	}
	out := map[string]interface{}{
		"blocks":   blocks,
		"markdown": md,
	}
	// Tell the author when the draft is not the whole story.
	var notes []string
	if meta.Truncated {
		notes = append(notes, "the model hit its length limit, so the draft stops early — raise the length or continue it yourself")
	}
	if meta.FromReasoning {
		notes = append(notes, "this model returned its answer as reasoning text, so the draft may need more tidying than usual")
	}
	if meta.Model != "" && !strings.EqualFold(meta.Model, backend.Model) {
		notes = append(notes, "served by "+meta.Model)
	}
	if len(notes) > 0 {
		out["notes"] = notes
	}
	writeJSON(w, r, http.StatusOK, out)
}

// draftShape carries the author's shaping choices for a generated draft.
//
// Every field is optional and an empty one contributes nothing to the prompt.
// That matters: an unset "tone" must not silently become "professional", because
// the author would have no way to get the model's own default voice back.
type draftShape struct {
	Tone     string
	Audience string
	Length   string
	Language string
	Shape    string
	MaxWords int
}

// draftTones, draftLengths and draftShapes are the accepted vocabularies. They
// are allow-lists, not suggestions: these strings are interpolated into a prompt,
// so accepting arbitrary text here would let an author rewrite the instruction
// the operator's key is paying for.
var (
	draftTones = map[string]string{
		"neutral":        "a clear, neutral voice",
		"friendly":       "a warm, friendly voice that speaks directly to the reader",
		"professional":   "a precise, professional voice",
		"technical":      "a technical voice that assumes the reader is comfortable with detail",
		"conversational": "a relaxed, conversational voice, contractions allowed",
		"persuasive":     "a persuasive voice that argues a clear position",
	}
	draftLengths = map[string]string{
		"short":  "Keep it short — roughly 300–500 words.",
		"medium": "Aim for roughly 700–900 words.",
		"long":   "Write a thorough piece of roughly 1200–1600 words.",
	}
	draftShapes = map[string]string{
		"post":     "Write it as a complete blog post.",
		"outline":  "Return only a structured outline: headings and one-line bullets under each, no full paragraphs.",
		"howto":    "Write it as a step-by-step how-to with numbered steps the reader can follow.",
		"listicle": "Write it as a numbered list article, each item with its own heading and a short explanation.",
		"faq":      "Write it as a series of question-and-answer pairs, each question an H2.",
	}
)

// decorateDraftPrompt appends the author's shaping choices to their instruction.
//
// The choices are appended rather than prepended so the author's own words stay
// the primary instruction — a model given "audience: beginners" before the actual
// topic tends to write about the audience.
func decorateDraftPrompt(prompt string, s draftShape) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	var reqs []string
	if v, ok := draftTones[strings.ToLower(strings.TrimSpace(s.Tone))]; ok {
		reqs = append(reqs, "Write in "+v+".")
	}
	if a := sanitizeShapeText(s.Audience); a != "" {
		reqs = append(reqs, "Write for this audience: "+a+".")
	}
	if v, ok := draftShapes[strings.ToLower(strings.TrimSpace(s.Shape))]; ok {
		reqs = append(reqs, v)
	}
	// An explicit word count wins over the coarse length band, since the author
	// typed a number and the band is only a preset.
	switch {
	case s.MaxWords > 0:
		reqs = append(reqs, "Target about "+strconv.Itoa(clampInt(s.MaxWords, 100, 4000))+" words.")
	default:
		if v, ok := draftLengths[strings.ToLower(strings.TrimSpace(s.Length))]; ok {
			reqs = append(reqs, v)
		}
	}
	if l := sanitizeShapeText(s.Language); l != "" {
		reqs = append(reqs, "Write the entire post in "+l+".")
	}
	if len(reqs) > 0 {
		b.WriteString("\n\nAdditional requirements:\n")
		for _, r := range reqs {
			b.WriteString("- " + r + "\n")
		}
	}
	return b.String()
}

// sanitizeShapeText bounds and flattens a free-text shaping field (audience,
// language).
//
// This is prompt hygiene, not a security boundary: the author already controls the
// main prompt, so there is no privilege to escalate by writing instructions here.
// Collapsing newlines keeps each value inside its own bullet so the requirements
// list stays well-formed, and the length cap keeps a pasted essay from crowding
// out the instruction it is meant to qualify.
func sanitizeShapeText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	const max = 80
	if len(s) > max {
		s = s[:max]
	}
	return strings.TrimSpace(s)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
