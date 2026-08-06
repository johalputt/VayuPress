// SPDX-License-Identifier: Apache-2.0

package main

// mcp_veil_test.go — the VayuVeil MCP surface.
//
// The failure mode this file exists to catch is not a broken handler. It is a
// payload that is individually true and collectively misleading: counts without
// the scope sentence, a permanent limit indistinguishable from a fixable one, an
// unreadable answer flattened into a comfortable boolean. The page spent a lot of
// care refusing to read as a shield, and JSON is exactly where that care gets
// dropped on the way out.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/vayuveil"
	"github.com/johalputt/vayupress/internal/veilaudit"
)

func veilToolNames(t *testing.T) map[string]mcp.Tool {
	t.Helper()
	srv := mcp.NewServer("test", "0")
	(&App{}).registerVeilTools(srv)
	out := map[string]mcp.Tool{}
	for _, tl := range srv.Tools() {
		out[tl.Name] = tl
	}
	return out
}

// THE test. Nothing on this surface may write, and the reason is specific rather
// than a general preference: the write that would matter makes root edit a
// systemd unit and restart a live service, and a model's context is full of text
// other people wrote.
func TestTheVeilMCPSurfaceCannotRequestHardening(t *testing.T) {
	for name, tl := range veilToolNames(t) {
		if !strings.HasPrefix(name, "vayuveil_") {
			continue
		}
		// A read tool takes no input. Anything that accepts a parameter is doing
		// something with it, which is the shape a write arrives in.
		props, _ := tl.InputSchema["properties"].(map[string]any)
		if len(props) != 0 {
			t.Errorf("%s accepts input; every tool on this surface is read-only", name)
		}
		for _, verb := range []string{"harden", "request", "apply", "set", "write", "enable"} {
			if strings.HasPrefix(name, "vayuveil_"+verb) {
				t.Errorf("%s looks like a write tool on a read-only surface", name)
			}
		}
	}
	if _, exists := veilToolNames(t)["vayuveil_harden"]; exists {
		t.Fatal("a hardening-request tool exists; that makes root restart a live service on a model's say-so")
	}
}

// Both tools exist and are described. A surface whose description does not warn
// about what `pass` means is one whose numbers get quoted without it.
func TestBothVeilToolsAreRegisteredAndSayWhatPassMeans(t *testing.T) {
	tools := veilToolNames(t)
	for _, want := range []string{"vayuveil_posture", "vayuveil_unit_controls"} {
		tl, ok := tools[want]
		if !ok {
			t.Fatalf("%s is not registered", want)
		}
		if tl.Description == "" {
			t.Fatalf("%s has no description", want)
		}
		if tl.Handler == nil {
			t.Fatalf("%s has no handler", want)
		}
	}
	if !strings.Contains(tools["vayuveil_posture"].Description, "VERIFIED ENFORCING") {
		t.Error("the posture tool's description does not say what pass means")
	}
	if !strings.Contains(tools["vayuveil_unit_controls"].Description, "IN FORCE") {
		t.Error("the hardening tool's description does not draw the written/in-force distinction")
	}
}

// Every audit status must map to a distinct word, and unverified must survive
// verbatim. It is the one value whose whole job is not to be mistaken for either
// of its neighbours.
func TestEveryStatusMapsToADistinctWordAndUnverifiedSurvives(t *testing.T) {
	seen := map[string]veilaudit.Status{}
	for _, s := range []veilaudit.Status{
		veilaudit.Pass, veilaudit.Warn, veilaudit.Info, veilaudit.Fail, veilaudit.Unverified,
	} {
		w := veilStatusWord(s)
		if prev, dup := seen[w]; dup {
			t.Errorf("statuses %v and %v both render as %q", prev, s, w)
		}
		seen[w] = s
	}
	if veilStatusWord(veilaudit.Unverified) != "unverified" {
		t.Error("unverified must render as itself")
	}
	// An unrecognised status must NOT land on "pass". A value added later and
	// defaulted to the reassuring end is how a new failure ships reported as fine.
	if got := veilStatusWord(veilaudit.Status(99)); got == "pass" {
		t.Fatalf("an unrecognised status rendered as %q", got)
	}
}

// The same rule for the hardening verdict, and it is the sharper case: the
// default must not be the one that means everything is fine.
func TestAnUnrecognisedHardeningVerdictIsNotReportedAsInForce(t *testing.T) {
	if got := veilHardenVerdictWord(vayuveil.HardenVerdict(99)); got != "unknown" {
		t.Fatalf("an unrecognised verdict rendered as %q; it must be unknown", got)
	}
	seen := map[string]bool{}
	for _, v := range []vayuveil.HardenVerdict{
		vayuveil.HardenInForce, vayuveil.HardenPending, vayuveil.HardenAwaitingRestart,
		vayuveil.HardenDidNotTake, vayuveil.HardenSkipped, vayuveil.HardenReverted,
		vayuveil.HardenFailed, vayuveil.HardenNotRequested,
	} {
		w := veilHardenVerdictWord(v)
		if w == "unknown" {
			t.Errorf("verdict %v has no word of its own", v)
		}
		if seen[w] {
			t.Errorf("two verdicts render as %q", w)
		}
		seen[w] = true
	}
}

