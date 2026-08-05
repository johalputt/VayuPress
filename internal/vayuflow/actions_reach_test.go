// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeMailer struct {
	sync.Mutex
	sent []string
	err  error
}

func (f *fakeMailer) Send(_ context.Context, to, subject, _ string) error {
	f.Lock()
	defer f.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, to+"|"+subject)
	return nil
}

func (f *fakeMailer) count() int {
	f.Lock()
	defer f.Unlock()
	return len(f.sent)
}

type fakeFetcher struct {
	sync.Mutex
	got  []string
	body string
	err  error
}

func (f *fakeFetcher) Fetch(_ context.Context, u string) (string, error) {
	f.Lock()
	defer f.Unlock()
	f.got = append(f.got, u)
	return f.body, f.err
}

func (f *fakeFetcher) count() int {
	f.Lock()
	defer f.Unlock()
	return len(f.got)
}

func wireReach(t *testing.T) (*fakeFetcher, *fakeMailer) {
	t.Helper()
	ff, fm := &fakeFetcher{body: "ok"}, &fakeMailer{}
	SetFetcher(ff)
	SetMailSender(fm)
	t.Cleanup(func() { SetFetcher(nil); SetMailSender(nil) })
	return ff, fm
}

// THE P7 GATE. Egress actions are inert under VAYUOS_MODE=tor — and the refusal
// happens in the effect path, so it does not depend on the action remembering.
func TestEgressActionsAreInertInATorSpace(t *testing.T) {
	ff, _ := wireReach(t)
	orig := clearnetBlocked
	clearnetBlocked = func() bool { return true }
	t.Cleanup(func() { clearnetBlocked = orig })

	c, _ := CapabilityFor("egress.fetch")
	fn, _ := actionFor("egress.fetch")
	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}

	_, err := fn(context.Background(), map[string]string{"url": "https://example.com/x"}, e)
	if err == nil {
		t.Fatal("a fetch happened in a Tor Space")
	}
	if errors.Is(err, ErrDryRun) {
		t.Fatal("the Tor refusal must not be reported as a dry-run capture")
	}
	// Judged on what the fetcher was ASKED to do, not on the return value: an
	// action that made the request and then reported an error would still have
	// leaked.
	if ff.count() != 0 {
		t.Fatalf("the guarded client was called %d time(s) in a Tor Space", ff.count())
	}
	if spend.Egress != 0 {
		t.Errorf("a refused fetch charged %d against the ceiling", spend.Egress)
	}
}

// Every registered egress-kind action must be inert, not just the one that
// exists today. This is the registry's job rather than a per-action habit.
func TestEveryEgressKindActionIsRegisteredInert(t *testing.T) {
	got := CapabilitiesOfKind(KindEgress)
	if len(got) == 0 {
		t.Fatal("no egress actions are registered; this test is proving nothing")
	}
	for _, c := range got {
		if c.Onion != OnionInert {
			t.Errorf("%s declares Onion=%s; an outbound call must be inert in a Tor Space", c.Action, c.Onion)
		}
	}
}

func TestAFetchSpendsTheEgressCeilingAndIsBounded(t *testing.T) {
	ff, _ := wireReach(t)
	c, _ := CapabilityFor("egress.fetch")
	fn, _ := actionFor("egress.fetch")

	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{"url": "https://example.com/x"}, e); err != nil {
		t.Fatal(err)
	}
	if spend.Egress != 1 || ff.count() != 1 {
		t.Errorf("one fetch should spend one egress and make one request, got spend=%d calls=%d",
			spend.Egress, ff.count())
	}
	// The second is refused by the ceiling, and must not reach the client.
	before := ff.count()
	if _, err := fn(context.Background(), map[string]string{"url": "https://example.com/y"}, e); err == nil {
		t.Fatal("a fetch past the egress ceiling was permitted")
	}
	if ff.count() != before {
		t.Error("a fetch refused by the ceiling still reached the client")
	}
}

