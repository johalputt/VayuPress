// SPDX-License-Identifier: Apache-2.0

package vayuflow

// audit_adr0151_test.go — the pre-release adversarial pass over ADR-0151.
//
// The ADR pre-declared seven attacks so this pass would start from "what would
// I do to this" rather than from the feature list. Each finding below is
// written in the attacker's voice, with the consequence spelled out, and each
// one FAILED against the code as first written. The attacks that found nothing
// are recorded at the bottom, because a clean result is only evidence if it
// says what was tried.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// ── Attack 5, first variant. The Tor kill-switch has an opt-out. ────────────
//
// Effects.Fetch refused an outbound call only when the CALLING action had
// declared itself OnionInert. The registry test that keeps egress actions inert
// covers KindEgress and nothing else — so a content-kind or model-kind action
// that called e.Fetch was handed the network in a Tor Space, and the only thing
// left between it and the clearnet was safefetch downstream.
//
// The attacker does not need to register anything: they need the NEXT action
// somebody adds to reach the network as an incidental part of doing something
// else, which is how nearly every leak in this codebase's history has looked.
// A guarantee that holds only while every future author remembers a field is
// not a guarantee, and the method is called Fetch — being outbound is not a
// property it has to be told about.
func TestFetchRefusesInATorSpaceWhateverTheActionDeclared(t *testing.T) {
	orig := clearnetBlocked
	clearnetBlocked = func() bool { return true }
	t.Cleanup(func() { clearnetBlocked = orig })

	// A capability that is emphatically NOT inert — the drafting action, which
	// legitimately runs in a Tor Space because writing a local draft touches no
	// network. Now it reaches for one.
	c, err := CapabilityFor("content.draft.create")
	if err != nil {
		t.Fatal(err)
	}
	if c.Onion != OnionActive {
		t.Fatalf("this attack needs an OnionActive capability; %s is %s", c.Action, c.Onion)
	}

	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
	if err := e.Fetch("https://tracker.example/beacon"); err == nil {
		t.Fatal("an action that did not declare itself inert was permitted to reach " +
			"the clearnet from a Tor Space")
	}
	if spend.Egress != 0 {
		t.Errorf("a refused fetch charged %d against the egress ceiling", spend.Egress)
	}
}

// ── Attack 5, second variant. The panel says something the report does not. ──
//
// Flow.NeedsEgress answered "does any step declare OnionInert?", and the panel
// rendered "This flow reaches a remote host" from it. model.draft.generate is
// registered inert — correctly, because a REMOTE provider makes it an outbound
// call — so a flow whose only step generates a draft against a model running on
// this very host was told it reaches a remote host.
//
// flowaudit answers the same question from CapabilitiesOfKind(KindEgress) and
// gets the other answer. Two implementations of "does this reach out", one on
// the page and one in the posture report, disagreeing about the same flow. This
// codebase has a note about exactly that, and the panel is the one that is
// wrong.
func TestNeedsEgressMeansEgressAndNotMerelyInert(t *testing.T) {
	f := goodFlow()
	f.Steps = []Step{{Action: "model.draft.generate", Params: map[string]string{"prompt": "x"}}}
	if f.NeedsEgress() {
		t.Error("a flow whose only step is a model generation is reported as reaching a " +
			"remote host; with a local provider that claim is false, and the posture " +
			"report already disagrees with it")
	}

	// And the honest answer is still available, so the panel can say the true
	// thing rather than nothing.
	if !f.NeedsModel() {
		t.Error("NeedsModel must recognise a model step, or the panel has no way to " +
			"describe what this flow actually does")
	}

	// The real egress action must still be recognised, or this fix has traded a
	// false positive for a false negative — which is the worse of the two.
	g := goodFlow()
	g.Steps = []Step{{Action: "egress.fetch", Params: map[string]string{"url": "https://example.com"}}}
	if !g.NeedsEgress() {
		t.Error("a fetch step is not reported as reaching a remote host")
	}
	if g.NeedsModel() {
		t.Error("a fetch step is reported as a model step")
	}
}

// ── Attack 1, second half. "Does the inbox grow without bound while it does?" ─
//
// The rate ceiling holds and writes no row for a refused storm — that half was
// built and is tested. The other half was not: PruneDrained existed, carried a
// comment reading "The trail is bounded by policy rather than by hope", and was
// called by nothing in the entire binary. Every event this install has ever
// published stays in vayuflow_inbox forever, drained or not.
//
// Ten thousand articles is the ADR's own number for this attack. Ten thousand
// rows nobody deletes is not a crash; it is a table that is still there in a
// year, on an install whose operator was told it was bounded.
func TestADrainPassAlsoForgetsWhatItHasAlreadyDrained(t *testing.T) {
	ib, _, _, dr := newInboxRig(t, RoleAdmin)
	ctx := context.Background()

	// A row drained long ago, and one drained a moment ago. Only the first is
	// past the retention window.
	old := insertDrainedAt(t, ib, "article.created", "stale-1", -40*24*time.Hour)
	recent := insertDrainedAt(t, ib, "article.created", "fresh-1", -1*time.Minute)

	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if rowExists(t, ib, old) {
		t.Error("a row drained 40 days ago is still in the inbox after a drain pass; " +
			"nothing in this binary ever calls PruneDrained, so the table grows forever")
	}
	if !rowExists(t, ib, recent) {
		t.Error("a row drained a minute ago was pruned; the retention window is not " +
			"being applied and the trail is being thrown away early")
	}
}

