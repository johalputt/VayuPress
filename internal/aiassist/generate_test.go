package aiassist

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateOpOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k123" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-4o-mini" {
			t.Errorf("model = %v", body["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"# Hi\n\nHello."}}]}`))
	}))
	defer srv.Close()

	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL + "/v1", APIKey: "k123", Model: "gpt-4o-mini"},
		OpDraft, "write about x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Hello") {
		t.Errorf("got %q", out)
	}
}

func TestGenerateOpOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/generate") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"response":"drafted text"}`))
	}))
	defer srv.Close()

	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOllama, Endpoint: srv.URL, Model: "llama3.2"}, OpDraft, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if out != "drafted text" {
		t.Errorf("got %q", out)
	}
}

func TestGenerateOpOpenAIRequiresModel(t *testing.T) {
	if _, _, err := GenerateOpDetail(context.Background(), nil,
		Backend{Kind: KindOpenAI, Endpoint: "http://x/v1"}, OpDraft, "p"); err == nil {
		t.Error("expected an error when no model is set for an OpenAI-compatible backend")
	}
}

func TestDraftOpPromptExists(t *testing.T) {
	p, ok := buildPrompt(OpDraft, "a topic")
	if !ok || !strings.Contains(p, "Markdown") || !strings.Contains(p, "a topic") {
		t.Errorf("draft prompt not built correctly: ok=%v p=%q", ok, p)
	}
	found := false
	for _, op := range SupportedOps() {
		if op == OpDraft {
			found = true
		}
	}
	if !found {
		t.Error("OpDraft missing from SupportedOps")
	}
}

// TestGenerateOpSurfacesProviderError pins that a provider's own explanation
// reaches the caller. OpenRouter answers a rejected request with a JSON body
// explaining exactly why — no credits, model not available to this account,
// rate limited — and discarding it leaves an operator with "it failed" and no
// way to fix their own install.
func TestGenerateOpSurfacesProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		_, _ = w.Write([]byte(`{"error":{"message":"This model requires credits","code":402}}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "write something")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "This model requires credits") {
		t.Errorf("the provider's own reason must reach the caller, got: %v", err)
	}
	if !strings.Contains(err.Error(), "402") {
		t.Errorf("the status code must be reported, got: %v", err)
	}
}

// TestGenerateOpEmptyContentIsAnError pins the silent-empty-draft bug. Reasoning
// models on OpenRouter routinely return an empty "content" with the prose in
// "reasoning"; returning "" with a nil error made that indistinguishable from
// success, so the editor inserted nothing and said the model "returned nothing"
// while the server logged no failure at all.
func TestGenerateOpEmptyContentIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"   "}}]}`))
	}))
	defer srv.Close()
	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "write something")
	if err == nil {
		t.Fatalf("empty content must be an error, got output %q and nil error", out)
	}
}

// TestGenerateOpFallsBackToReasoning: when a reasoning model puts its answer in
// "reasoning" and leaves "content" empty, use it rather than failing — the draft
// is there, just under a different key.
func TestGenerateOpFallsBackToReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","reasoning":"# Title\n\nBody."}}]}`))
	}))
	defer srv.Close()
	out, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "write something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "# Title") {
		t.Errorf("reasoning content should be used as the draft, got %q", out)
	}
}

// TestGenerateOpErrorBodyOn200 covers the other OpenRouter shape: HTTP 200 with
// an error object and no choices.
func TestGenerateOpErrorBodyOn200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited upstream"}}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil || !strings.Contains(err.Error(), "rate limited upstream") {
		t.Errorf("a 200-with-error body must surface the reason, got: %v", err)
	}
}

// TestProviderErrorScrubsEndpoints: a provider message is shown to the author, so
// it must not carry the operator's gateway address. A custom gateway that echoes
// its own URL in an error would otherwise disclose an internal host.
func TestProviderErrorScrubsEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"upstream http://10.1.2.3:8080/v1/chat failed for internal-gw.corp:9000"}}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("a provider-reported failure must be a *ProviderError, got %T", err)
	}
	for _, leak := range []string{"10.1.2.3", "http://", "internal-gw.corp:9000", "8080"} {
		if strings.Contains(pe.Message, leak) {
			t.Errorf("scrubbed message still contains %q: %s", leak, pe.Message)
		}
	}
	// The useful part must survive.
	if !strings.Contains(pe.Message, "upstream") || !strings.Contains(pe.Message, "failed") {
		t.Errorf("scrubbing removed the actionable text: %s", pe.Message)
	}
	if pe.Status != http.StatusBadGateway {
		t.Errorf("status should be preserved, got %d", pe.Status)
	}
}

// TestTransportErrorIsNotAProviderError: a connection failure's text contains the
// endpoint URL, so it must NOT be typed as safe-to-display.
func TestTransportErrorIsNotAProviderError(t *testing.T) {
	// Port 1 on loopback refuses connections.
	_, _, err := GenerateOpDetail(context.Background(), &http.Client{},
		Backend{Kind: KindOpenAI, Endpoint: "http://127.0.0.1:1/v1", APIKey: "k", Model: "m"}, OpDraft, "x")
	if err == nil {
		t.Fatal("expected an error")
	}
	var pe *ProviderError
	if errors.As(err, &pe) {
		t.Errorf("a transport error must not be typed as a displayable ProviderError: %v", err)
	}
}

// TestTruncationIsReported: a draft cut off at the token cap is still returned,
// but the caller must be able to tell the author it is incomplete.
func TestTruncationIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"served/model","choices":[{"finish_reason":"length","message":{"content":"# Half a post"}}]}`))
	}))
	defer srv.Close()
	out, meta, err := GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if err != nil {
		t.Fatalf("a truncated draft is still a draft: %v", err)
	}
	if out == "" {
		t.Error("the partial content should be returned")
	}
	if !meta.Truncated {
		t.Error("truncation must be reported")
	}
	if meta.Model != "served/model" {
		t.Errorf("the served model should be reported, got %q", meta.Model)
	}
}

// TestOptionsAreSent: temperature and max_tokens must reach the provider, or the
// new controls in the editor panel would be decorative.
func TestOptionsAreSent(t *testing.T) {
	var got map[string]interface{}
	var sawReferer, sawTitle bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		if r.Header.Get("HTTP-Referer") != "" {
			sawReferer = true
		}
		if r.Header.Get("X-Title") != "" {
			sawTitle = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	_, _, err := GenerateOpDetail(context.Background(), srv.Client(), Backend{
		Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m",
		Temperature: 0.7, MaxTokens: 1234, Referer: "https://example.org", Title: "VayuPress",
	}, OpDraft, "x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["temperature"] != 0.7 {
		t.Errorf("temperature not sent: %v", got["temperature"])
	}
	if got["max_tokens"] != float64(1234) {
		t.Errorf("max_tokens not sent: %v", got["max_tokens"])
	}
	if !sawReferer || !sawTitle {
		t.Error("attribution headers should be sent when supplied")
	}
	// Zero values must be omitted so the provider's own defaults apply.
	got = nil
	_, _, _ = GenerateOpDetail(context.Background(), srv.Client(),
		Backend{Kind: KindOpenAI, Endpoint: srv.URL, APIKey: "k", Model: "m"}, OpDraft, "x")
	if _, ok := got["temperature"]; ok {
		t.Error("an unset temperature must not be sent")
	}
	if _, ok := got["max_tokens"]; ok {
		t.Error("an unset max_tokens must not be sent")
	}
}
