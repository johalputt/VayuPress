// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The property under test is COMPLETENESS. A reset that changes the password and
// nothing else looks correct in review and in manual testing — the holder signs
// in with the new password and everything seems fine — while an attacker's
// enrolled device keeps full IMAP and SMTP access. That is why the pipeline takes
// its dependencies as function fields: so a test can prove each step ran.

type resetSpy struct {
	order            []string
	passwordHash     string
	revokedApp       bool
	revokedSessions  bool
	invalidated      bool
	held             bool
	notifiedTo       string
	notifiedSubject  string
	notifiedBody     string
	filedTo          string
	filedBody        string
	auditAction      string
	auditActor       string
	auditTarget      string
	auditDetail      string
	setPasswordError error
	revokeAppError   error
}

func (s *resetSpy) deps() mailResetDeps {
	return mailResetDeps{
		SetPasswordHash: func(_ context.Context, _, hash string) error {
			s.order = append(s.order, "password")
			if s.setPasswordError != nil {
				return s.setPasswordError
			}
			s.passwordHash = hash
			return nil
		},
		RevokeAppPasswords: func(context.Context, string) (int, error) {
			s.order = append(s.order, "app-passwords")
			if s.revokeAppError != nil {
				return 0, s.revokeAppError
			}
			s.revokedApp = true
			return 3, nil
		},
		RevokeSessions: func(context.Context, string) (int, error) {
			s.order = append(s.order, "sessions")
			s.revokedSessions = true
			return 2, nil
		},
		InvalidateTokens: func(context.Context, string) {
			s.order = append(s.order, "tokens")
			s.invalidated = true
		},
		HoldQueue: func(context.Context, string) (int, error) {
			s.order = append(s.order, "queue")
			s.held = true
			return 4, nil
		},
		RecoveryContact: func(context.Context, string) string { return "backup@elsewhere.test" },
		Notify: func(_ context.Context, to, subject, body string) error {
			s.order = append(s.order, "notify")
			s.notifiedTo, s.notifiedSubject, s.notifiedBody = to, subject, body
			return nil
		},
		FileToMailbox: func(_ context.Context, mailbox, _, body string) error {
			s.order = append(s.order, "file")
			s.filedTo, s.filedBody = mailbox, body
			return nil
		},
		Audit: func(action, actor, target, detail string) {
			s.order = append(s.order, "audit")
			s.auditAction, s.auditActor, s.auditTarget, s.auditDetail = action, actor, target, detail
		},
		Now: func() time.Time { return time.Date(2026, 7, 26, 14, 22, 0, 0, time.UTC) },
	}
}

// TestResetRevokesEverySurvivingCredential is the one that matters. Each of these
// outlives a naive password change, and each is a complete bypass of the reset.
func TestResetRevokesEverySurvivingCredential(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{}
	out, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"User@Example.com", "a-good-password", mailResetByCode, "self")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if !spy.revokedApp {
		t.Error("app passwords survived the reset — an enrolled attacker device keeps IMAP and SMTP access")
	}
	if !spy.revokedSessions {
		t.Error("member sessions survived the reset — an attacker stays signed in to webmail")
	}
	if !spy.invalidated {
		t.Error("outstanding reset links survived — a second link is still a working key")
	}
	if !spy.held {
		t.Error("queued outbound was not held — staged mail sends under the recovered account")
	}
	if out.AppPasswordsRevoked != 3 || out.SessionsRevoked != 2 || out.QueueHeld != 4 {
		t.Errorf("outcome = %+v, want the counts reported back to the caller", out)
	}
	if len(out.Problems) != 0 {
		t.Errorf("unexpected problems: %v", out.Problems)
	}
}