// Pruning on every pass would mean a DELETE against the whole table every five
// seconds for the life of the process. The pass must forget on an interval, not
// on every tick, or the fix for an unbounded table is an unbounded scan.
func TestPruningRunsOnAnIntervalRatherThanEveryPass(t *testing.T) {
	ib, _, _, dr := newInboxRig(t, RoleAdmin)
	ctx := context.Background()
	base := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	dr.now = func() time.Time { return base }

	insertDrainedAt(t, ib, "article.created", "stale-a", -40*24*time.Hour)
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}

	// A second stale row appears immediately after. The very next pass must NOT
	// prune it — the interval has not elapsed.
	second := insertDrainedAt(t, ib, "article.created", "stale-b", -40*24*time.Hour)
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if !rowExists(t, ib, second) {
		t.Error("two drain passes a moment apart both pruned; the interval is not being honoured")
	}

	// Once the interval has passed, it goes.
	dr.now = func() time.Time { return base.Add(inboxPruneEvery + time.Minute) }
	if _, err := dr.Drain(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if rowExists(t, ib, second) {
		t.Error("the interval elapsed and the stale row survived; pruning never happens again " +
			"after the first pass")
	}
}

// ── Attack 7. The dry-run lie, in the one place it was still being told. ─────
//
// Effects.Model is deliberately NOT gated on dry-run: the ADR requires a dry run
// to call the model for real, because a stubbed generation tells the operator
// nothing about what the live run produces. Effects.Fetch was gated, and the two
// together produce a specific, quiet lie.
//
// Dry-run a fetch → model → draft flow and the fetch is refused, so `$prev` is
// empty, so the model is genuinely called ON NOTHING — and the captured diff
// shows a real generation next to "would fetch: <url>". The operator reads a
// draft that was produced from an empty body and believes they are looking at
// what the live run will write. Under-reporting the fetch and over-reporting the
// generation, in the same run, on the same screen.
//
// A fetch is a read that produces a value, exactly like the model call, and it
// is already charged against the egress ceiling and already refused in a Tor
// Space. So it happens, and the capture says so plainly.
func TestADryRunFetchesForRealSoTheGenerationIsNotFedNothing(t *testing.T) {
	ff, _ := wireReach(t)
	ff.body = "the actual article body"

	c, _ := CapabilityFor("egress.fetch")
	fn, _ := actionFor("egress.fetch")
	var spend Spend
	e := &Effects{mode: RunDryRun, cap: c, budget: testBudget(), spend: &spend}

	out, err := fn(context.Background(), map[string]string{"url": "https://example.com/x"}, e)
	if err != nil && !errors.Is(err, ErrDryRun) {
		t.Fatal(err)
	}
	if out != "the actual article body" {
		t.Errorf("a dry run handed the next step %q; the step after a fetch runs on an "+
			"empty body while the model step beside it runs for real", out)
	}
	if ff.count() != 1 {
		t.Errorf("the guarded client was called %d time(s); a dry run that skips the read "+
			"under-reports what the live run does", ff.count())
	}
	if spend.Egress != 1 {
		t.Errorf("the dry run charged %d egress; it must spend what the live run spends", spend.Egress)
	}
	// And the operator must be told the read really happened — a capture that
	// said "would fetch" while having fetched is the same lie pointing the other
	// way.
	did := strings.Join(e.notes, "; ")
	if !strings.Contains(did, "fetched") {
		t.Errorf("the dry-run capture does not record that the fetch was performed: %q", did)
	}
	for _, r := range e.refusals {
		if strings.Contains(r, "would fetch") {
			t.Errorf("the capture still claims the fetch was refused: %q", r)
		}
	}
}

// A dry run still must not WRITE, which is the property the whole mode exists
// for. Making the fetch real must not have moved that line.
func TestADryRunStillRefusesEveryWrite(t *testing.T) {
	for _, action := range []string{"content.draft.create", "content.draft.update", "mail.send"} {
		c, err := CapabilityFor(action)
		if err != nil {
			t.Fatal(err)
		}
		var spend Spend
		e := &Effects{mode: RunDryRun, cap: c, budget: testBudget(), spend: &spend}
		if err := e.Write(c.Writes, "anything at all"); !errors.Is(err, ErrDryRun) {
			t.Errorf("%s: a dry-run write returned %v, not a capture", action, err)
		}
		if spend.Writes != 1 {
			t.Errorf("%s: a dry-run write charged %d; it must spend what the live run spends",
				action, spend.Writes)
		}
	}
}

