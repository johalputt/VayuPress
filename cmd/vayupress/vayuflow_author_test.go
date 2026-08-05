// SPDX-License-Identifier: Apache-2.0

package main

// vayuflow_author_test.go — the handler half of the authoring surface.
//
// The engine half is covered in internal/vayuflow. What is pinned here is the
// mapping, which is where the two decisions that matter live: a save must not
// arm, and an edit must not re-point the owner. Both are one forgotten line
// away from being wrong, and neither would fail any other test.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/users"
	"github.com/johalputt/vayupress/internal/vayuflow"
)

// signedIn returns a request carrying an operator identity, the way the session
// middleware leaves it.
func signedIn(t *testing.T, id string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/os/api/vayuflow/save", nil)
	u := &users.User{ID: id, Role: users.RoleAdmin}
	return r.WithContext(context.WithValue(r.Context(), ctxUserKey, u))
}

func minimalInput() flowSaveInput {
	var in flowSaveInput
	in.Name = "Monday digest"
	in.Trigger.Kind = "manual"
	in.Condition.Kind = "always"
	in.Steps = []flowStepInput{{Action: "content.draft.create",
		Params: map[string]string{"title": "Digest", "slug": "digest"}}}
	in.Budget.MaxStepsPerRun = 4
	in.Budget.MaxRunsPerHour = 2
	in.Budget.MaxWritesPerRun = 1
	in.Budget.MaxEgressPerRun = 1
	in.Budget.TimeoutSeconds = 60
	return in
}

// A new flow is stored switched OFF and in DRY-RUN, whatever the form wanted.
// Anything else means creating an automation is the same act as arming one, and
// the operator never got to look at what it does first.
func TestANewFlowIsCreatedOffAndInDryRun(t *testing.T) {
	a := &App{}
	f, err := a.flowFromInput(signedIn(t, "user-7"), minimalInput())
	if err != nil {
		t.Fatal(err)
	}
	if f.Enabled {
		t.Error("a newly created flow is switched on")
	}
	if f.Mode != vayuflow.RunDryRun {
		t.Errorf("a newly created flow is in %s, not dry-run", f.Mode)
	}
	if f.Owner != "user-7" {
		t.Errorf("owner is %q, not the operator who created it", f.Owner)
	}
	// And it is a flow the store will accept, or the form would fail on save
	// for a reason this mapping could have prevented.
	if err := f.Complete(); err != nil {
		t.Errorf("the mapped flow is not storable: %v", err)
	}
}