// TestResetHashesAndNeverStoresThePlaintext.
func TestResetHashesAndNeverStoresThePlaintext(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{}
	const pw = "correct-horse-battery"
	if _, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"user@example.com", pw, mailResetByCode, "self"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if spy.passwordHash == pw || strings.Contains(spy.passwordHash, pw) {
		t.Fatal("the plaintext password reached the store")
	}
	if !strings.HasPrefix(spy.passwordHash, "argon2id$") {
		t.Errorf("password stored as %q, want an Argon2id hash", spy.passwordHash)
	}
}

// TestResetStopsWhenThePasswordCannotChange. Revoking a holder's devices while
// their OLD password still works would lock them further out — the opposite of
// recovery — so nothing after step 1 may run.
func TestResetStopsWhenThePasswordCannotChange(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{setPasswordError: errors.New("no such account")}
	if _, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"user@example.com", "a-good-password", mailResetByCode, "self"); err == nil {
		t.Fatal("a failed password change reported success")
	}
	if spy.revokedApp || spy.revokedSessions || spy.held {
		t.Errorf("credentials were revoked after the password change failed: %v", spy.order)
	}
}

// TestResetCompletesEvenWhenAStepFails. The dangerous outcome is a half-done
// reset, so a failing step is recorded and the rest still runs.
func TestResetCompletesEvenWhenAStepFails(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{revokeAppError: errors.New("database is locked")}
	out, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"user@example.com", "a-good-password", mailResetByCode, "self")
	if err != nil {
		t.Fatalf("a recoverable step failure aborted the whole reset: %v", err)
	}
	if !spy.revokedSessions || !spy.held {
		t.Error("a failure in one step stopped the later ones")
	}
	if len(out.Problems) != 1 || !strings.Contains(out.Problems[0], "revoke app passwords") {
		t.Errorf("problems = %v, want the failed step surfaced to the caller", out.Problems)
	}
	if !strings.Contains(spy.auditDetail, "problems=") {
		t.Errorf("audit detail = %q, want the failure recorded", spy.auditDetail)
	}
}

// TestResetNotifiesBothChannels. The in-mailbox copy is not redundant: it is the
// only channel that exists for an account with no recovery address.
func TestResetNotifiesBothChannels(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{}
	out, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"user@example.com", "a-good-password", mailResetByLink, "self")
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if spy.notifiedTo != "backup@elsewhere.test" || out.NotifiedContact != "backup@elsewhere.test" {
		t.Errorf("recovery address not notified (to=%q out=%q)", spy.notifiedTo, out.NotifiedContact)
	}
	if spy.filedTo != "user@example.com" {
		t.Errorf("notice not filed into the mailbox (got %q)", spy.filedTo)
	}
	// The notice has to be actionable, not merely present.
	for _, want := range []string{"was reset", "signed out", "If this was NOT you"} {
		if !strings.Contains(spy.notifiedBody, want) {
			t.Errorf("notice is missing %q:\n%s", want, spy.notifiedBody)
		}
	}
	// It must say what was destroyed, or the reader cannot tell a real reset from
	// a takeover that left devices connected.
	if !strings.Contains(spy.notifiedBody, "3 connected apps") {
		t.Errorf("notice does not report the revoked devices:\n%s", spy.notifiedBody)
	}
	if !strings.Contains(spy.notifiedBody, "recovery address") {
		t.Errorf("notice for a link-based reset does not say how it was authorised:\n%s", spy.notifiedBody)
	}
}

// TestResetAuditsWhatItDestroyed. "Something happened" is not an audit trail; an
// investigator needs to know whether the attacker's devices were cut off.
func TestResetAuditsWhatItDestroyed(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{}
	if _, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"user@example.com", "a-good-password", mailResetByAdmin, "admin@example.com"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if spy.auditAction != "vayumail.password.reset" {
		t.Errorf("audit action = %q", spy.auditAction)
	}
	if spy.auditActor != "admin@example.com" || spy.auditTarget != "user@example.com" {
		t.Errorf("audit actor/target = %q/%q", spy.auditActor, spy.auditTarget)
	}
	for _, want := range []string{
		"reason=administrator", "app_passwords_revoked=3", "sessions_revoked=2", "queue_held=4",
	} {
		if !strings.Contains(spy.auditDetail, want) {
			t.Errorf("audit detail %q is missing %q", spy.auditDetail, want)
		}
	}
}

