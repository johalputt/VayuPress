package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/secrets"

	_ "github.com/mattn/go-sqlite3"
)

// newAITestSecrets builds a self-managed secrets store on an in-memory DB with
// just the tables the store needs, for the resolve/availability tests.
func newAITestSecrets(t *testing.T) *secrets.Store {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1) // :memory: is per-connection
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE service_credentials(id TEXT PRIMARY KEY,provider TEXT NOT NULL,label TEXT NOT NULL DEFAULT '',endpoint TEXT NOT NULL DEFAULT '',secret_nonce TEXT NOT NULL DEFAULT '',secret_ct TEXT NOT NULL DEFAULT '',hint TEXT NOT NULL DEFAULT '',enabled INTEGER NOT NULL DEFAULT 1,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE secret_keyring(id INTEGER PRIMARY KEY,dek TEXT NOT NULL,kek_src TEXT NOT NULL DEFAULT 'none',kek_check TEXT NOT NULL DEFAULT '',created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,rotated_at DATETIME)`); err != nil {
		t.Fatalf("keyring: %v", err)
	}
	return secrets.New(db, nil, "")
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

// TestResolveAIBackendCustomByID: a custom gateway selected by credential id
// resolves to exactly that credential's endpoint+key, never the most-recent
// custom row, and reports a helpful reason when the model or credential is
// missing.
func TestResolveAIBackendCustomByID(t *testing.T) {
	ctx := context.Background()
	store := newAITestSecrets(t)
	gw, err := store.Upsert(ctx, secrets.ProviderCustom, "LLM Gateway", "https://llm.example/v1", "sk-gateway", true, false)
	if err != nil {
		t.Fatalf("upsert gateway: %v", err)
	}
	// A more-recently-updated, unrelated custom credential (would win ProviderSecret).
	if _, err := store.Upsert(ctx, secrets.ProviderCustom, "Pushover", "https://api.pushover.net/1", "push-token", true, false); err != nil {
		t.Fatalf("upsert pushover: %v", err)
	}
	a := &App{secrets: store}

	b, ok, reason := a.resolveAIBackend(ctx, "custom:"+gw, "my-model")
	if !ok {
		t.Fatalf("expected the gateway to resolve, got reason %q", reason)
	}
	if b.Endpoint != "https://llm.example/v1" || b.APIKey != "sk-gateway" || b.Model != "my-model" {
		t.Fatalf("resolved wrong credential: %+v", b)
	}

	// No model → unavailable with a reason (never a panic).
	if _, ok, reason := a.resolveAIBackend(ctx, "custom:"+gw, ""); ok || reason == "" {
		t.Errorf("missing model should be unavailable with a reason, got ok=%v reason=%q", ok, reason)
	}
	// Unknown credential id → unavailable with a reason.
	if _, ok, reason := a.resolveAIBackend(ctx, "custom:deadbeef", "m"); ok || reason == "" {
		t.Errorf("unknown custom id should be unavailable with a reason, got ok=%v reason=%q", ok, reason)
	}
}

// TestAIAvailableProvidersCustomNeedsEndpoint: the picker only advertises a
// custom gateway once it has BOTH a key and a base URL (custom has no built-in
// default), so it never offers a backend resolveAIBackend would reject — and it
// offers one entry per custom credential, keyed by id.
func TestAIAvailableProvidersCustomNeedsEndpoint(t *testing.T) {
	ctx := context.Background()
	store := newAITestSecrets(t)
	// Custom credential with a key but NO endpoint → must not be advertised.
	if _, err := store.Upsert(ctx, secrets.ProviderCustom, "No URL", "", "sk-nourl", true, false); err != nil {
		t.Fatalf("upsert no-url: %v", err)
	}
	a := &App{secrets: store}
	for _, p := range a.aiAvailableProviders(ctx) {
		if strings.HasPrefix(p.ID, "custom:") {
			t.Fatalf("custom credential without a base URL must not be advertised, got %+v", p)
		}
	}

	// Now add an endpoint → it becomes advertised as custom:<id>, needing a model.
	gw, err := store.Upsert(ctx, secrets.ProviderCustom, "Gateway", "https://llm.example/v1", "sk-gw", true, false)
	if err != nil {
		t.Fatalf("upsert gateway: %v", err)
	}
	var found bool
	for _, p := range a.aiAvailableProviders(ctx) {
		if p.ID == "custom:"+gw {
			found = true
			if !p.NeedsModel {
				t.Error("a custom gateway must require an explicit model")
			}
		}
	}
	if !found {
		t.Error("a custom gateway with a key and base URL should be advertised as custom:<id>")
	}
}

// TestAICuratedModels: every provider with a curated fallback returns a non-empty
// list (so the picker is never empty even when the live catalogue is unreachable).
func TestAICuratedModels(t *testing.T) {
	for _, p := range []string{secrets.ProviderOpenRouter, secrets.ProviderOpenAI, secrets.ProviderOllama} {
		if len(aiCuratedModels(p)) == 0 {
			t.Errorf("aiCuratedModels(%q) should not be empty", p)
		}
	}
}

// TestAIProviderModelsFallback: an unconfigured provider still yields the curated
// list rather than nothing.
func TestAIProviderModelsFallback(t *testing.T) {
	a := &App{} // no secrets store
	got := a.aiProviderModels(context.Background(), secrets.ProviderOpenRouter)
	if len(got) == 0 {
		t.Error("unconfigured provider should fall back to the curated model list")
	}
}

// TestAIProviderModelsLive: a custom OpenAI-compatible gateway's /models endpoint
// is fetched and its ids are returned.
func TestAIProviderModelsLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"gw-large"},{"id":"gw-small"}]}`))
	}))
	defer srv.Close()

	store := newAITestSecrets(t)
	id, err := store.Upsert(context.Background(), secrets.ProviderCustom, "Gateway", srv.URL, "sk-gw", true, false)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	a := &App{secrets: store}
	got := a.aiProviderModels(context.Background(), "custom:"+id)
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "gw-large") || !strings.Contains(joined, "gw-small") {
		t.Errorf("live model list should contain the gateway's ids, got %v", got)
	}
}
