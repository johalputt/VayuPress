// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// ── M-1: DMARC bypass via From-header abuse ──────────────────────────────────

// TestDMARCFailsClosedOnDuplicateFrom covers the classic display/verify split: a
// verifier reading the FIRST From header evaluates the attacker's own domain
// (which passes its own DMARC), while many mail clients render the LAST one. The
// reader sees the bank; the checks say pass.
func TestDMARCFailsClosedOnDuplicateFrom(t *testing.T) {
	raw := []byte("From: attacker@evil.example\r\n" +
		"From: ceo@bank.example\r\n" +
		"Subject: Wire transfer\r\n\r\nbody\r\n")

	got, ok := headerFromDomain(raw)
	if ok {
		t.Fatalf("two From headers accepted as well-formed (domain=%q)", got)
	}

	v := verifyInbound(context.Background(), "mx.test", net.ParseIP("192.0.2.1"), "evil.example", "a@evil.example", raw)
	if v.DMARC != "fail" {
		t.Errorf("DMARC = %q, want fail", v.DMARC)
	}
	if !v.Quarantine {
		t.Error("message with two From headers was not quarantined")
	}
	if !v.MalformedFrom {
		t.Error("MalformedFrom not set")
	}
}

// TestDMARCFailsClosedOnMultiAddressFrom is the more dangerous variant: the old
// ParseAddress call errored, the domain came back empty, and the whole DMARC
// block was SKIPPED — so the verdict was "none", which every downstream consumer
// reads as "this domain publishes no policy" rather than "we could not tell".
func TestDMARCFailsClosedOnMultiAddressFrom(t *testing.T) {
	raw := []byte("From: attacker@evil.example, ceo@bank.example\r\n\r\nbody\r\n")

	if _, ok := headerFromDomain(raw); ok {
		t.Fatal("multi-address From accepted as well-formed")
	}
	v := verifyInbound(context.Background(), "mx.test", net.ParseIP("192.0.2.1"), "evil.example", "a@evil.example", raw)
	if v.DMARC == "none" {
		t.Error("DMARC evaluation was skipped — the exact bypass this guards")
	}
	if !v.Quarantine {
		t.Error("multi-address From was not quarantined")
	}
}

// TestDMARCAcceptsWellFormedFrom guards against over-correction: a normal
// message must still parse, or this defence becomes a mail outage.
func TestDMARCAcceptsWellFormedFrom(t *testing.T) {
	for _, in := range []string{
		"From: Someone <someone@example.com>\r\n\r\nbody\r\n",
		"From: someone@example.com\r\n\r\nbody\r\n",
		"From: \"Last, First\" <someone@example.com>\r\n\r\nbody\r\n",
	} {
		d, ok := headerFromDomain([]byte(in))
		if !ok || d != "example.com" {
			t.Errorf("well-formed From rejected: %q -> (%q, %v)", in, d, ok)
		}
	}
}

// ── M-2: trusted-header injection ────────────────────────────────────────────

// TestStripsForgedAuthenticationResults covers RFC 8601 §5: a verifier MUST
// delete extant Authentication-Results fields, or a sender simply asserts
// "dkim=pass" about itself and anything reading the raw source believes it.
func TestStripsForgedAuthenticationResults(t *testing.T) {
	raw := []byte("Authentication-Results: mx.test; spf=pass; dkim=pass; dmarc=pass\r\n" +
		"From: attacker@evil.example\r\n" +
		"Subject: hi\r\n\r\nbody\r\n")

	out := stripTrustedHeaders(raw)
	if strings.Contains(string(out), "dkim=pass") {
		t.Error("forged Authentication-Results survived stripping")
	}
	if !strings.Contains(string(out), "From: attacker@evil.example") {
		t.Error("stripping removed an unrelated header")
	}
	if !strings.HasSuffix(string(out), "body\r\n") {
		t.Error("body was altered")
	}
}

// TestStripsForgedVayuMailHeaders covers the assertions the pipeline itself
// trusts. X-VayuMail-Forwarded is the sharpest: a sender who sets it inbound
// silently suppresses the recipient's own auto-forward — invisible, targeted mail
// loss that looks like a product bug.
func TestStripsForgedVayuMailHeaders(t *testing.T) {
	raw := []byte("X-VayuMail-Auth-Quarantine: no\r\n" +
		"X-VayuMail-Forwarded: yes\r\n" +
		"From: a@b.example\r\n\r\nbody\r\n")

	out := string(stripTrustedHeaders(raw))
	for _, bad := range []string{"X-VayuMail-Auth-Quarantine", "X-VayuMail-Forwarded"} {
		if strings.Contains(out, bad) {
			t.Errorf("forged %s survived stripping", bad)
		}
	}
}

