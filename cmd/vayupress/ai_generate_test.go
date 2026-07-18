package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/secrets"
)

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
