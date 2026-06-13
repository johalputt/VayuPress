package did_test

import (
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/did"
)

func TestDIDAuthFlow(t *testing.T) {
	auth := did.NewAuthenticator(5 * time.Minute)

	doc, priv, err := did.GenerateDIDKey()
	if err != nil {
		t.Fatal(err)
	}

	challenge, err := auth.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}

	req := &did.AuthRequest{
		DID:       doc.ID,
		Nonce:     challenge.Nonce,
		Signature: did.SignChallenge(priv, challenge.Nonce),
	}

	if err := auth.Verify(doc, req); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestExpiredChallenge(t *testing.T) {
	auth := did.NewAuthenticator(1 * time.Millisecond)
	doc, priv, _ := did.GenerateDIDKey()
	challenge, _ := auth.IssueChallenge()
	time.Sleep(10 * time.Millisecond)
	req := &did.AuthRequest{
		DID:       doc.ID,
		Nonce:     challenge.Nonce,
		Signature: did.SignChallenge(priv, challenge.Nonce),
	}
	// Nonce is consumed on first lookup regardless; expired challenge returns error
	err := auth.Verify(doc, req)
	if err == nil {
		t.Error("expected error for expired challenge")
	}
}

func TestInvalidSignature(t *testing.T) {
	auth := did.NewAuthenticator(5 * time.Minute)
	doc, _, _ := did.GenerateDIDKey()
	_, wrongPriv, _ := did.GenerateDIDKey()
	challenge, _ := auth.IssueChallenge()
	req := &did.AuthRequest{
		DID:       doc.ID,
		Nonce:     challenge.Nonce,
		Signature: did.SignChallenge(wrongPriv, challenge.Nonce),
	}
	if err := auth.Verify(doc, req); err == nil {
		t.Error("expected invalid signature error")
	}
}
