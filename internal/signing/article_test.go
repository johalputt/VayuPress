package signing_test

import (
	"testing"

	"github.com/johalputt/vayupress/internal/signing"
)

func TestSignAndVerify(t *testing.T) {
	pub, priv, err := signing.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	_ = pub

	payload := signing.ArticlePayload{
		ID:          "art-1",
		Title:       "Hello World",
		Body:        "body text",
		AuthorID:    "user-1",
		PublishedAt: "2026-01-01T00:00:00Z",
	}
	sa, err := signing.Sign(priv, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := signing.Verify(sa); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestTamperDetection(t *testing.T) {
	_, priv, _ := signing.GenerateKeyPair()
	sa, _ := signing.Sign(priv, signing.ArticlePayload{
		ID: "x", Title: "T", Body: "B", AuthorID: "a", PublishedAt: "2026-01-01T00:00:00Z",
	})
	sa.Payload.Title = "TAMPERED"
	if err := signing.Verify(sa); err == nil {
		t.Error("expected tamper detection, got nil")
	}
}
