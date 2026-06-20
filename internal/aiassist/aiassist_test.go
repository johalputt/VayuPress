package aiassist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDisabledWhenNoURL(t *testing.T) {
	c := New(Config{}, nil)
	if c.Enabled() {
		t.Fatal("expected disabled client")
	}
	if _, err := c.Assist(context.Background(), OpSummarize, "hi"); err == nil {
		t.Error("expected error when disabled")
	}
}

func TestAssistCallsOllama(t *testing.T) {
	var gotModel, gotPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model, Prompt string
			Stream        bool
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)
		gotModel, gotPrompt = req.Model, req.Prompt
		json.NewEncoder(w).Encode(map[string]string{"response": "  a summary  "})
	}))
	defer srv.Close()

	c := New(Config{URL: srv.URL, Model: "testmodel"}, srv.Client())
	got, err := c.Assist(context.Background(), OpSummarize, "Some article text")
	if err != nil {
		t.Fatal(err)
	}
	if got != "a summary" {
		t.Errorf("result = %q, want trimmed 'a summary'", got)
	}
	if gotModel != "testmodel" {
		t.Errorf("model = %q", gotModel)
	}
	if !strings.Contains(gotPrompt, "Some article text") {
		t.Errorf("prompt missing article text: %q", gotPrompt)
	}
}

func TestUnsupportedOp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"response": "x"})
	}))
	defer srv.Close()
	c := New(Config{URL: srv.URL}, srv.Client())
	if _, err := c.Assist(context.Background(), "nonsense", "text"); err == nil {
		t.Error("expected unsupported-op error")
	}
}

func TestModelError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"error": "model not found"})
	}))
	defer srv.Close()
	c := New(Config{URL: srv.URL}, srv.Client())
	if _, err := c.Assist(context.Background(), OpImprove, "text"); err == nil {
		t.Error("expected model error to propagate")
	}
}
