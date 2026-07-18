package aiassist

import (
	"context"
	"encoding/json"
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

	out, err := GenerateOp(context.Background(), srv.Client(),
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

	out, err := GenerateOp(context.Background(), srv.Client(),
		Backend{Kind: KindOllama, Endpoint: srv.URL, Model: "llama3.2"}, OpDraft, "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if out != "drafted text" {
		t.Errorf("got %q", out)
	}
}

func TestGenerateOpOpenAIRequiresModel(t *testing.T) {
	if _, err := GenerateOp(context.Background(), nil,
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
