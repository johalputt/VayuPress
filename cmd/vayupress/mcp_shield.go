// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mcp"
	"github.com/johalputt/vayupress/internal/shieldaudit"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// mcp_shield.go — VayuShield over MCP, READ ONLY.
//
// # Why there are no write tools here, and why that is not timidity
//
// An MCP tool is invoked by a model, and a model's context contains text from
// wherever it has been reading: a blog comment, a fetched page, a mail body, a
// pull-request description. A write tool therefore turns any of that text into a
// potential instruction.
//
// For most surfaces that is a manageable risk. For this one it is not. The single
// most dangerous field in the product is the operator ALLOW list, because a
// source on it skips every gate including the jail — so a writable allow list
// means a stranger who can get text in front of a model can add themselves to it
// and walk past the entire shield. Observe-only mode is the same shape from the
// other end: one write turns every defence into a counter.
//
// The answer is not a cleverer permission check. It is that a model gets to
// OBSERVE this system and a human gets to change it. When writes are added they
// will go through a pending-change queue an operator approves in VayuOS, so
// injected text can at worst produce a proposal somebody looks at.
//
// # What these tools deliberately do not disclose
//
// Everything here is aggregate or configuration state. The allow and deny lists
// are reported as COUNTS rather than values: knowing which networks bypass the
// shield is the one piece of shield state with direct offensive value, and least
// disclosure costs nothing here because an operator reading their own panel can
// see the values anyway.
//
// No visitor address, fingerprint or session token is exposed by any tool in this
// file. The history tool returns the same aggregates the panel shows, which are
// already free of anything that identifies a person.

