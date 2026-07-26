package mail

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Recovery is the highest-value attack surface in the product: a mailbox is the
// reset channel for its owner's bank, registrar and VPS. These tests are written
// to FAIL when a guard is removed, not to demonstrate the happy path.

func recoveryStore(t *testing.T) (*AccountStore, context.Context) {
	t.Helper()
	db, _ := sql.Open("sqlite3", ":memory:")
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewAccountStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	ctx := context.Background()
	if err := s.Create(ctx, "user@example.com", "hash", "User", RoleMailbox); err != nil {
		t.Fatalf("create account: %v", err)
	}
	return s, ctx
}

// TestRecoveryCodeIsSingleUse is the core guarantee. A code that survives its own
// redemption is not a recovery code, it is a password that was printed on paper.
func TestRecoveryCodeIsSingleUse(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)

	codes, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), RecoveryCodeCount)
	}
	if n := s.CountRecoveryCodes(ctx, "user@example.com"); n != RecoveryCodeCount {
		t.Errorf("remaining = %d, want %d", n, RecoveryCodeCount)
	}

	if !s.ConsumeRecoveryCode(ctx, "user@example.com", codes[0]) {
		t.Fatal("a freshly generated code was rejected")
	}
	if s.ConsumeRecoveryCode(ctx, "user@example.com", codes[0]) {
		t.Error("a spent code was accepted a second time")
	}
	if n := s.CountRecoveryCodes(ctx, "user@example.com"); n != RecoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d after one use", n, RecoveryCodeCount-1)
	}
	// Spending one code must not disturb the others.
	if !s.ConsumeRecoveryCode(ctx, "user@example.com", codes[1]) {
		t.Error("an unrelated code stopped working after a sibling was spent")
	}
}