// ── Attacks that found nothing, recorded because trying and finding nothing is
// a different thing from not looking. ───────────────────────────────────────
//
//   - Attack 2, budget bypass via step expansion. There is no expansion to
//     exploit: Steps is a fixed ordered list settled at save time, chargeStep
//     runs before every step, and Complete() refuses a flow whose step count
//     exceeds its own MaxStepsPerRun. A model returning a list returns a
//     STRING; nothing iterates it. The write and egress ceilings are charged
//     inside Effects, so an action calling Write in a loop is bounded by the
//     ledger rather than by the action's own restraint. Confirmed below.
//   - Attack 3, authority outliving the grant. The owner's role is not stored
//     on the flow at all — resolved on every run, and a resolver that errors is
//     read as no role rather than as the last known one. Covered by the P1/P2
//     suite; re-attacked here from the demotion side.
//   - Attack 4, injection through content. There is no edge for injected text
//     to take: Step has no branch target, prev is a single value, and
//     substitution only replaces a parameter whose WHOLE value is the
//     placeholder — so content cannot append to a URL or splice a second
//     recipient into a mail step. Confirmed below.
//   - Attack 6, idempotency under redelivery. The key is claimed in Begin,
//     before any step runs, and derives from the inbox row id. Covered by the
//     P2 and P5 suites.

func TestAnActionCannotOutspendTheWriteCeilingByLooping(t *testing.T) {
	c, _ := CapabilityFor("content.draft.create")
	b := testBudget()
	b.MaxWritesPerRun = 2
	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: b, spend: &spend}

	var refusedAt int
	for i := 1; i <= 10; i++ {
		if err := e.Write(WriteDraft, "draft"); err != nil {
			refusedAt = i
			break
		}
	}
	if refusedAt != 3 {
		t.Fatalf("a single action wrote past its ceiling; refused at attempt %d, expected 3", refusedAt)
	}
	if spend.Writes != 2 {
		t.Errorf("the ledger recorded %d writes against a ceiling of 2", spend.Writes)
	}
}

func TestInjectedContentCannotSpliceASecondRecipientOrRedirectAFetch(t *testing.T) {
	// The attacker controls a comment body, which becomes prev.
	hostile := "victim@example.com, everyone@example.com"
	params := map[string]string{"to": "$prev", "subject": "hi", "body": "b"}
	got := substitutePrev(params, hostile)
	if got["to"] != hostile {
		t.Fatalf("substitution mangled the value: %q", got["to"])
	}
	// Substitution is whole-value only, so the attacker cannot append to a
	// parameter the operator wrote.
	partial := substitutePrev(map[string]string{"url": "https://example.com/$prev"}, "@evil.example")
	if partial["url"] != "https://example.com/$prev" {
		t.Errorf("content was spliced into the middle of an operator-written parameter: %q", partial["url"])
	}
	// And the fan-out the whole-value case would have bought is refused by the
	// action itself.
	_, _ = wireReach(t)
	c, _ := CapabilityFor("mail.send")
	fn, _ := actionFor("mail.send")
	var spend Spend
	e := &Effects{mode: RunLive, cap: c, budget: testBudget(), spend: &spend}
	if _, err := fn(context.Background(), got, e); err == nil {
		t.Error("a comment chose two recipients and the mail step accepted them")
	}
}

func TestADemotedOwnerStopsTheFlowEvenThoughItWasArmedByAnAdmin(t *testing.T) {
	db := newTestDB(t)
	fs, rs := NewStore(db), NewRunStore(db)
	role := RoleAdmin
	rn := NewRunner(fs, rs, func(context.Context, string) (string, error) { return role, nil })
	wireContent(t)

	f := goodFlow()
	f.Enabled, f.Mode = true, RunLive
	f.Steps = []Step{{Action: "mail.send", Params: map[string]string{
		"to": "a@example.com", "subject": "s", "body": "b"}}}
	if err := fs.Save(context.Background(), &f); err != nil {
		t.Fatal(err)
	}

	// Armed as admin, which mail.send requires.
	role = RoleEditor // ...and then demoted, without anyone touching the flow.
	run, err := rn.Execute(context.Background(), f, "manual", "demote-1", Subject{})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != StatusRefused {
		t.Fatalf("a demoted owner's flow ran anyway: status %s", run.Status)
	}
	if !strings.Contains(run.Error, "no longer holds") {
		t.Errorf("the refusal does not say why: %q", run.Error)
	}
}

// ── helpers used only by this file ───────────────────────────────────────────

// insertDrainedAt writes a row that was drained `ago` before now, so retention
// can be exercised without waiting thirty days for it.
func insertDrainedAt(t *testing.T, ib *Inbox, event, eventID string, ago time.Duration) int64 {
	t.Helper()
	ts := time.Now().UTC().Add(ago).Format(tsLayout)
	res, err := ib.db.Exec(
		`INSERT INTO vayuflow_inbox(event_name,event_id,subject_json,created_at,drained_at) VALUES(?,?,?,?,?)`,
		event, eventID, "{}", ts, ts)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func rowExists(t *testing.T, ib *Inbox, id int64) bool {
	t.Helper()
	var n int
	if err := ib.db.QueryRow(`SELECT COUNT(*) FROM vayuflow_inbox WHERE id=?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}