// TestStripsFoldedForgedHeader guards the subtle case: a header folded across
// continuation lines must be removed WHOLE. Dropping only its first line leaves
// the continuation behind as a syntactically valid header of its own.
func TestStripsFoldedForgedHeader(t *testing.T) {
	raw := []byte("Authentication-Results: mx.test;\r\n" +
		"\tspf=pass;\r\n" +
		"\tdkim=pass\r\n" +
		"From: a@b.example\r\n\r\nbody\r\n")

	out := string(stripTrustedHeaders(raw))
	if strings.Contains(out, "spf=pass") || strings.Contains(out, "dkim=pass") {
		t.Errorf("folded continuation survived:\n%s", out)
	}
	if !strings.Contains(out, "From: a@b.example") {
		t.Error("stripping ate the following header")
	}
}

// TestStripPreservesBodyBytes pins that the body is untouched — DKIM signatures
// cover it, so a single altered byte breaks every downstream verification.
func TestStripPreservesBodyBytes(t *testing.T) {
	body := "line one\r\n..dot stuffed\r\nAuthentication-Results: not a header, in body\r\n"
	raw := []byte("From: a@b.example\r\n\r\n" + body)
	out := string(stripTrustedHeaders(raw))
	if !strings.HasSuffix(out, body) {
		t.Errorf("body altered:\n%q", out)
	}
}

// ── M-3/M-4: connection limiting ─────────────────────────────────────────────

// TestConnLimiterPerSourceCap is the control that makes AuthThrottle mean
// something: without it an attacker opens N sockets and the 2s delay becomes
// N/2 guesses per second.
func TestConnLimiterPerSourceCap(t *testing.T) {
	l := newConnLimiter(100, 3)
	addr := &net.TCPAddr{IP: net.ParseIP("198.51.100.7"), Port: 1234}

	var releases []func()
	for i := 0; i < 3; i++ {
		rel, ok := l.acquire(addr)
		if !ok {
			t.Fatalf("acquire %d refused below the cap", i)
		}
		releases = append(releases, rel)
	}
	if _, ok := l.acquire(addr); ok {
		t.Fatal("per-source cap not enforced")
	}
	// A different source must be unaffected — otherwise one host denies service
	// to everyone, which is the DoS rather than the defence.
	if _, ok := l.acquire(&net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 1}); !ok {
		t.Fatal("a different source was refused")
	}
	releases[0]()
	if _, ok := l.acquire(addr); !ok {
		t.Fatal("slot not returned on release")
	}
}

func TestConnLimiterGlobalCap(t *testing.T) {
	l := newConnLimiter(2, 100)
	a := &net.TCPAddr{IP: net.ParseIP("198.51.100.1"), Port: 1}
	b := &net.TCPAddr{IP: net.ParseIP("198.51.100.2"), Port: 2}
	if _, ok := l.acquire(a); !ok {
		t.Fatal("first refused")
	}
	if _, ok := l.acquire(b); !ok {
		t.Fatal("second refused")
	}
	if _, ok := l.acquire(&net.TCPAddr{IP: net.ParseIP("198.51.100.3"), Port: 3}); ok {
		t.Fatal("global cap not enforced")
	}
}

// TestConnLimiterReleaseIsIdempotent — a double release would corrupt the count
// downward and quietly disable the cap.
func TestConnLimiterReleaseIsIdempotent(t *testing.T) {
	l := newConnLimiter(1, 1)
	addr := &net.TCPAddr{IP: net.ParseIP("198.51.100.4"), Port: 1}
	rel, _ := l.acquire(addr)
	rel()
	rel()
	if l.total != 0 {
		t.Fatalf("total = %d after double release, want 0", l.total)
	}
	if len(l.perAddr) != 0 {
		t.Fatalf("perAddr leaked %d entries", len(l.perAddr))
	}
}

// TestConnLimiterDoesNotLeakPerAddrEntries — a map that grows once per distinct
// source is itself a memory exhaustion vector on a public port.
func TestConnLimiterDoesNotLeakPerAddrEntries(t *testing.T) {
	l := newConnLimiter(1000, 10)
	for i := 0; i < 500; i++ {
		rel, ok := l.acquire(&net.TCPAddr{IP: net.IPv4(198, 51, 100, byte(i%256)), Port: i})
		if ok {
			rel()
		}
	}
	if len(l.perAddr) != 0 {
		t.Fatalf("perAddr retained %d entries after all releases", len(l.perAddr))
	}
}

// ── R-1: password spraying (per-source throttle) ─────────────────────────────