// THE one that cannot be got wrong. A flow borrows its owner's authority, and
// that authority is re-read on every run — so if an edit adopted whoever
// pressed Save, a demoted owner could restore their flow's reach by editing its
// name, and the run-time check that ADR-0151 §4 exists for would mean nothing.
func TestAnEditNeverRePointsTheOwnerOrArmsTheFlow(t *testing.T) {
	db := newFlowTestDB(t)
	a := &App{flowStore: vayuflow.NewStore(db)}

	// A flow owned by someone else, switched on. It stays in dry-run: an armed
	// LIVE flow cannot be edited at all — see
	// TestEditingAnArmedLiveFlowIsRefusedTheWayDeletingOneIs — so enabled plus
	// dry-run is the state that exercises the carry-across.
	stored, err := a.flowFromInput(signedIn(t, "original-owner"), minimalInput())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.flowStore.Save(context.Background(), &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := a.flowStore.SetEnabled(context.Background(), stored.ID, true); err != nil {
		t.Fatal(err)
	}
	// A DIFFERENT operator edits it.
	edit := minimalInput()
	edit.ID = stored.ID
	edit.Name = "Renamed by somebody else"
	got, err := a.flowFromInput(signedIn(t, "somebody-else"), edit)
	if err != nil {
		t.Fatal(err)
	}

	if got.Owner != "original-owner" {
		t.Errorf("editing re-pointed the owner to %q; a demoted owner could restore their flow's "+
			"authority by renaming it", got.Owner)
	}
	if !got.Enabled {
		t.Error("editing switched the flow off")
	}
	if got.Mode != vayuflow.RunDryRun {
		t.Errorf("editing changed the mode to %s; arming is its own decision with its own "+
			"audit entry", got.Mode)
	}
	if got.Name != "Renamed by somebody else" {
		t.Errorf("the edit did not apply: name is %q", got.Name)
	}
}

// A request with no operator identity cannot create a flow. An owner that does
// not resolve to an account makes the run-time authority check fail closed
// forever — the flow would save cleanly and then refuse every time it fired,
// for a reason nothing on the page could explain.
func TestAFlowCannotBeCreatedWithoutAnIdentifiableOwner(t *testing.T) {
	a := &App{}
	r := httptest.NewRequest(http.MethodPost, "/os/api/vayuflow/save", nil) // no user in context
	_, err := a.flowFromInput(r, minimalInput())
	if err == nil {
		t.Fatal("a flow was created with no owner")
	}
	if !strings.Contains(err.Error(), "owner") {
		t.Errorf("the refusal does not say why: %q", err)
	}
}

// The wire struct has no mode or enabled field, and unknown fields are refused
// rather than ignored. A form that sends `mode` and is silently obeyed-but-
// ignored teaches an operator the field works.
func TestTheSaveDocumentRefusesToCarryArmingFields(t *testing.T) {
	for _, body := range []string{
		`{"name":"x","mode":"live"}`,
		`{"name":"x","enabled":true}`,
		`{"name":"x","owner":"someone-else"}`,
	} {
		var in flowSaveInput
		dec := json.NewDecoder(strings.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&in); err == nil {
			t.Errorf("%s was accepted; arming and ownership must not travel on a save", body)
		}
	}
	// The legitimate document still decodes, or the guard has broken saving.
	var ok flowSaveInput
	dec := json.NewDecoder(strings.NewReader(
		`{"id":"","name":"x","trigger":{"kind":"manual","cron":"","event":""},` +
			`"condition":{"kind":"always","value":""},"steps":[],` +
			`"budget":{"max_steps_per_run":1,"max_runs_per_hour":1,"max_writes_per_run":1,` +
			`"max_egress_per_run":1,"timeout_seconds":30}}`))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ok); err != nil {
		t.Fatalf("a valid document was refused: %v", err)
	}
}

// An unrecognised on= value changes nothing. "off" is not a safe default when
// the operator may have meant on.
func TestEnableRefusesAValueItDoesNotRecognise(t *testing.T) {
	for _, good := range []string{"true", "1", "on", "yes", "FALSE", "0", "off", "no"} {
		if _, ok := flowEnableFor(good); !ok {
			t.Errorf("%q is a recognisable answer and was refused", good)
		}
	}
	for _, bad := range []string{"", "maybe", "enabled", "2"} {
		if v, ok := flowEnableFor(bad); ok {
			t.Errorf("%q was accepted and read as %v", bad, v)
		}
	}
}

// Blank parameter values are dropped rather than stored as empty strings. An
// action that checks `params["url"] == ""` and one that checks presence would
// otherwise disagree about a field the operator simply left alone.
func TestBlankParametersAreDroppedNotStoredEmpty(t *testing.T) {
	a := &App{}
	in := minimalInput()
	in.Steps = []flowStepInput{{Action: "content.draft.create",
		Params: map[string]string{"title": "Digest", "slug": "  ", "extra": ""}}}
	f, err := a.flowFromInput(signedIn(t, "user-1"), in)
	if err != nil {
		t.Fatal(err)
	}
	p := f.Steps[0].Params
	if _, present := p["slug"]; present {
		t.Error("a whitespace-only parameter was stored")
	}
	if _, present := p["extra"]; present {
		t.Error("an empty parameter was stored")
	}
	if p["title"] != "Digest" {
		t.Errorf("a real parameter was lost: %v", p)
	}
}

// ── The form ────────────────────────────────────────────────────────────────