// A body from outside this install is the most obviously attacker-controlled
// value in the engine, so it is bounded like a generation.
func TestAnOversizedResponseFailsTheStep(t *testing.T) {
	ff, _ := wireReach(t)
	ff.body = strings.Repeat("x", MaxFetchBody+1)
	c, _ := CapabilityFor("egress.fetch")
	fn, _ := actionFor("egress.fetch")
	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{"url": "https://example.com/x"}, e); err == nil {
		t.Fatal("an unbounded response body was accepted")
	}
}

// Only http(s) reaches a client at all — a scheme this code does not understand
// is not one it can make a safety claim about.
func TestOnlyHTTPSchemesAreFetched(t *testing.T) {
	ff, _ := wireReach(t)
	c, _ := CapabilityFor("egress.fetch")
	fn, _ := actionFor("egress.fetch")
	for _, raw := range []string{
		"file:///etc/passwd", "gopher://x", "ftp://x/y", "data:text/plain,hi", "  ", "://nonsense",
	} {
		var spend Spend
		e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
		if _, err := fn(context.Background(), map[string]string{"url": raw}, e); err == nil {
			t.Errorf("scheme in %q was accepted", raw)
		}
		if spend.Egress != 0 {
			t.Errorf("%q charged the egress ceiling before being refused", raw)
		}
	}
	if ff.count() != 0 {
		t.Errorf("a refused scheme reached the client %d time(s)", ff.count())
	}
}

// Mail is live and irreversible: a dry run must capture it and NOT deliver.
func TestADryRunNeverDelivers(t *testing.T) {
	_, fm := wireReach(t)
	c, _ := CapabilityFor("mail.send")
	fn, _ := actionFor("mail.send")
	var spend Spend
	e := &Effects{mode: RunDryRun, cap: c, budget: testBudget(), spend: &spend}

	_, err := fn(context.Background(), map[string]string{
		"to": "a@example.com", "subject": "Digest", "body": "hello"}, e)
	if !errors.Is(err, ErrDryRun) {
		t.Fatalf("a dry-run mail step should capture, got %v", err)
	}
	if fm.count() != 0 {
		t.Fatalf("a dry run DELIVERED %d message(s); this is the one effect that cannot be undone",
			fm.count())
	}
	if len(e.refusals) == 0 || !strings.Contains(e.refusals[0], "a@example.com") {
		t.Errorf("the capture must name the recipient, got %v", e.refusals)
	}
}

func TestALiveMailStepDelivers(t *testing.T) {
	_, fm := wireReach(t)
	c, _ := CapabilityFor("mail.send")
	fn, _ := actionFor("mail.send")
	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{
		"to": "a@example.com", "subject": "Digest", "body": "hello"}, e); err != nil {
		t.Fatal(err)
	}
	if fm.count() != 1 {
		t.Fatalf("expected one delivery, got %d", fm.count())
	}
	if spend.Writes != 1 {
		t.Errorf("a delivery must spend a write, got %d", spend.Writes)
	}
}

// An empty message is refused before the ceiling is charged: sending nothing to
// someone is not a thing an automation should spend a budget doing.
func TestAnEmptyMessageIsRefusedWithoutCharging(t *testing.T) {
	_, fm := wireReach(t)
	c, _ := CapabilityFor("mail.send")
	fn, _ := actionFor("mail.send")
	for _, p := range []map[string]string{
		{"to": "a@example.com", "subject": "s", "body": "   "},
		{"to": "", "subject": "s", "body": "b"},
		{"to": "a@example.com", "subject": "", "body": "b"},
	} {
		var spend Spend
		e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
		if _, err := fn(context.Background(), p, e); err == nil {
			t.Errorf("params %v produced a delivery", p)
		}
		if spend.Writes != 0 {
			t.Errorf("params %v charged a write before validating", p)
		}
	}
	if fm.count() != 0 {
		t.Errorf("a refused message was delivered %d time(s)", fm.count())
	}
}

// An unwired sender must fail the step loudly. A run that "succeeded" having
// delivered nothing is the worst outcome: the trail says it worked.
func TestAnUnwiredSenderFailsTheStep(t *testing.T) {
	SetMailSender(nil)
	c, _ := CapabilityFor("mail.send")
	fn, _ := actionFor("mail.send")
	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), map[string]string{
		"to": "a@example.com", "subject": "s", "body": "b"}, e); err == nil {
		t.Fatal("an unwired mail sender reported success")
	}
}
