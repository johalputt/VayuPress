// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// TestAPasswordResetDisconnectsEveryAITool — a tool connected to the
// mailbox (as an attacker who was in it would connect one) loses its way
// in when the holder resets the password: the connection is gone, its key
// is revoked, and the notice counts it among what was disconnected.
func TestAPasswordResetDisconnectsEveryAITool(t *testing.T) {
	a, client := mailboxOAuthApp(t)
	verifier := "a-verifier-long-enough-for-pkce-0123456789abcdef"
	sum := sha256.Sum256([]byte(verifier))
	rec := consentAs(a, client, "vmail:dana@example.com", "mailbox", base64.RawURLEncoding.EncodeToString(sum[:]))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if tok := oauthExchange(a, client, loc.Query().Get("code"), verifier); tok.Code != 200 {
		t.Fatalf("connecting: %d %s", tok.Code, tok.Body.String())
	}
	cs, _ := a.vayuMail.Connections(vmail.ReadAsOwner("dana"))
	if len(cs) != 1 {
		t.Fatalf("%d connections before the reset", len(cs))
	}
	deps, ok := a.mailResetDepsFor()
	if !ok {
		t.Fatal("no reset pipeline")
	}
	var notice string
	deps.FileToMailbox = func(_ context.Context, _, _, body string) error { notice = body; return nil }
	out, err := applyMailPasswordReset(context.Background(), deps, "dana@example.com", "a-new-password-entirely", mailResetByCode, "dana@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if out.ToolsDisconnected != 1 || len(out.Problems) != 0 {
		t.Errorf("the outcome: %+v", out)
	}
	if _, ok := a.vayuMail.ConnectionOf(cs[0].KeyID); ok {
		t.Error("the tool's connection outlived the reset")
	}
	keys, _ := a.apiKeys.ListByOwner(context.Background(), "vmail:dana@example.com")
	if len(keys) != 1 || !keys[0].Revoked {
		t.Errorf("the tool's key outlived the reset: %+v", keys)
	}
	if !strings.Contains(notice, "1 AI tool disconnected") {
		t.Errorf("the notice does not say the tool was disconnected:\n%s", notice)
	}
}
