// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	tok := oauthExchange(a, client, loc.Query().Get("code"), verifier)
	var granted struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if tok.Code != 200 || json.Unmarshal(tok.Body.Bytes(), &granted) != nil || granted.Refresh == "" || !a.apiKeys.Verify(granted.Access) {
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
	// What the tool holds is worth nothing now: its token is refused, and
	// renewing it, which keeps a key's id, does not bring the key back.
	if a.apiKeys.Verify(granted.Access) {
		t.Error("the tool's token still works after the reset")
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {granted.Refresh}, "client_id": {client.ID}}
	r := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	renewed := httptest.NewRecorder()
	a.handleOAuthToken(renewed, r)
	if renewed.Code != http.StatusBadRequest || !strings.Contains(renewed.Body.String(), "invalid_grant") {
		t.Errorf("the tool renewed its way back in: %d %s", renewed.Code, renewed.Body.String())
	}
}

// TestAPasswordResetNamesWhatKeepsLeavingTheMailbox — forwarding, a rule
// that forwards, a file link and a share with another mailbox are left as
// they were (each may be the holder's own) and named in the notice, for
// the holder to remove what they did not set up; a mailbox with none of
// them is told nothing of the kind.
func TestAPasswordResetNamesWhatKeepsLeavingTheMailbox(t *testing.T) {
	a := calendarApp(t)
	eli(t, a)
	ctx := context.Background()
	reset := func() string {
		t.Helper()
		deps, ok := a.mailResetDepsFor()
		if !ok {
			t.Fatal("no reset pipeline")
		}
		var notice string
		deps.FileToMailbox = func(_ context.Context, _, _, body string) error { notice = body; return nil }
		if _, err := applyMailPasswordReset(ctx, deps, "dana@example.com", "a-new-password-entirely", mailResetByCode, "dana@example.com"); err != nil {
			t.Fatal(err)
		}
		return notice
	}
	if quiet := reset(); strings.Contains(quiet, "left as they were") {
		t.Errorf("a mailbox with nothing leaving it was told of something:\n%s", quiet)
	}
	accts := a.vayuMail.Accounts()
	if err := accts.SetForward(ctx, "dana@example.com", "copy@elsewhere.example"); err != nil {
		t.Fatal(err)
	}
	if err := accts.CreateFilter(ctx, vmail.FilterRule{Mailbox: "dana@example.com", Conditions: []vmail.RuleCondition{{Field: "subject", Op: "contains", Value: "invoice"}},
		Actions: []vmail.RuleAction{{Kind: "forward", Arg: "taker@elsewhere.example"}}}); err != nil {
		t.Fatal(err)
	}
	// A rule switched off sends nothing, and is not named.
	off, err := accts.SaveRule(ctx, vmail.FilterRule{Mailbox: "dana@example.com", Conditions: []vmail.RuleCondition{{Field: "subject", Op: "contains", Value: "old"}},
		Actions: []vmail.RuleAction{{Kind: "forward", Arg: "off@elsewhere.example"}}})
	if err != nil || off == 0 {
		t.Fatal(err)
	}
	dana := vmail.ReadAsOwner("dana")
	id, err := a.vayuMail.DrivePut(dana, 0, "plan.txt", "", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.vayuMail.DriveShareFile(dana, id, "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.vayuMail.DriveGrantTo(dana, id, "eli", false); err != nil {
		t.Fatal(err)
	}
	notice := reset()
	for _, want := range []string{"These were left as they were. If you did not set one up, remove it now:",
		"your mail is forwarded to copy@elsewhere.example", "rules forward mail to taker@elsewhere.example",
		"1 link to your files is open", "shared with 1 other mailbox"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice has no %q:\n%s", want, notice)
		}
	}
	if strings.Contains(notice, "off@elsewhere.example") {
		t.Errorf("a rule switched off was named:\n%s", notice)
	}
	if fwd := accts.ForwardFor(ctx, "dana@example.com"); fwd != "copy@elsewhere.example" {
		t.Errorf("the reset changed the forwarding to %q", fwd)
	}
}
