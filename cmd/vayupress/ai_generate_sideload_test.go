package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/secrets"
)

func TestIsLocalOrInlineImage(t *testing.T) {
	cases := map[string]bool{
		"":                                      true,
		"/media/abc.png":                        true,
		"/some/path.jpg":                        true,
		"data:image/png;base64,AAAA":            true,
		"https://cdn.example.com/x/media/y.png": true, // already re-hosted
		"relative/thing.png":                    true, // not http(s) → not sideloaded
		"https://pixabay.com/a.jpg":             false,
		"http://unsplash.example/b.png":         false,
	}
	for in, want := range cases {
		if got := isLocalOrInlineImage(in); got != want {
			t.Errorf("isLocalOrInlineImage(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestSideloadBlocksImagesNoop confirms that a document with only local image
// URLs is returned unchanged (and that unknown block fields survive the JSON
// round-trip, since sideload operates on raw JSON).
func TestSideloadBlocksImagesNoop(t *testing.T) {
	in := `[{"type":"image","url":"/media/a.png","alt":"x","width":"wide","customField":42},{"type":"paragraph","text":"hi"}]`
	out, changed := sideloadBlocksImages(context.Background(), in)
	if changed {
		t.Errorf("expected no change for local-only images, got changed=true")
	}
	// The original is returned verbatim when nothing changed.
	if out != in {
		t.Errorf("unchanged input should be returned as-is:\n in=%s\nout=%s", in, out)
	}
}

func TestSideloadBlocksImagesBadJSON(t *testing.T) {
	if out, changed := sideloadBlocksImages(context.Background(), "not json"); changed || out != "not json" {
		t.Errorf("bad JSON must be returned unchanged, got changed=%v out=%q", changed, out)
	}
}

// TestResolveAIBackendUnconfigured: with no secrets store and no env Ollama, any
// provider request reports unavailable with a helpful reason (never a panic).
func TestResolveAIBackendUnconfigured(t *testing.T) {
	a := &App{} // zero value: nil secrets, nil aiAssist
	for _, provider := range []string{"", secrets.ProviderOllama, secrets.ProviderOpenAI, secrets.ProviderOpenRouter} {
		if _, ok, reason := a.resolveAIBackend(context.Background(), provider, ""); ok || reason == "" {
			t.Errorf("provider %q: expected unavailable with a reason, got ok=%v reason=%q", provider, ok, reason)
		}
	}
}

func TestAIDefaults(t *testing.T) {
	if aiDefaultModel(secrets.ProviderOpenAI) == "" || aiDefaultModel(secrets.ProviderOpenRouter) == "" {
		t.Error("OpenAI/OpenRouter should have a default model")
	}
	if !strings.Contains(aiDefaultEndpoint(secrets.ProviderOpenAI), "openai.com") {
		t.Errorf("OpenAI default endpoint wrong: %q", aiDefaultEndpoint(secrets.ProviderOpenAI))
	}
	if aiDefaultModel(secrets.ProviderCustom) != "" {
		t.Error("custom provider should require an explicit model")
	}
}