// TestResetRejectsAWeakPassword — recovery must not be a way around the strength
// rule the console enforces.
func TestResetRejectsAWeakPassword(t *testing.T) {
	t.Parallel()
	for _, pw := range []string{"", "short", "1234567"} {
		spy := &resetSpy{}
		if _, err := applyMailPasswordReset(context.Background(), spy.deps(),
			"user@example.com", pw, mailResetByCode, "self"); err == nil {
			t.Errorf("password %q was accepted", pw)
		}
		if len(spy.order) != 0 {
			t.Errorf("password %q ran pipeline steps before validation: %v", pw, spy.order)
		}
	}
}

// TestResetRunsRevocationBeforeNotifying. Order is security-relevant: the notice
// claims the devices were signed out, so it must not be sent before they were.
func TestResetRunsRevocationBeforeNotifying(t *testing.T) {
	t.Parallel()
	spy := &resetSpy{}
	if _, err := applyMailPasswordReset(context.Background(), spy.deps(),
		"user@example.com", "a-good-password", mailResetByCode, "self"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	pos := map[string]int{}
	for i, step := range spy.order {
		if _, seen := pos[step]; !seen {
			pos[step] = i
		}
	}
	for _, pair := range [][2]string{
		{"password", "app-passwords"},
		{"app-passwords", "notify"},
		{"sessions", "notify"},
		{"queue", "notify"},
		{"notify", "audit"},
	} {
		if pos[pair[0]] > pos[pair[1]] {
			t.Errorf("%q ran after %q — order was %v", pair[0], pair[1], spy.order)
		}
	}
}

// TestResetToleratesMissingChannels covers a Tor Space, where there is no
// clearnet egress to notify over. Recovery must still work there — it is the
// environment where recovery codes are the ONLY factor.
func TestResetToleratesMissingChannels(t *testing.T) {
	t.Parallel()
	d := mailResetDeps{
		SetPasswordHash:    func(context.Context, string, string) error { return nil },
		RevokeAppPasswords: func(context.Context, string) (int, error) { return 1, nil },
		// No RevokeSessions, HoldQueue, Notify, FileToMailbox or Audit.
	}
	out, err := applyMailPasswordReset(context.Background(), d,
		"user@example.com", "a-good-password", mailResetByCode, "self")
	if err != nil {
		t.Fatalf("reset failed with no notification channel available: %v", err)
	}
	if out.AppPasswordsRevoked != 1 {
		t.Errorf("outcome = %+v", out)
	}
	if len(out.Problems) != 0 {
		t.Errorf("absent optional channels were reported as problems: %v", out.Problems)
	}
}

// TestNoticeMessageCannotForgeHeaders. The notice is assembled by hand, so the
// assembler — not its current callers — must be the thing that refuses a
// newline. A future caller passing a mailbox name through would otherwise inject
// headers into a message delivered straight to a Maildir.
func TestNoticeMessageCannotForgeHeaders(t *testing.T) {
	t.Parallel()
	raw := string(buildNoticeMessage(
		"security@example.com",
		"victim@example.com\r\nBcc: attacker@evil.test",
		"Reset\r\nX-Injected: yes",
		"body",
		time.Date(2026, 7, 26, 14, 22, 0, 0, time.UTC)))

	head, _, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok {
		t.Fatal("message has no header/body separator")
	}
	for _, forged := range []string{"Bcc:", "X-Injected:"} {
		if strings.Contains(head, forged) {
			t.Errorf("header injection succeeded — %q appears in:\n%s", forged, head)
		}
	}
	for _, want := range []string{"From: ", "To: ", "Subject: ", "Content-Type: text/plain"} {
		if !strings.Contains(head, want) {
			t.Errorf("message is missing %q", want)
		}
	}
}