// TestSourceThrottleCatchesSpraying is the regression for the residual risk left
// open by the first audit pass: AuthThrottle is keyed by MAILBOX, so one password
// tried against a thousand different accounts accrues no delay anywhere — every
// address has its own independent counter and each sees exactly one failure.
// Spraying is the dominant attack against mail servers for precisely that reason.
func TestSourceThrottleCatchesSpraying(t *testing.T) {
	th := NewAuthThrottleWithMax(8 * time.Second)
	src := "198.51.100.42"

	// Same source, every failure against a DIFFERENT mailbox — the shape a
	// per-mailbox counter is blind to.
	for i := 0; i < 6; i++ {
		if th.Delay(src) > 0 && i == 0 {
			t.Fatal("delay applied before any failure")
		}
		th.Fail(src)
	}
	if th.Delay(src) <= 0 {
		t.Fatal("spraying from one source accrued no delay")
	}

	// A different source must be untouched, or one noisy host degrades everyone.
	if d := th.Delay("203.0.113.9"); d != 0 {
		t.Errorf("unrelated source delayed by %v", d)
	}
}

// TestSourceThrottleCeilingIsHigher — the source signal is stronger than the
// per-mailbox one (a mailbox failing repeatedly is usually a typo or a stale
// client; a source failing across many mailboxes is not something legitimate use
// produces), so it is allowed to bite harder.
func TestSourceThrottleCeilingIsHigher(t *testing.T) {
	mailbox := NewAuthThrottle()
	source := NewAuthThrottleWithMax(8 * time.Second)
	for i := 0; i < 200; i++ {
		mailbox.Fail("someone@example.com")
		source.Fail("198.51.100.43")
	}
	mb, sd := mailbox.Delay("someone@example.com"), source.Delay("198.51.100.43")
	if mb > authDelayMax {
		t.Errorf("mailbox delay %v exceeded its ceiling %v", mb, authDelayMax)
	}
	if sd <= mb {
		t.Errorf("source delay %v is not above the mailbox ceiling %v", sd, mb)
	}
}

// TestSourceThrottleForgivesSuccess — a legitimate user behind a shared NAT whose
// neighbour was guessing must not stay penalised once they authenticate. This is
// why the defence is a decaying delay rather than a per-IP block: shared
// addresses are the norm on mobile networks.
func TestSourceThrottleForgivesSuccess(t *testing.T) {
	th := NewAuthThrottleWithMax(8 * time.Second)
	src := "198.51.100.44"
	for i := 0; i < 5; i++ {
		th.Fail(src)
	}
	if th.Delay(src) == 0 {
		t.Fatal("no delay after failures")
	}
	th.Success(src)
	if d := th.Delay(src); d != 0 {
		t.Errorf("delay %v persisted after a successful authentication", d)
	}
}

// ── R-2: inbound verification is bounded ─────────────────────────────────────

// TestInboundVerificationRespectsDeadline pins that DNS-dependent verification
// cannot hold an SMTP connection indefinitely. A sender controlling their own
// authoritative nameserver can answer as slowly as the resolver tolerates, and
// this work runs inside the transaction.
func TestInboundVerificationRespectsDeadline(t *testing.T) {
	raw := []byte("From: someone@example.com\r\nSubject: hi\r\n\r\nbody\r\n")

	// An already-cancelled context must not stall: every lookup is bound to it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan AuthVerdict, 1)
	go func() { done <- verifyInbound(ctx, "mx.test", net.ParseIP("192.0.2.1"), "h", "a@example.com", raw) }()

	select {
	case v := <-done:
		// A cancelled context yields degraded verdicts, never a hang.
		if v.Header == "" {
			t.Error("no Authentication-Results assembled")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("verification ignored a cancelled context — the connection-hold vector")
	}
}

// TestDKIMVerificationIsCapped guards the amplifier: each signature costs a
// public-key operation plus a DNS lookup, signature headers are small, so an
// unbounded count turns one cheap message into thousands of asymmetric operations
// and thousands of queries. The message size limit is no defence.
func TestDKIMVerificationIsCapped(t *testing.T) {
	if maxDKIMVerifications <= 0 {
		t.Fatal("DKIM verification count is unbounded")
	}
	if maxDKIMVerifications > 20 {
		t.Errorf("maxDKIMVerifications = %d is too generous to bound the work",
			maxDKIMVerifications)
	}

	var sigs strings.Builder
	for i := 0; i < 500; i++ {
		sigs.WriteString("DKIM-Signature: v=1; a=rsa-sha256; d=evil.example; s=s; b=AAAA\r\n")
	}
	raw := []byte(sigs.String() + "From: a@evil.example\r\n\r\nbody\r\n")

	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_ = verifyInbound(ctx, "mx.test", net.ParseIP("192.0.2.1"), "h", "a@evil.example", raw)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("500 signatures were not bounded")
	}
}
