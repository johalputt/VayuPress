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
)

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
	return []string{OpSummarize, OpImprove, OpTitles, OpSEO, OpContinue}
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

// generate calls Ollama's /api/generate with streaming disabled.
func (c *Client) generate(ctx context.Context, prompt string) (string, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"model":  c.cfg.Model,
		"prompt": prompt,
		"stream": false,
	})
	endpoint := strings.TrimRight(c.cfg.URL, "/") + "/api/generate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
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
	default:
		return "", false
	}
}
