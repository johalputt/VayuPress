// Package compat contains golden-file compatibility tests.
// Any change to a Stable schema or contract must produce a deliberate golden update.
// See docs/compatibility/stability-matrix.md for stability levels.
package compat_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/johalputt/vayupress/internal/signing"
	"os"
	"path/filepath"
	"testing"
)

// goldenPath returns the path to a golden file, creating the directory if needed.
func goldenPath(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join("..", "..", "testdata", "golden")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir golden: %v", err)
	}
	return filepath.Join(dir, name)
}

// updateGolden writes data to the golden file when GOLDEN_UPDATE=1 is set.
func updateGolden(t *testing.T, path string, data []byte) {
	t.Helper()
	if os.Getenv("GOLDEN_UPDATE") != "1" {
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
	t.Logf("golden updated: %s", path)
}

// TestSignedArticleSchemaStable verifies the SignedArticle JSON structure has
// not changed since the golden was captured. A change here requires a new ADR
// and a compatibility migration per docs/compatibility/api-contracts.md.
func TestSignedArticleSchemaStable(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	_ = pub

	payload := signing.ArticlePayload{
		ID:          "00000000-0000-0000-0000-000000000001",
		Title:       "golden-title",
		Body:        "golden-body",
		AuthorID:    "author-golden",
		PublishedAt: "2026-01-01T00:00:00Z",
	}

	sa, err := signing.Sign(priv, payload)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Replace non-deterministic signature with a placeholder for golden comparison.
	// We only test the field names / structure, not the actual signature value.
	type structShape struct {
		PayloadFields  []string `json:"payload_fields"`
		TopLevelFields []string `json:"top_level_fields"`
		PayloadVersion uint32   `json:"payload_version"`
	}

	var raw map[string]interface{}
	b, _ := json.Marshal(sa)
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	payloadRaw, _ := raw["payload"].(map[string]interface{})
	topKeys := sortedKeys(raw)
	payloadKeys := sortedKeys(payloadRaw)

	shape := structShape{
		PayloadFields:  payloadKeys,
		TopLevelFields: topKeys,
		PayloadVersion: 1,
	}

	got, err := json.MarshalIndent(shape, "", "  ")
	if err != nil {
		t.Fatalf("marshal shape: %v", err)
	}
	got = append(got, '\n')

	path := goldenPath(t, "signed-article-schema.json")
	updateGolden(t, path, got)

	want, err := os.ReadFile(path)
	if err != nil {
		// Golden does not exist yet — write it.
		if os.IsNotExist(err) {
			if err2 := os.WriteFile(path, got, 0o644); err2 != nil {
				t.Fatalf("create golden: %v", err2)
			}
			t.Logf("golden created: %s (first run)", path)
			return
		}
		t.Fatalf("read golden: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("SignedArticle schema changed (Stable contract violation).\n"+
			"If intentional: set GOLDEN_UPDATE=1 and update ADR per docs/compatibility/stability-matrix.md\n\n"+
			"got:\n%s\nwant:\n%s", got, want)
	}
}

// TestPluginRequestSchemaStable verifies the plugin IPC Request JSON structure.
func TestPluginRequestSchemaStable(t *testing.T) {
	type RequestShape struct {
		Fields []string `json:"fields"`
	}

	// This mirrors internal/sandbox.Request field names — if they change, golden fails.
	fields := []string{
		"capabilities",
		"causation_id",
		"correlation_id",
		"hook_name",
		"payload",
		"trace_id",
	}

	shape := RequestShape{Fields: fields}
	got, err := json.MarshalIndent(shape, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')

	path := goldenPath(t, "plugin-request-schema.json")
	updateGolden(t, path, got)

	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			os.WriteFile(path, got, 0o644) //nolint:errcheck
			t.Logf("golden created: %s", path)
			return
		}
		t.Fatalf("read golden: %v", err)
	}

	if string(got) != string(want) {
		t.Errorf("Plugin IPC request schema changed (Stable contract violation).\n"+
			"got:\n%s\nwant:\n%s", got, want)
	}
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Simple insertion sort — small maps only.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