// registerShieldTools adds the read-only VayuShield surface.
//
// Scoped to settings/read rather than a section of its own: the shield's gates,
// thresholds and policy ARE configuration, and the canonical section vocabulary
// is a fixed twelve that the admin permission grid, the VCB validator and the API
// docs all read from. Adding a thirteenth to hang four read tools off would
// change three unrelated surfaces to express something settings/read already says.
func (a *App) registerShieldTools(srv *mcp.Server) {
	read := a.mcpVisible(apikeys.SectionSettings, apikeys.ActionRead)

	srv.Register(mcp.Tool{
		Name: "vayushield_status",
		Description: "Return VayuShield's live state: whether it is protecting, observing or off, " +
			"plus the per-layer counters (in-flight, shed, challenges served and passed, jails, " +
			"reputation suspects, request-inspection findings and cluster peers). Aggregate counts " +
			"only — no visitor is identified. Read-only.",
		InputSchema: objSchema(nil, nil),
		Visible:     read,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if a.vayuShield == nil {
				return "", fmt.Errorf("VayuShield is not initialised")
			}
			st := a.vayuShield.Status()
			cur := a.shieldCurrentSettings()

			state := "protected"
			switch {
			case st.ObserveOnly:
				state = "observing — nothing is enforcing"
			case !cur.Enabled:
				state = "off"
			case st.SurgeActive:
				state = "surge — verifying visitors"
			case st.UnderAttack:
				state = "under attack"
			}
			peers, verdictsIn, refused, pushed, pushFailed := a.vayuShield.ClusterStats()

			out := map[string]any{
				"state":             state,
				"observe_only":      st.ObserveOnly,
				"requests_per_sec":  st.RPS,
				"in_flight":         st.InFlight,
				"sentences_active":  st.Blocklisted + st.RepJailed,
				"blocklisted":       st.Blocklisted,
				"reputation_jailed": st.RepJailed,
				"suspects":          st.Suspects,
				"pardons":           st.Pardons,
				"fair_shed":         st.FairShed,
				"surge_challenges":  st.SurgeChallenges,
				"challenges_served": st.ChallengesServed,
				"challenges_passed": st.ChallengesPassed,
				"calibration_bias":  st.CalibrationBias,
				"inspection": map[string]any{
					"foreign_stack_probes": st.InspectFindings[0],
					"traversal":            st.InspectFindings[1],
					"payload":              st.InspectFindings[2],
					"rules":                st.InspectRules,
					"ruleset_version":      st.InspectRuleset,
				},
				// The observe-mode counters, so "what WOULD each gate have done"
				// is answerable without the panel. Indexed by gate name.
				"would_have": shieldWouldHaveByName(st),
			}
			// Third-party feed matches, reported only when a feed is enabled.
			// Omitting the key on installs that opted into none says "this is not
			// in play here", which two zeroes would not.
			if hostile, datacenter := a.vayuShield.IntelHits(); len(a.shieldIntelStatus()) > 0 {
				out["network_intelligence"] = map[string]any{
					"hostile_matches":    hostile,
					"datacenter_matches": datacenter,
					"feeds":              a.shieldIntelStatus(),
				}
			}
			if peers > 0 {
				out["cluster"] = map[string]any{
					"peers": peers, "verdicts_in": verdictsIn,
					"refused_by_allow_list": refused,
					"pushes_ok":             pushed, "pushes_failed": pushFailed,
				}
			}
			return jsonStr(out), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "vayushield_posture",
		Description: "Return the VayuShield posture report: every control with a pass/warn/info/fail " +
			"verdict and the reasoning, computed from what is actually enforcing rather than from " +
			"which switches are on. Includes the permanent volumetric-absorption failure that no " +
			"configuration can clear. Read-only.",
		InputSchema: objSchema(nil, nil),
		Visible:     read,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			// nil request: shieldAuditInputs guards for it, and the client-IP row
			// then reports from recent visitor traffic rather than from a request
			// that does not exist. That is the right source anyway — deriving it
			// from one request is the defect fixed in 3.16.4.
			checks := shieldaudit.Run(a.shieldAuditInputs(nil))
			rows := make([]map[string]string, 0, len(checks))
			for _, c := range checks {
				rows = append(rows, map[string]string{
					"title": c.Title, "status": c.Status.String(), "detail": c.Detail,
				})
			}
			pass, warn, info, fail := shieldaudit.Summary(checks)
			return jsonStr(map[string]any{
				"rows": rows,
				"summary": map[string]any{
					"pass": pass, "warn": warn, "info": info, "fail": fail,
					// Compare against this, never against zero: a perfectly
					// configured install still carries the volumetric row.
					"baseline_fails":          shieldaudit.BaselineFails,
					"failing_beyond_baseline": fail - shieldaudit.BaselineFails,
				},
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "vayushield_settings",
		Description: "Return which VayuShield gates are enabled and at what thresholds. Operator " +
			"allow/deny network lists are reported as COUNTS, never as values. Read-only — no tool " +
			"in this server can change any of it.",
		InputSchema: objSchema(nil, nil),
		Visible:     read,
		Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if a.vayuShield == nil {
				return "", fmt.Errorf("VayuShield is not initialised")
			}
			cur := a.shieldCurrentSettings()
			pol := a.vayuShield.Policy()
			return jsonStr(map[string]any{
				"enabled":      cur.Enabled,
				"observe_only": cur.ObserveOnly,
				"gates": map[string]bool{
					"rate_limit":   cur.RateLimit,
					"load_shed":    cur.LoadShed,
					"auto_block":   cur.AutoBlock,
					"under_attack": cur.UnderAttack,
					"surge":        cur.Surge,
					"tarpit":       cur.Tarpit,
					"group_ipv4":   cur.GroupIPv4,
					"behind_cdn":   config.TrustCloudflareEnabled(),
				},
				"thresholds": map[string]float64{
					"challenge": cur.PoWThreshold,
					"js_check":  cur.JSThreshold,
					"block":     cur.BlockThreshold,
				},
				"limits": map[string]int{
					"rate_per_minute":  cur.RatePerMinute,
					"burst":            cur.Burst,
					"max_in_flight":    cur.MaxInFlight,
					"jail_minutes":     cur.AutoBlockJailMinutes,
					"under_attack_rps": cur.UnderAttackRPS,
				},
				// Counts, not values. See the file header.
				"policy": map[string]any{
					"allow_networks":      len(cur.Policy.AllowCIDRs),
					"deny_networks":       len(cur.Policy.DenyCIDRs),
					"deny_countries":      cur.Policy.DenyCountries,
					"challenge_countries": cur.Policy.ChallengeCountries,
					"allow_countries":     cur.Policy.AllowCountries,
					"routes":              len(cur.Policy.Routes),
					"geo_active":          pol.GeoActive(),
					"source_rules_active": pol.SourceActive(),
					"parse_errors":        a.vayuShield.PolicyIssues(),
				},
				"tor_inert_gates": vayushield.GatesInertInTorMode(),
				// Which third-party lists the operator opted into, and whether each
				// is actually holding data. The URL is included because "trust this
				// list" is not answerable without knowing whose list it is — and
				// unlike the allow list, a published feed's address has no offensive
				// value: it is a public endpoint anyone can already read.
				"network_intelligence": a.shieldIntelStatus(),
			}), nil
		},
	})

	srv.Register(mcp.Tool{
		Name: "vayushield_history",
		Description: "Return the recorded block/challenge trail over a window: totals, why requests " +
			"were refused, what was being hit, which countries they came from, and the challenge " +
			"pass rate by hour. Aggregates only — no address or fingerprint is returned. Read-only.",
		InputSchema: objSchema(nil, map[string]any{
			"hours": intProp("Look-back window in hours (default 24, max 2160)."),
			"limit": intProp("How many rows per breakdown (default 8, max 50)."),
		}),
		Visible: read,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			if a.vayuShield == nil {
				return "", fmt.Errorf("VayuShield is not initialised")
			}
			store := a.vayuShield.BotStore()
			if store == nil {
				return "", fmt.Errorf("the adaptive database is unavailable, so there is no trail")
			}
			var in struct {
				Hours int `json:"hours"`
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Hours <= 0 {
				in.Hours = 24
			}
			if in.Hours > 24*90 {
				in.Hours = 24 * 90
			}
			if in.Limit <= 0 {
				in.Limit = 8
			}
			if in.Limit > 50 {
				in.Limit = 50
			}
			tr, err := store.ReadTrail(ctx, in.Hours, in.Limit, config.Cfg.AnalyticsRetainDays)
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{
				"hours":          in.Hours,
				"blocked":        tr.TotalBlocks,
				"challenged":     tr.TotalChallenges,
				"solved":         tr.TotalSolved,
				"reasons":        tr.Reasons,
				"paths":          tr.Paths,
				"countries":      tr.Countries,
				"hourly":         tr.Hours,
				"retention_days": tr.RetentionDays,
			}), nil
		},
	})

	// analytics_referrers exists because analytics_summary does not carry it, and
	// the referrer table is where a self-referral or referrer-spam problem shows
	// up. Diagnosing that from a screenshot is how a wrong conclusion gets shipped.
	srv.Register(mcp.Tool{
		Name: "analytics_referrers",
		Description: "Return the top referring hosts for the last N days, with counts. The site's " +
			"own domain and subdomains are excluded — internal navigation is not a referral. " +
			"Aggregate counts only. Read-only.",
		InputSchema: objSchema(nil, map[string]any{
			"days":  intProp("Look-back window in days (default 30, max 365)."),
			"limit": intProp("How many referrers to return (default 20, max 200)."),
		}),
		Visible: a.mcpVisible(apikeys.SectionAnalytics, apikeys.ActionRead),
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			if a.analytics == nil {
				return "", fmt.Errorf("analytics are unavailable")
			}
			var in struct {
				Days  int `json:"days"`
				Limit int `json:"limit"`
			}
			_ = json.Unmarshal(args, &in)
			if in.Days <= 0 {
				in.Days = 30
			}
			if in.Days > 365 {
				in.Days = 365
			}
			if in.Limit <= 0 {
				in.Limit = 20
			}
			if in.Limit > 200 {
				in.Limit = 200
			}
			refs, err := a.analytics.TopReferrers(ctx, in.Days, in.Limit)
			if err != nil {
				return "", err
			}
			return jsonStr(map[string]any{"days": in.Days, "referrers": refs}), nil
		},
	})
}

// shieldWouldHaveByName labels the observe-mode counters, so a caller reads
// "rate_limit: 412" rather than an unlabelled array position.
func shieldWouldHaveByName(st vayushield.Status) map[string]int64 {
	out := make(map[string]int64, len(st.WouldHave))
	for i, n := range st.WouldHave {
		if n > 0 {
			out[vayushield.GateNames[i]] = n
		}
	}
	return out
}
