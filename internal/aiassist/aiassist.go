// Package aiassist provides an opt-in, sovereign AI writing assistant for the
// VayuPress editor.
//
// Sovereignty first: the assistant talks to a LOCAL, operator-run inference
// server using the Ollama HTTP API (POST /api/generate). Nothing is sent to a
// hosted third-party model — the operator points VAYU_AI_URL at their own Ollama
// (or any Ollama-compatible) endpoint and chooses the model. When unconfigured,
// the assistant is disabled and the editor simply hides the feature.
//
// The assistant is deliberately stateless and prompt-driven: each operation is a
// small instruction wrapped around the author's text. It never auto-edits
// content — it returns suggestions the author chooses to apply (consistent with
// the project's "no autonomous actions" ethics charter).
package aiassist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Operations supported by the assistant.
const (
	OpSummarize = "summarize"
	OpImprove   = "improve"
	OpTitles    = "titles"
	OpSEO       = "seo"
	OpContinue  = "continue"
	// OpDraft writes a complete post in Markdown from a free-form instruction —
	// the "give a prompt, get a draft" editor flow.
	OpDraft = "draft"
)

// Provider kinds for a runtime-selected backend.
const (
	KindOllama = "ollama" // native Ollama /api/generate protocol
	KindOpenAI = "openai" // OpenAI-compatible /chat/completions (OpenAI, OpenRouter, …)
)

// Backend selects the inference provider for a one-off generation. It lets the
// editor pick a provider at request time — a local Ollama, or any
// OpenAI-compatible endpoint (OpenAI, OpenRouter, or a custom gateway) whose
// base URL + API key the operator stored in VayuOS.
type Backend struct {
	Kind     string // KindOllama | KindOpenAI
	Endpoint string // base URL (Ollama root, or the OpenAI-compatible base ending in /v1)
	APIKey   string // bearer token for OpenAI-compatible providers (empty for Ollama)
	Model    string // model name
}

// Config configures the local inference endpoint.
type Config struct {
	URL   string // base URL of the Ollama server, e.g. http://localhost:11434
	Model string // model name, e.g. "llama3.2"
}

// Client calls a local Ollama-compatible inference server.
type Client struct {
	cfg     Config
	http    *http.Client
	enabled bool
}

// New builds a Client. A blank URL yields a disabled client.
func New(cfg Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	if cfg.Model == "" {
		cfg.Model = "llama3.2"
	}
	return &Client{cfg: cfg, http: httpClient, enabled: strings.TrimSpace(cfg.URL) != ""}
}

// Enabled reports whether a local model endpoint is configured.
func (c *Client) Enabled() bool { return c.enabled }

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

// SupportedOps lists the operation identifiers the assistant accepts.
func SupportedOps() []string {
	return []string{OpSummarize, OpImprove, OpTitles, OpSEO, OpContinue, OpDraft}
}

// GenerateOp runs op over text against an explicit, runtime-selected backend
// (Ollama, OpenAI, OpenRouter, or any OpenAI-compatible gateway). It is the
// stateless path the editor's provider picker uses; the env-configured Client
// above remains for the default local-Ollama assist. text is capped to bound the
// prompt. A nil httpClient gets a 90s default.
func GenerateOp(ctx context.Context, hc *http.Client, b Backend, op, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if len(text) > 12000 {
		text = text[:12000]
	}
	prompt, ok := buildPrompt(op, text)
	if !ok {
		return "", fmt.Errorf("unsupported operation %q", op)
	}
	if hc == nil {
		hc = &http.Client{Timeout: 90 * time.Second}
	}
	switch strings.ToLower(strings.TrimSpace(b.Kind)) {
	case KindOpenAI:
		return generateOpenAI(ctx, hc, b, prompt)
	default:
		return generateOllamaAt(ctx, hc, b.Endpoint, b.Model, prompt)
	}
}

// Assist runs op over text and returns the model's suggestion. text is capped to
// keep prompts bounded. Returns an error when disabled or on transport failure.
func (c *Client) Assist(ctx context.Context, op, text string) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("AI assistant is not configured (set VAYU_AI_URL)")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("text is required")
	}
	if len(text) > 12000 {
		text = text[:12000]
	}
	prompt, ok := buildPrompt(op, text)
	if !ok {
		return "", fmt.Errorf("unsupported operation %q", op)
	}
	return c.generate(ctx, prompt)
}

// generate calls the env-configured local Ollama for the default assist Client.
func (c *Client) generate(ctx context.Context, prompt string) (string, error) {
	return generateOllamaAt(ctx, c.http, c.cfg.URL, c.cfg.Model, prompt)
}

// generateOllamaAt calls an Ollama /api/generate endpoint with streaming off.
func generateOllamaAt(ctx context.Context, hc *http.Client, base, model, prompt string) (string, error) {
	if strings.TrimSpace(model) == "" {
		model = "llama3.2"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":  model,
		"prompt": prompt,
		"stream": false,
	})
	endpoint := strings.TrimRight(base, "/") + "/api/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ai endpoint status %d", resp.StatusCode)
	}
	var out struct {
		Response string `json:"response"`
		Error    string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ai decode: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ai model error: %s", out.Error)
	}
	return strings.TrimSpace(out.Response), nil
}

// generateOpenAI calls an OpenAI-compatible /chat/completions endpoint (OpenAI,
// OpenRouter, or any compatible gateway) with streaming disabled.
func generateOpenAI(ctx context.Context, hc *http.Client, b Backend, prompt string) (string, error) {
	if strings.TrimSpace(b.Model) == "" {
		return "", fmt.Errorf("a model name is required for this provider")
	}
	body, _ := json.Marshal(map[string]interface{}{
		"model":    b.Model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
	})
	endpoint := strings.TrimRight(strings.TrimSpace(b.Endpoint), "/")
	if endpoint == "" {
		return "", fmt.Errorf("this provider has no endpoint configured")
	}
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if k := strings.TrimSpace(b.APIKey); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("ai request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ai endpoint status %d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("ai decode: %w", err)
	}
	if out.Error.Message != "" {
		return "", fmt.Errorf("ai model error: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("ai returned no choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// buildPrompt returns the instruction prompt for op, or ok=false if unknown.
func buildPrompt(op, text string) (string, bool) {
	switch op {
	case OpSummarize:
		return "Summarize the following article in 2-3 concise sentences suitable " +
			"for a meta description. Return only the summary.\n\n" + text, true
	case OpImprove:
		return "Improve the clarity, grammar, and flow of the following text " +
			"without changing its meaning or adding new facts. Return only the " +
			"revised text.\n\n" + text, true
	case OpTitles:
		return "Suggest 5 concise, compelling title options for the following " +
			"article. Return them as a numbered list, nothing else.\n\n" + text, true
	case OpSEO:
		return "Act as an SEO editor. For the following article, suggest: a meta " +
			"title (<=60 chars), a meta description (<=155 chars), and 5 focus " +
			"keywords. Return as a short labelled list.\n\n" + text, true
	case OpContinue:
		return "Continue writing the following article in the same voice and " +
			"style. Add one or two coherent paragraphs. Return only the new text.\n\n" + text, true
	case OpDraft:
		return "Write a complete, well-structured blog post in GitHub-Flavored " +
			"Markdown based on the instruction below. Begin with a single H1 title " +
			"(# Title). Use ## / ### subheadings, short paragraphs, and bullet or " +
			"numbered lists where they help. Do not include front-matter, code " +
			"fences around the whole post, or any commentary — return only the " +
			"Markdown post.\n\nInstruction: " + text, true
	default:
		return "", false
	}
}