// Every action the form offers must exist in the registry, and every registered
// action must be offered. A form with its own list is a list that drifts, and
// the operator finds out by saving something the store refuses for a reason the
// page never mentioned.
func TestTheFormOffersExactlyTheRegisteredActions(t *testing.T) {
	card := flowEditorCard()
	for _, c := range vayuflow.Capabilities() {
		if !strings.Contains(card, `value="`+c.Action+`"`) {
			t.Errorf("the form does not offer the registered action %q", c.Action)
		}
	}
	// And nothing invented. Count the option values inside the first step's
	// select rather than searching the whole card, so a stray match elsewhere
	// cannot make this pass.
	sel := flowFormSlice(t, card, `<select class="input" id="ff-act1">`, `</select>`)
	got := strings.Count(sel, `<option value="`) - 1 // the leading "—" placeholder
	if got != len(vayuflow.Capabilities()) {
		t.Errorf("step 1 offers %d actions, the registry has %d",
			got, len(vayuflow.Capabilities()))
	}
}

func TestTheFormOffersExactlyTheEnginesConditionsAndEvents(t *testing.T) {
	card := flowEditorCard()
	condSel := flowFormSlice(t, card, `<select class="input" id="ff-cond">`, `</select>`)
	for _, n := range vayuflow.LeafConditionNames() {
		if !strings.Contains(condSel, `value="`+n+`"`) {
			t.Errorf("the form does not offer the condition %q", n)
		}
	}
	if n := strings.Count(condSel, `<option value="`); n != len(vayuflow.LeafConditionNames()) {
		t.Errorf("the condition list offers %d, the engine has %d", n, len(vayuflow.LeafConditionNames()))
	}

	evSel := flowFormSlice(t, card, `<select class="input" id="ff-event">`, `</select>`)
	for _, e := range vayuflow.KnownEvents() {
		if !strings.Contains(evSel, `value="`+e+`"`) {
			t.Errorf("the form does not offer the event %q", e)
		}
	}
}

// No budget field may arrive blank. ADR-0151 §3 makes "unlimited"
// inexpressible; a form defaulting a ceiling to empty would put it back as a
// shrug, and the first save would fail on a field the operator never saw.
func TestEveryBudgetFieldShipsWithARealNumber(t *testing.T) {
	card := flowEditorCard()
	for _, id := range []string{"ff-bsteps", "ff-bruns", "ff-bwrites", "ff-begress", "ff-btimeout"} {
		field := flowFormSlice(t, card, `id="`+id+`"`, `>`)
		if !strings.Contains(field, `value="`) {
			t.Errorf("%s has no default; an empty ceiling is 'unlimited' arriving by the back door", id)
		}
		if strings.Contains(field, `value=""`) {
			t.Errorf("%s defaults to blank", id)
		}
		if !strings.Contains(field, `min="1"`) {
			t.Errorf("%s accepts zero, which Budget.Complete refuses — the form should say so first", id)
		}
	}
}

// flowFormSlice returns the ONE substring between two markers, failing if the
// opening marker does not appear exactly once. Searching a whole page for a
// value passes on any page that uses it elsewhere.
func flowFormSlice(t *testing.T, s, open, shut string) string {
	t.Helper()
	if n := strings.Count(s, open); n != 1 {
		t.Fatalf("expected exactly one %q, found %d", open, n)
	}
	rest := s[strings.Index(s, open)+len(open):]
	end := strings.Index(rest, shut)
	if end < 0 {
		t.Fatalf("no %q after %q", shut, open)
	}
	return rest[:end]
}

// newFlowTestDB gives the handler tests a real store to edit against.
//
// It applies the migrations ONE STATEMENT PER PHYSICAL LINE, the way the
// migration runner does. Anything smarter — splitting on semicolons, say —
// would let a migration pass here and fail in production.
func newFlowTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, name := range []string{"085-vayuflow.up.sql", "086-vayuflow-runs.up.sql", "087-vayuflow-inbox.up.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "db", "migrations", name)) // #nosec G304 -- fixed path inside this repository
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			if _, err := db.Exec(line); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		}
	}
	return db
}
