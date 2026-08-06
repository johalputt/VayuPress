// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/vayuveil"
	"github.com/johalputt/vayupress/internal/veilaudit"
)

// mcp_veil.go — VayuVeil over MCP, READ ONLY.
//
// # Why this exists
//
// VayuVeil's entire product is a posture report, and until now that report
// existed in exactly one place: a browser page. VayuShield has four MCP tools;
// VayuVeil had none. So the one thing an operator is told to do — read the state
// rather than reason about what it must be — was the one thing they could not do
// without opening the panel, and neither could any other MCP client.
//
// # Why every tool here is read-only, and why that is not timidity
//
// The same argument mcp_shield.go makes, with one addition specific to this
// subsystem. A model's context holds text from wherever it has been reading, so
// a write tool turns any of that text into a potential instruction. Here the
// write that would matter is REQUESTING HARDENING: it makes root edit a systemd
// unit and restart a live service. A comment on a blog post must never be able to
// bounce someone's mail server, and no permission check makes that safe enough to
// be worth having. Requesting hardening stays a human pressing a button in
// VayuOS, where they have read what it costs.
//
// # What this refuses to let a reader conclude
//
// The report's honesty is structural, and flattening it into JSON is precisely
// where it would be lost. So the payload carries the framing with it: `pass`
// means VERIFIED ENFORCING and nothing else, the permanent rows are flagged as
// permanent rather than left to look like ordinary failures, and `scope` states
// in words that the green rows are process-scoped. A caller that reads only the
// summary counts still cannot come away believing this install defends a screen.