// The payload carries its own framing. A caller that reads only the summary
// counts must still be unable to conclude that this install defends a screen.
func TestThePosturePayloadShipsTheScopeSentenceWithTheNumbers(t *testing.T) {
	tl := veilToolNames(t)["vayuveil_posture"]
	// a.siteSettings is nil on this zero App, so reporting reads as OFF — which is
	// the case worth testing: even the smallest payload must carry the framing.
	out, err := tl.Handler(t.Context(), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("the payload is not valid JSON: %v", err)
	}
	scope, _ := got["scope"].(string)
	if scope == "" {
		t.Fatal("the payload carries no scope sentence")
	}
	for _, required := range []string{"process-scoped", "none can be", "never a pass"} {
		if !strings.Contains(scope, required) {
			t.Errorf("the scope sentence omits %q", required)
		}
	}
	if _, ok := got["summary"]; !ok {
		t.Error("no summary counts")
	}
	if _, ok := got["enabled"]; !ok {
		t.Error("a caller cannot tell whether reporting is even switched on")
	}
}

// A permanent limit must be distinguishable from a fixable failure. Without the
// flag, a caller alerting on the fail count pages somebody forever about a row
// that is there by construction.
func TestPermanentRowsAreFlaggedAsSuchInThePayload(t *testing.T) {
	// Reporting off yields the single info row, so drive the audit directly to get
	// the permanent ones — the payload's shape is what is under test, not this
	// App's state.
	checks := veilaudit.Run(veilaudit.Inputs{
		Enabled: true, Channels: vayuveil.Channels(),
		Observations: map[vayuveil.ChannelID]vayuveil.Observation{},
		Enforced:     map[vayuveil.Needs]bool{},
	})
	permanent := 0
	for _, c := range checks {
		if c.Permanent {
			permanent++
		}
	}
	if permanent == 0 {
		t.Fatal("the audit produced no permanent rows; this guard is blind")
	}

	tl := veilToolNames(t)["vayuveil_posture"]
	out, err := tl.Handler(t.Context(), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got struct {
		Rows []struct {
			Title     string `json:"title"`
			Permanent bool   `json:"permanent"`
		} `json:"rows"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Rows) == 0 {
		t.Fatal("no rows in the payload")
	}
	// Every row carries the key, whatever its value — a field present only when
	// true reads as absent-means-false to some clients and missing to others.
	if !strings.Contains(out, `"permanent"`) {
		t.Error("rows do not carry a permanent flag at all")
	}
}

// The hardening payload keeps "not in force" and "could not be read" apart. One
// boolean would round the second into the first, which is the direction that
// turns an unreadable answer into a comfortable one.
func TestTheHardeningPayloadKeepsUnknownApartFromOff(t *testing.T) {
	tl := veilToolNames(t)["vayuveil_unit_controls"]
	out, err := tl.Handler(t.Context(), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got struct {
		Verdict  string `json:"verdict"`
		Baseline []struct {
			Directive string `json:"directive"`
			InForce   bool   `json:"in_force"`
			Known     bool   `json:"known"`
			ReadBack  string `json:"read_back_from"`
		} `json:"baseline"`
		Refused []struct {
			Directive string `json:"directive"`
			Reason    string `json:"reason"`
		} `json:"refused"`
		Explanation string `json:"explanation"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Baseline) != len(vayuveil.HardenBaseline()) {
		t.Fatalf("the payload carries %d directives, the baseline has %d",
			len(got.Baseline), len(vayuveil.HardenBaseline()))
	}
	if !strings.Contains(out, `"known"`) {
		t.Fatal("the payload has no known bit, so an unreadable answer is indistinguishable from a no")
	}
	for _, d := range got.Baseline {
		if d.ReadBack == "" {
			t.Errorf("%s is reported without saying how it was verified", d.Directive)
		}
	}
	// The refusals ship too. A list of controls with no account of what was
	// declined reads as the whole story.
	if len(got.Refused) == 0 {
		t.Error("the payload lists what is written and not what was refused")
	}
	if got.Explanation == "" {
		t.Error("the verdict arrives without its explanation")
	}
	if got.Verdict == "" {
		t.Error("no verdict")
	}
}

// AUDIT FINDING — the payload must say how old the capture result is.
//
// The capture suite is metered: it opens device nodes, so it runs at most once
// per interval for every caller. That is defensible only because it is an
// experiment rather than a control — but a payload that presented a minute-old
// sweep as this instant would be the same defect as remembering a control and
// calling it verified.
func TestThePosturePayloadSaysHowOldTheCaptureResultIs(t *testing.T) {
	tl := veilToolNames(t)["vayuveil_posture"]
	out, err := tl.Handler(t.Context(), nil)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	age, _ := got["capture_suite_age"].(string)
	if age == "" {
		t.Fatal("the payload does not say when the capture suite last ran")
	}
	// And it must not let a reader think the control rows are cached too — they
	// are not, and conflating the two understates what the report verified.
	if !strings.Contains(age, "not run") && !strings.Contains(age, "not cached") &&
		!strings.Contains(age, "just now") {
		t.Errorf("the age string does not distinguish the metered suite from the uncached controls: %q", age)
	}
}