// TestRecoveryCodeCannotBeRacedTwice covers the concurrency case the atomic
// consume exists for. Two simultaneous redemptions of one captured code both find
// the row; exactly one may win, or a captured code buys two resets.
func TestRecoveryCodeCannotBeRacedTwice(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	codes, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	const racers = 8
	var wg sync.WaitGroup
	results := make([]bool, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = s.ConsumeRecoveryCode(ctx, "user@example.com", codes[0])
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, ok := range results {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Errorf("%d concurrent redemptions of one code succeeded, want exactly 1", wins)
	}
}

// TestRecoveryCodesAreScopedToTheirAccount stops one mailbox's codes recovering
// another — the whole scheme collapses if codes are a global pool.
func TestRecoveryCodesAreScopedToTheirAccount(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	if err := s.Create(ctx, "other@example.com", "hash", "Other", RoleMailbox); err != nil {
		t.Fatalf("create other: %v", err)
	}
	codes, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if s.ConsumeRecoveryCode(ctx, "other@example.com", codes[0]) {
		t.Error("one mailbox's recovery code was accepted for a different mailbox")
	}
	if n := s.CountRecoveryCodes(ctx, "user@example.com"); n != RecoveryCodeCount {
		t.Errorf("the failed cross-account attempt consumed a code (remaining %d)", n)
	}
}

// TestRegeneratingCodesInvalidatesTheOldSheet pins why generation REPLACES.
// A holder regenerates because they think the old sheet is compromised; leaving
// the old codes live makes that action a no-op against the exact threat.
func TestRegeneratingCodesInvalidatesTheOldSheet(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	old, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	fresh, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if s.ConsumeRecoveryCode(ctx, "user@example.com", old[0]) {
		t.Error("a code from the replaced sheet still works — regeneration did not revoke")
	}
	if !s.ConsumeRecoveryCode(ctx, "user@example.com", fresh[0]) {
		t.Error("a code from the new sheet was rejected")
	}
	if n := s.CountRecoveryCodes(ctx, "user@example.com"); n != RecoveryCodeCount-1 {
		t.Errorf("remaining = %d, want %d — the old sheet was not cleared", n, RecoveryCodeCount-1)
	}
}

// TestRecoveryCodeAcceptsHumanTranscription: the code is read off paper, so
// case, spacing and dashes must not decide whether someone gets their mail back.
// Garbage must still be rejected.
func TestRecoveryCodeAcceptsHumanTranscription(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	codes, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	plain := strings.ReplaceAll(codes[0], "-", "")
	for i, variant := range []string{
		strings.ToLower(codes[0]),
		plain,
		" " + strings.ToLower(plain) + " ",
		plain[0:4] + " " + plain[4:8] + " " + plain[8:12],
	} {
		// Each variant is the SAME code, so only the first may succeed.
		got := s.ConsumeRecoveryCode(ctx, "user@example.com", variant)
		if i == 0 && !got {
			t.Errorf("variant %q rejected", variant)
		}
		if i > 0 && got {
			t.Errorf("variant %q of an already-spent code was accepted", variant)
		}
	}
	for _, bad := range []string{"", "not-a-code", "AAAA-AAAA-AAAA", codes[1] + "X"} {
		if s.ConsumeRecoveryCode(ctx, "user@example.com", bad) {
			t.Errorf("invalid code %q was accepted", bad)
		}
	}
}

// TestRecoveryCodesAreNotStoredInTheClear: a database read must not hand an
// attacker a working set of codes.
func TestRecoveryCodesAreNotStoredInTheClear(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	codes, err := s.GenerateRecoveryCodes(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT code_hash FROM vayumail_recovery_codes`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var stored []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err == nil {
			stored = append(stored, h)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(stored) != RecoveryCodeCount {
		t.Fatalf("stored %d hashes, want %d", len(stored), RecoveryCodeCount)
	}
	for _, h := range stored {
		if !strings.HasPrefix(h, "argon2id$") {
			t.Errorf("code stored as %q — codes are short enough to type, so they must be Argon2id, not a fast hash", h)
		}
		for _, c := range codes {
			if strings.Contains(h, c) {
				t.Fatal("a recovery code is recoverable from the stored row")
			}
		}
	}
}

// ── Recovery address ─────────────────────────────────────────────────────────

// TestRecoveryAddressMustBeOffThisInstall is the subtle one, and the reason the
// rule is not "a different domain". One install serves MANY mail domains, so a
// second domain hosted here fails in exactly the same way as the mailbox itself
// while looking, to the holder, like an independent provider.
func TestRecoveryAddressMustBeOffThisInstall(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	// This install serves example.com AND second.example — both must be refused.
	accepts := func(d string) bool { return d == "example.com" || d == "second.example" }

	for _, bad := range []string{
		"user@example.com",   // the mailbox itself
		"backup@example.com", // same domain
		"backup@second.example",
		"backup@EXAMPLE.COM", // case must not evade the check
	} {
		if err := s.SetRecoveryContactPending(ctx, "user@example.com", bad, accepts); err == nil {
			t.Errorf("%q was accepted as a recovery address, but this install serves it", bad)
		}
	}
	if err := s.SetRecoveryContactPending(ctx, "user@example.com", "someone@elsewhere.test", accepts); err != nil {
		t.Errorf("a genuinely off-install address was refused: %v", err)
	}
}

// TestRecoveryAddressRejectsMalformedInput — the address is where the master key
// gets sent, so a typo that parses is a typo that redirects it.
func TestRecoveryAddressRejectsMalformedInput(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	for _, bad := range []string{
		"", "nodomain", "@nolocal.test", "trailing@", "two@at@signs.test",
		"spaces in@address.test", "no-dot@localhost",
		"injection@x.test>, evil@attacker.test",
	} {
		if err := s.SetRecoveryContactPending(ctx, "user@example.com", bad, nil); err == nil {
			t.Errorf("malformed recovery address %q was accepted", bad)
		}
	}
}

// TestUnverifiedRecoveryAddressIsNotAFactor. An address nobody has proven
// control of is worse than none: it is a typo pointing at a stranger.
func TestUnverifiedRecoveryAddressIsNotAFactor(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	if err := s.SetRecoveryContactPending(ctx, "user@example.com", "someone@elsewhere.test", nil); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if got := s.RecoveryContact(ctx, "user@example.com"); got != "" {
		t.Errorf("RecoveryContact returned %q for an UNVERIFIED address", got)
	}
	st := s.RecoveryStatusFor(ctx, "user@example.com")
	if st.Ready {
		t.Error("an account with only a pending address reports itself recoverable")
	}
	if st.ContactPending != "someone@elsewhere.test" {
		t.Errorf("pending = %q, want it surfaced for the console", st.ContactPending)
	}

	if err := s.VerifyRecoveryContact(ctx, "user@example.com"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got := s.RecoveryContact(ctx, "user@example.com"); got != "someone@elsewhere.test" {
		t.Errorf("after verification RecoveryContact = %q", got)
	}
	st = s.RecoveryStatusFor(ctx, "user@example.com")
	if !st.Ready || st.ContactPending != "" {
		t.Errorf("after verification status = %+v, want ready with no pending", st)
	}
}

// TestVerifyWithNothingPendingIsRefused — otherwise a stale verification link
// could confirm an address the holder already withdrew.
func TestVerifyWithNothingPendingIsRefused(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	if err := s.VerifyRecoveryContact(ctx, "user@example.com"); err == nil {
		t.Error("verification succeeded with no address pending")
	}
	if err := s.SetRecoveryContactPending(ctx, "user@example.com", "someone@elsewhere.test", nil); err != nil {
		t.Fatalf("set pending: %v", err)
	}
	if err := s.ClearRecoveryContact(ctx, "user@example.com"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := s.VerifyRecoveryContact(ctx, "user@example.com"); err == nil {
		t.Error("a withdrawn address was still confirmable")
	}
}

// ── Reset tokens ─────────────────────────────────────────────────────────────

// TestResetTokenIsSingleUseAndBound covers replay and cross-account use.
func TestResetTokenIsSingleUseAndBound(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	tok, err := s.CreateRecoveryToken(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	got, err := s.ConsumeRecoveryToken(ctx, tok)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if got != "user@example.com" {
		t.Errorf("token resolved to %q", got)
	}
	if _, err := s.ConsumeRecoveryToken(ctx, tok); err == nil {
		t.Error("a captured reset link was replayable into a second reset")
	}
	if _, err := s.ConsumeRecoveryToken(ctx, "deadbeef"); err == nil {
		t.Error("a forged token was accepted")
	}
	if _, err := s.ConsumeRecoveryToken(ctx, ""); err == nil {
		t.Error("an empty token was accepted")
	}
}

// TestResetTokenCannotBeRacedTwice — same reasoning as the code race: the window
// between reading the row and deleting it must not buy a second reset.
func TestResetTokenCannotBeRacedTwice(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	tok, err := s.CreateRecoveryToken(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	const racers = 8
	var wg sync.WaitGroup
	wins := make([]bool, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.ConsumeRecoveryToken(ctx, tok)
			wins[i] = err == nil
		}(i)
	}
	close(start)
	wg.Wait()
	n := 0
	for _, ok := range wins {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d concurrent redemptions succeeded, want exactly 1", n)
	}
}

// TestExpiredResetTokenIsRejectedAndBurned. An expired link must not merely fail
// — it must be gone, so it cannot be retried against a later clock change.
func TestExpiredResetTokenIsRejectedAndBurned(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	tok, err := s.CreateRecoveryToken(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	past := time.Now().UTC().Add(-time.Hour).Format("2006-01-02 15:04:05")
	if _, err := s.db.ExecContext(ctx,
		`UPDATE vayumail_recovery_tokens SET expires_at=?`, past); err != nil {
		t.Fatalf("age the token: %v", err)
	}
	if _, err := s.ConsumeRecoveryToken(ctx, tok); err == nil {
		t.Fatal("an expired reset link was accepted")
	}
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vayumail_recovery_tokens`).Scan(&n)
	if n != 0 {
		t.Error("an expired token was left in the table instead of being burned")
	}
}

// TestResetTokenIsNotStoredInTheClear — a database read must not yield a working
// reset link for every mailbox on the server.
func TestResetTokenIsNotStoredInTheClear(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	tok, err := s.CreateRecoveryToken(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	var stored string
	if err := s.db.QueryRowContext(ctx, `SELECT token_hash FROM vayumail_recovery_tokens`).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if stored == tok || strings.Contains(stored, tok) {
		t.Fatal("the raw reset token is stored in the database")
	}
}

// TestInvalidateRecoveryTokensKillsOutstandingLinks. Any password change must
// void links minted before it — otherwise a link an attacker requested survives
// the victim noticing and changing their password.
func TestInvalidateRecoveryTokensKillsOutstandingLinks(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	tok, err := s.CreateRecoveryToken(ctx, "user@example.com")
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	s.InvalidateRecoveryTokens(ctx, "user@example.com")
	if _, err := s.ConsumeRecoveryToken(ctx, tok); err == nil {
		t.Error("a reset link survived an explicit invalidation")
	}
}

// ── Readiness ────────────────────────────────────────────────────────────────

// TestUnrecoverableAccountsIsHonest drives the readiness view. Reporting an
// account as covered when it is not is the one failure mode that matters here,
// because nobody finds out until the day they are locked out.
func TestUnrecoverableAccountsIsHonest(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	for _, e := range []string{"codes@example.com", "contact@example.com", "pending@example.com"} {
		if err := s.Create(ctx, e, "hash", "X", RoleMailbox); err != nil {
			t.Fatalf("create %s: %v", e, err)
		}
	}
	if _, err := s.GenerateRecoveryCodes(ctx, "codes@example.com"); err != nil {
		t.Fatalf("codes: %v", err)
	}
	if err := s.SetRecoveryContactPending(ctx, "contact@example.com", "a@elsewhere.test", nil); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := s.VerifyRecoveryContact(ctx, "contact@example.com"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Enrolled but never confirmed — must still count as unrecoverable.
	if err := s.SetRecoveryContactPending(ctx, "pending@example.com", "b@elsewhere.test", nil); err != nil {
		t.Fatalf("pending: %v", err)
	}

	got := map[string]bool{}
	for _, e := range s.UnrecoverableAccounts(ctx) {
		got[e] = true
	}
	if !got["user@example.com"] {
		t.Error("an account with no factor at all is missing from the readiness list")
	}
	if !got["pending@example.com"] {
		t.Error("an account whose only address is UNVERIFIED was reported as recoverable")
	}
	if got["codes@example.com"] {
		t.Error("an account holding unused codes was listed as unrecoverable")
	}
	if got["contact@example.com"] {
		t.Error("an account with a verified recovery address was listed as unrecoverable")
	}

	// Burning every code must move the account back onto the list.
	codes, err := s.GenerateRecoveryCodes(ctx, "codes@example.com")
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	for _, c := range codes {
		if !s.ConsumeRecoveryCode(ctx, "codes@example.com", c) {
			t.Fatalf("code %q rejected", c)
		}
	}
	got = map[string]bool{}
	for _, e := range s.UnrecoverableAccounts(ctx) {
		got[e] = true
	}
	if !got["codes@example.com"] {
		t.Error("an account that has spent every code is still reported as recoverable")
	}
}

// TestRecoveryCodeEntropyAndShape guards the generator itself: a predictable or
// short code is a bypass, and an ambiguous alphabet is a support burden.
func TestRecoveryCodeEntropyAndShape(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c, err := newRecoveryCode()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(c) != 14 || c[4] != '-' || c[9] != '-' {
			t.Fatalf("code %q is not XXXX-XXXX-XXXX", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q within 500 draws — the generator is not random", c)
		}
		seen[c] = true
		for _, r := range strings.ReplaceAll(c, "-", "") {
			if !strings.ContainsRune(recoveryCodeAlphabet, r) {
				t.Fatalf("code %q contains %q, which is outside the unambiguous alphabet", c, r)
			}
		}
	}
	// The characters people misread off paper must not be in play at all.
	for _, r := range "01OIL" {
		if strings.ContainsRune(recoveryCodeAlphabet, r) {
			t.Errorf("alphabet contains the easily-misread %q", r)
		}
	}
}

// ── Assisted recovery ────────────────────────────────────────────────────────

// TestRecoveryRequestAcceptsUnknownAddresses. The public endpoint must behave
// identically for a real mailbox and a made-up one, so the store cannot refuse
// one — refusing would be the enumeration oracle the whole flow avoids. The
// administrator sees the address and declines.
func TestRecoveryRequestAcceptsUnknownAddresses(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	if err := s.FileRecoveryRequest(ctx, "user@example.com", "locked out", "203.0.113.1"); err != nil {
		t.Fatalf("known address: %v", err)
	}
	if err := s.FileRecoveryRequest(ctx, "ghost@example.com", "", "203.0.113.2"); err != nil {
		t.Fatalf("unknown address must be accepted too: %v", err)
	}
	if n := len(s.PendingRecoveryRequests(ctx)); n != 2 {
		t.Errorf("%d pending requests, want 2", n)
	}
}

// TestRepeatedRequestsDoNotFloodTheQueue — an anxious holder clicking five times
// must not bury the administrator, and an attacker must not be able to.
func TestRepeatedRequestsDoNotFloodTheQueue(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	for i := 0; i < 5; i++ {
		if err := s.FileRecoveryRequest(ctx, "user@example.com", "try", "203.0.113.1"); err != nil {
			t.Fatalf("file: %v", err)
		}
	}
	if n := len(s.PendingRecoveryRequests(ctx)); n != 1 {
		t.Errorf("%d rows for one address, want the pending request reused", n)
	}
}

// TestRecoveryDecisionIsAtomic. Two administrators clicking Approve at the same
// moment must not both succeed — that would mint two live reset links for one
// authorisation.
func TestRecoveryDecisionIsAtomic(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	if err := s.FileRecoveryRequest(ctx, "user@example.com", "", ""); err != nil {
		t.Fatalf("file: %v", err)
	}
	id := s.PendingRecoveryRequests(ctx)[0].ID

	const racers = 6
	var wg sync.WaitGroup
	wins := make([]bool, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.DecideRecoveryRequest(ctx, id, "approved", "admin")
			wins[i] = err == nil
		}(i)
	}
	close(start)
	wg.Wait()
	n := 0
	for _, ok := range wins {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d concurrent approvals succeeded, want exactly 1", n)
	}
	if len(s.PendingRecoveryRequests(ctx)) != 0 {
		t.Error("the decided request is still pending")
	}
	// A decided request cannot be decided again.
	if _, err := s.DecideRecoveryRequest(ctx, id, "approved", "admin"); err == nil {
		t.Error("an already-decided request was approved a second time")
	}
}

// TestRecoveryNoteIsBounded — the note is free text from an unauthenticated
// caller and lands in an administrator's console.
func TestRecoveryNoteIsBounded(t *testing.T) {
	t.Parallel()
	s, ctx := recoveryStore(t)
	if err := s.FileRecoveryRequest(ctx, "user@example.com", strings.Repeat("A", 5000), ""); err != nil {
		t.Fatalf("file: %v", err)
	}
	got := s.PendingRecoveryRequests(ctx)
	if len(got) != 1 {
		t.Fatalf("want 1 request, got %d", len(got))
	}
	if len([]rune(got[0].Note)) > maxRecoveryNote {
		t.Errorf("note stored at %d runes, want it capped at %d", len([]rune(got[0].Note)), maxRecoveryNote)
	}
}
