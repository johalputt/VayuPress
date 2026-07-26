package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/vayuos/mail"
	_ "github.com/mattn/go-sqlite3"
)

func cliStore(t *testing.T) (*mail.AccountStore, context.Context) {
	t.Helper()
	db, _ := sql.Open("sqlite3", ":memory:")
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	s, err := mail.NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, "user@example.com", "hash", "User", mail.RoleMailbox); err != nil {
		t.Fatalf("create: %v", err)
	}
	return s, ctx
}

// TestBreakGlassStillRevokesAppPasswords is the important one. The break-glass
// path runs when something has already gone wrong, which makes it the last place
// that should cut corners — a reset here that left an attacker's enrolled device
// connected would be worse than useless, because the operator would believe the
// account was secured.
func TestBreakGlassStillRevokesAppPasswords(t *testing.T) {
	t.Parallel()
	s, ctx := cliStore(t)
	if _, err := s.CreateAppPassword(ctx, "user@example.com", "phone", "hashA"); err != nil {
		t.Fatalf("app password: %v", err)
	}
	if _, err := s.CreateAppPassword(ctx, "user@example.com", "laptop", "hashB"); err != nil {
		t.Fatalf("app password: %v", err)
	}
	if n := len(s.AppPasswordCredentials(ctx, "user@example.com")); n != 2 {
		t.Fatalf("setup: %d credentials, want 2", n)
	}

	var out strings.Builder
	if err := runMailCLI(ctx, []string{"passwd", "user@example.com", "a-new-password"}, &out, s); err != nil {
		t.Fatalf("break-glass: %v", err)
	}
	if n := len(s.AppPasswordCredentials(ctx, "user@example.com")); n != 0 {
		t.Errorf("%d app password(s) survived a break-glass reset", n)
	}
	// It must also say what it did NOT do, or an operator assumes sessions and the
	// outbound queue were handled when the server was not even running.
	for _, want := range []string{
		"2 app password(s) revoked",
		"live webmail sessions are not ended",
		"queued outbound mail is not held",
		"audit log",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("break-glass output is missing %q:\n%s", want, out.String())
		}
	}
}

// TestBreakGlassRefusesUnknownMailbox — a typo must not silently succeed and
// leave the operator believing they reset something.
func TestBreakGlassRefusesUnknownMailbox(t *testing.T) {
	t.Parallel()
	s, ctx := cliStore(t)
	var out strings.Builder
	if err := runMailCLI(ctx, []string{"passwd", "nobody@example.com", "a-new-password"}, &out, s); err == nil {
		t.Error("break-glass accepted a mailbox that does not exist")
	}
	if err := runMailCLI(ctx, []string{"passwd", "user@example.com", "short"}, &out, s); err == nil {
		t.Error("break-glass accepted a password below the minimum length")
	}
}

// TestRecoveryCLIReportsHonestly. The whole point of the readiness view is that
// it does not overstate: an account with an unverified address is not covered.
func TestRecoveryCLIReportsHonestly(t *testing.T) {
	t.Parallel()
	s, ctx := cliStore(t)

	var out strings.Builder
	if err := runMailCLI(ctx, []string{"recovery", "user@example.com"}, &out, s); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if !strings.Contains(out.String(), "CANNOT be recovered") {
		t.Errorf("a mailbox with nothing enrolled was not reported as unrecoverable:\n%s", out.String())
	}

	out.Reset()
	if err := runMailCLI(ctx, []string{"unrecoverable"}, &out, s); err != nil {
		t.Fatalf("unrecoverable: %v", err)
	}
	if !strings.Contains(out.String(), "user@example.com") {
		t.Errorf("the unrecoverable list omitted an unenrolled mailbox:\n%s", out.String())
	}

	// Enrol an UNVERIFIED address — still unrecoverable.
	if err := s.SetRecoveryContactPending(ctx, "user@example.com", "a@elsewhere.test", nil); err != nil {
		t.Fatalf("pending: %v", err)
	}
	out.Reset()
	if err := runMailCLI(ctx, []string{"recovery", "user@example.com"}, &out, s); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if !strings.Contains(out.String(), "CANNOT be recovered") {
		t.Errorf("an UNVERIFIED address was reported as recovery:\n%s", out.String())
	}

	// Verify it — now covered.
	if err := s.VerifyRecoveryContact(ctx, "user@example.com"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	out.Reset()
	if err := runMailCLI(ctx, []string{"recovery", "user@example.com"}, &out, s); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if !strings.Contains(out.String(), "can be recovered") {
		t.Errorf("a verified address was not counted:\n%s", out.String())
	}
}

// TestMailCLIRefusesWithoutVayuMail — better a clear message than a nil panic on
// an install where mail was never configured.
func TestMailCLIRefusesWithoutVayuMail(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := runMailCLI(context.Background(), []string{"recovery", "a@b.co"}, &out, nil); err == nil {
		t.Error("the CLI ran against a nil account store")
	}
	if err := runMailCLI(context.Background(), nil, &out, nil); err == nil {
		t.Error("no arguments should print usage as an error")
	}
}

// TestRecoveryConsoleJSNeverUsesInnerHTML. The panel renders mail addresses and
// server error strings, and it is the surface that displays freshly minted
// credentials.
func TestRecoveryConsoleJSNeverUsesInnerHTML(t *testing.T) {
	t.Parallel()
	js := withoutComments(repoFile(t, "static/js/admin-os-mail-recovery.js"))
	if strings.Contains(js, "innerHTML") {
		t.Error("the recovery panel must build nodes with textContent, never innerHTML")
	}
	// Codes exist in readable form exactly once. Leaving a previous mailbox's set
	// on screen after switching selection would get them written down against the
	// wrong account.
	if !strings.Contains(js, "codesEl.textContent = ''") {
		t.Error("switching mailbox must clear any codes still on screen")
	}
	// Regeneration silently invalidates the sheet the holder is carrying.
	if !strings.Contains(js, "window.confirm") {
		t.Error("regenerating codes must be confirmed — it revokes the holder's existing sheet")
	}
	if !strings.Contains(js, "X-CSRF-Token") {
		t.Error("credential-issuing writes must carry the CSRF token")
	}
}

// TestRecoveryCardDoesNotOverclaim pins the chip wording. "Recovery enabled"
// would describe the feature while saying nothing about whether any mailbox
// could actually be recovered — the distinction this card exists to make.
func TestRecoveryCardDoesNotOverclaim(t *testing.T) {
	t.Parallel()
	src := repoFile(t, "cmd/vayupress/admin_os_mail_recovery.go")
	code := withoutComments(src)
	if strings.Contains(code, "Recovery enabled") {
		t.Error(`the chip must report how many mailboxes are covered, not that the feature exists`)
	}
	if !strings.Contains(code, "covered") || !strings.Contains(code, "cannot be recovered") {
		t.Error("the chip must state the covered/uncovered counts")
	}
	// The Tor-mode caveat must be surfaced in the console, not just the log.
	if !strings.Contains(code, "safefetch.ClearnetBlocked()") {
		t.Error("the card must tell a Tor-mode operator that codes are the only working factor")
	}
	// Enrolment is a credential-issuing surface; it must be admin-gated.
	if n := strings.Count(code, "a.isAdminRequest(r)"); n < 3 {
		t.Errorf("expected every recovery endpoint to be admin-gated, found %d checks", n)
	}
}