// registerVeilTools adds the read-only VayuVeil surface.
//
// Scoped to settings/read, matching VayuShield: the observation contract and the
// unit directives ARE configuration, and the canonical section vocabulary is a
// fixed twelve that the admin permission grid, the VCB validator and the API docs
// all read from. A thirteenth section to hang two read tools off would change
// three unrelated surfaces to express something settings/read already says.
func (a *App) registerVeilTools(srv *mcp.Server) {
	read := a.mcpVisible(apikeys.SectionSettings, apikeys.ActionRead)

	srv.Register(mcp.Tool{
		Name: "vayuveil_posture",
		Description: "Return the VayuVeil observation-posture report: every row with a " +
			"pass/warn/info/fail/unverified verdict and its reasoning, computed from what was read " +
			"back from the kernel rather than from which switches are on. 'pass' means VERIFIED " +
			"ENFORCING and is process-scoped — it never means this host's screen, keyboard or " +
			"clipboard are defended, which a server cannot do. Includes the permanent rows that no " +
			"configuration clears. Read-only.",
		InputSchema: objSchema(nil, nil),
		Visible:     read,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			enabled := a.veilEnabledCtx(ctx)
			var obs map[vayuveil.ChannelID]vayuveil.Observation
			var red []vayuveil.AttackResult
			var suiteAt time.Time
			if enabled {
				// Through the SAME cache the panel uses. The suite opens device
				// nodes, and a tool a model can call in a loop is precisely where
				// an unmetered one becomes a compute sink.
				var fresh bool
				obs, red, suiteAt, fresh = veilSuite.Get()
				if fresh {
					// Recorded here too, which it was not before. A real capture
					// discovered through the connector used to be found and
					// thrown away — a trail with a hole in it that nothing
					// revealed. Only on a genuine run, because the audit log is
					// WORM and a per-call row is a table nobody can clean up.
					recordVeilFindings(mcpActor(ctx), red)
				}
			}
			sandbox := vayuveil.ReadSandbox()
			harden := readVeilHardenState()
			checks := veilaudit.Run(veilaudit.Inputs{
				Enabled:       enabled,
				Channels:      vayuveil.Channels(),
				Observations:  obs,
				Enforced:      map[vayuveil.Needs]bool{},
				SelfHardening: vayuveil.VerifyProcessHardening(),
				RedTeam:       red,
				Sandbox:       sandbox,
				Host:          vayuveil.ReadHostPosture(),
				Harden:        harden,
				ProcessStart:  bootTime,
			})

			rows := make([]map[string]any, 0, len(checks))
			for _, c := range checks {
				rows = append(rows, map[string]any{
					"title": c.Title, "status": veilStatusWord(c.Status), "detail": c.Detail,
					// Carried per row rather than inferred from the title. A
					// permanent row is not a fault an operator can act on, and a
					// caller alerting on the raw fail count without it would page
					// somebody forever about a limit that is by construction.
					"permanent": c.Permanent,
				})
			}
			pass, warn, fail, unverified := veilaudit.Summary(checks)
			return jsonStr(map[string]any{
				"enabled": enabled,
				"rows":    rows,
				"summary": map[string]any{
					"pass": pass, "warn": warn, "fail": fail, "unverified": unverified,
				},
				// How old the capture result is. The control rows are read from
				// the kernel on every call and are not cached; this one is
				// metered, and a payload that did not say so would present a
				// minute-old sweep as this instant.
				"capture_suite_age": veilSuiteAge(suiteAt, time.Now().UTC()),
				// The sentence that keeps the numbers above honest, shipped WITH
				// them rather than in documentation a caller will not read.
				"scope": "Every green row is process-scoped: it covers the VayuPress process and " +
					"nothing else on this machine. No observation channel is enforced host-wide by " +
					"this install and none can be — that needs a compositor, a sandbox policy and " +
					"mandatory access control, which a web server does not have. 'unverified' means " +
					"the answer could not be read and is never a pass.",
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		// NAMED FOR WHAT IT RETURNS, not for what it is about. The first name was
		// "vayuveil_hardening", and the read-only guard in this package's tests
		// refused it — correctly. "Hardening" is a verb as readily as a noun, and a
		// model scanning a tool list for something that hardens a server would find
		// it. On a surface whose entire safety argument is that nothing here acts,
		// a name that reads as an action is a defect, not a nitpick.
		Name: "vayuveil_unit_controls",
		Description: "Return which service-unit hardening directives are verified in force for this " +
			"process, read back from the kernel, and the state of any drop-in a previous request " +
			"wrote. Distinguishes a directive that is IN FORCE from one merely WRITTEN — a drop-in " +
			"takes effect only when the service restarts into it. Read-only: this server cannot " +
			"request hardening, because that makes root edit a unit and restart a live service.",
		InputSchema: objSchema(nil, nil),
		Visible:     read,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			sandbox := vayuveil.ReadSandbox()
			harden := readVeilHardenState()
			verdict := vayuveil.ReconcileHardening(harden, sandbox, bootTime)
			missing := vayuveil.UnverifiedHardening(sandbox)

			directives := make([]map[string]any, 0, 5)
			for _, d := range vayuveil.HardenBaseline() {
				on, known := d.InForce(sandbox)
				directives = append(directives, map[string]any{
					"directive": d.Directive, "denies": d.Denies, "read_back_from": d.ReadBack,
					// Two fields, never one. "not in force" and "could not be
					// read" are different facts and a single boolean would round
					// the second into the first — which is the direction that
					// turns an unreadable answer into a comfortable one.
					"in_force": on, "known": known,
				})
			}
			refusals := make([]map[string]string, 0, 5)
			for _, r := range vayuveil.HardenRefusals() {
				refusals = append(refusals, map[string]string{"directive": r.Directive, "reason": r.Reason})
			}

			return jsonStr(map[string]any{
				"verdict":          veilHardenVerdictWord(verdict),
				"explanation":      vayuveil.DescribeHardenVerdict(verdict, missing),
				"worker_installed": harden.Installed,
				"drop_in_present":  harden.DropInPresent,
				"last_run": map[string]any{
					"reported": harden.HaveResult,
					"wrote":    harden.Wrote,
					"skipped":  harden.Skipped,
					"reverted": harden.Reverted,
					"failed":   harden.Failed,
					"detail":   harden.Detail,
				},
				"baseline": directives,
				// Shipped alongside what IS written, because a list of controls
				// with no account of what was declined reads as the whole story.
				"refused": refusals,
			}), nil
		},
	})
}

// veilStatusWord renders a verdict for a machine reader.
//
// Spelled out rather than reusing the panel's chip text: the page says
// "enforcing" and "exposed", which are right for a person scanning a column and
// wrong in a payload, where a caller wants the vocabulary the report is defined
// in. "unverified" in particular must survive verbatim — it is the one value
// whose whole purpose is to not be mistaken for either of its neighbours.
func veilStatusWord(s veilaudit.Status) string {
	switch s {
	case veilaudit.Pass:
		return "pass"
	case veilaudit.Warn:
		return "warn"
	case veilaudit.Info:
		return "info"
	case veilaudit.Fail:
		return "fail"
	case veilaudit.Unverified:
		return "unverified"
	}
	return "unverified"
}

// veilHardenVerdictWord renders the hardening verdict as a stable token.
//
// The default is "unknown", not "in_force". A verdict this function does not
// recognise is one added since it was written, and defaulting an unrecognised
// state to the reassuring end is how a new failure mode ships reported as fine.
func veilHardenVerdictWord(v vayuveil.HardenVerdict) string {
	switch v {
	case vayuveil.HardenInForce:
		return "in_force"
	case vayuveil.HardenPending:
		return "requested"
	case vayuveil.HardenAwaitingRestart:
		return "awaiting_restart"
	case vayuveil.HardenDidNotTake:
		return "did_not_take"
	case vayuveil.HardenSkipped:
		return "partly_skipped"
	case vayuveil.HardenReverted:
		return "reverted"
	case vayuveil.HardenFailed:
		return "failed"
	case vayuveil.HardenNotRequested:
		return "not_requested"
	}
	return "unknown"
}
