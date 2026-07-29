// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_audit.go — the posture report surface: read the root agent's
// enforcement digest, gather what the app honestly knows about itself, and
// render internal/shieldaudit's verdict on /os/vayushield.
//
// The privilege boundary is the whole design (ADR-0123). The app is unprivileged:
// it cannot run `nft list table` and has no business reading /etc/nginx, so
// everything it says about Tiers 2 and 3 comes from a fixed-schema file the root
// agent writes. Everything it says about ITSELF is introspection — never a probe.
// The app must not call itself over the network to find out whether it is
// reachable, because in onion mode that is precisely the clearnet callback the
// Tor Space design exists to prevent.

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/shieldaudit"
	"github.com/johalputt/vayupress/internal/vayushield"
)

// shieldDigestName is the file the root agent writes. Kept as a constant so the
// two sides cannot drift by a typo.
const shieldDigestName = "enforcement.digest"

// readShieldDigest parses the agent's enforcement digest.
//
// Parsing rules mirror the writer's contract, and the important one is that an
// ABSENT key is not "no". A newer app reading an older agent's digest must
// degrade to "unverified" rather than to "broken" — otherwise a routine version
// skew paints the panel red and teaches the operator to ignore it.
func readShieldDigest() shieldaudit.Digest {
	path := filepath.Join(shieldControlDir(), shieldDigestName)
	f, err := os.Open(path) //nolint:gosec // fixed name inside the app's own control dir
	if err != nil {
		return shieldaudit.Digest{}
	}
	defer func() { _ = f.Close() }()

	kv := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			kv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if len(kv) == 0 {
		return shieldaudit.Digest{}
	}

	d := shieldaudit.Digest{Present: true}
	// Age from the agent's own timestamp rather than the file mtime: an atomic
	// install(1) rewrites mtime even when the content is identical, so mtime
	// answers "when did the agent last run", not "how old is this observation".
	if ts, err := strconv.ParseInt(kv["generated"], 10, 64); err == nil && ts > 0 {
		if age := time.Since(time.Unix(ts, 0)); age > 0 {
			d.Age = age
		}
	}
	d.Tier2TablePresent = shieldaudit.ParseTri(kv["tier2_table"])
	d.Tier2MetersV4 = shieldaudit.ParseTri(kv["tier2_meters_v4"])
	d.Tier2MetersV6 = shieldaudit.ParseTri(kv["tier2_meters_v6"])
	d.ConntrackSized = shieldaudit.ParseTri(kv["conntrack_sized"])
	d.Tier3Installed = shieldaudit.ParseTri(kv["tier3_installed"])
	d.Tier3Enforcing = shieldaudit.ParseTri(kv["tier3_enforcing"])
	d.DefaultServer = shieldaudit.ParseTri(kv["default_server_443"])
	d.MCPVhostRestricted = shieldaudit.ParseTri(kv["mcp_vhost_restricted"])
	return d
}

// linkSpeedMbps reads the operator's measured ingress capacity from sysfs.
//
// This is the number every layer in the product shares a ceiling with, and it is
// deliberately read rather than assumed: once inbound traffic exceeds the uplink,
// packets are dropped by the upstream network and nothing running here is
// consulted. Returns 0 when it cannot be determined — a virtual or bonded
// interface commonly reports -1 or refuses the read entirely.
func linkSpeedMbps() int {
	entries, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return 0
	}
	best := 0
	for _, e := range entries {
		name := e.Name()
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "speed")) //nolint:gosec // sysfs, name from ReadDir
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || n <= 0 {
			continue
		}
		if n > best {
			best = n
		}
	}
	return best
}

// shieldAuditInputs assembles the report's inputs from live state.
func (a *App) shieldAuditInputs(r *http.Request) shieldaudit.Inputs {
	cur := a.shieldCurrentSettings()

	in := shieldaudit.Inputs{
		Tier2Wanted: shieldTierWanted(2),
		Tier3Wanted: shieldTierWanted(3),
		Tier2State:  shieldTierState(2),
		Tier3State:  shieldTierState(3),
		AgentAlive:  shieldAgentAlive(),

		BindAddr:  onionSafeBindAddr(config.Cfg.Port, config.Cfg.OnionMode),
		OnionMode: config.Cfg.OnionMode,

		RateLimit:   cur.RateLimit,
		LoadShed:    cur.LoadShed,
		AutoBlock:   cur.AutoBlock,
		Surge:       cur.Surge,
		ObserveOnly: a.vayuShield.Observing(),

		CaptureWired:  a.vayuShield.CaptureWired(),
		LinkSpeedMbps: linkSpeedMbps(),

		Digest: readShieldDigest(),
	}
	if a.siteSettings != nil {
		in.BehindCDN = a.siteSettings.Get(context.Background(), settings.KeyShieldBehindCDN) == "on"
	}
	// Whether real-client-IP resolution actually produced a visitor address
	// distinct from the peer, ON THIS REQUEST. This is the one signal that cannot
	// be derived from configuration: the panel could already detect the pooling
	// failure but had nowhere to record it.
	if r != nil {
		in.ClientIPResolved = auth.ClientIP(r) != stripPort(r.RemoteAddr)
	}
	return in
}

func stripPort(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// shieldAuditBody renders the posture report.
//
// Rows are ordered worst-first. An operator scanning a panel reads the top and
// stops, so a report that leads with its passes is a report that hides its
// failures — and the defect this whole surface exists to catch (a tier reporting
// Active while enforcing nothing) is exactly the kind that would be buried.
func (a *App) shieldAuditBody(r *http.Request) string {
	checks := shieldaudit.Run(a.shieldAuditInputs(r))
	pass, warn, info, fail := shieldaudit.Summary(checks)

	var b strings.Builder
	b.WriteString(vsRefresh("audit", "vs-body-audit", ""))
	b.WriteString(`<p class="muted text-sm">What this install is <strong>actually enforcing</strong>, as distinct from what is switched on. Rows about the kernel and the edge come from the root agent, which can see them; rows about VayuPress itself are read from the running process. Nothing here probes the site over the network.</p>`)

	// The baseline matters: a perfect install still shows one Fail, because the
	// volumetric row can never pass. Summarising against zero would report a
	// correct install as broken and train the operator to ignore the count.
	extra := fail - shieldaudit.BaselineFails
	switch {
	case extra > 0:
		b.WriteString(`<p class="text-sm"><span class="vs-hard-state is-err">✕ ` + strconv.Itoa(extra) +
			` control(s) not enforcing</span> — plus one permanent limit every install has.</p>`)
	case warn > 0:
		b.WriteString(`<p class="text-sm"><span class="vs-hard-state is-work">▲ ` + strconv.Itoa(warn) +
			` item(s) worth a look</span> — nothing is failing.</p>`)
	default:
		b.WriteString(`<p class="text-sm"><span class="vs-hard-state is-on">● Every control verified enforcing</span> — within the permanent limit below.</p>`)
	}

	order := []shieldaudit.Status{shieldaudit.Fail, shieldaudit.Warn, shieldaudit.Pass, shieldaudit.Info}
	icon := map[shieldaudit.Status]string{
		shieldaudit.Fail: `<span class="vs-hard-state is-err">✕</span>`,
		shieldaudit.Warn: `<span class="vs-hard-state is-work">▲</span>`,
		shieldaudit.Pass: `<span class="vs-hard-state is-on">●</span>`,
		shieldaudit.Info: `<span class="vs-hard-state is-off">○</span>`,
	}
	b.WriteString(`<div class="vs-feat">`)
	for _, want := range order {
		for _, c := range checks {
			if c.Status != want {
				continue
			}
			// data-status carries the verdict in machine-readable form. The icon
			// alone encodes it as a glyph, which is no use to a screen reader, to
			// a test asserting on the rendered page, or to an operator diffing two
			// installs.
			b.WriteString(`<div class="vs-tier" data-status="` + html.EscapeString(want.String()) +
				`"><div class="vs-tier-head">` + icon[want] + ` ` +
				html.EscapeString(c.Title) + `</div><p class="muted text-sm">` +
				html.EscapeString(c.Detail) + `</p></div>`)
		}
	}
	b.WriteString(`</div>`)
	b.WriteString(`<p class="muted text-xs">` + strconv.Itoa(pass) + ` enforcing · ` +
		strconv.Itoa(warn) + ` warning · ` + strconv.Itoa(info) + ` informational · ` +
		strconv.Itoa(fail) + ` failing.</p>`)
	return b.String()
}

// shieldAuditChip is the collapsed-state summary. It reads against
// shieldaudit.BaselineFails rather than zero: the volumetric row can never pass,
// so a chip comparing to zero would show a perfectly configured install as
// failing and teach the operator that the chip means nothing.
func (a *App) shieldAuditChip(r *http.Request) string {
	checks := shieldaudit.Run(a.shieldAuditInputs(r))
	_, warn, _, fail := shieldaudit.Summary(checks)
	if extra := fail - shieldaudit.BaselineFails; extra > 0 {
		return `<span class="mon-chip mon-chip--off">✕ ` + strconv.Itoa(extra) + ` not enforcing</span>`
	}
	if warn > 0 {
		return `<span class="mon-chip mon-chip--off">▲ ` + strconv.Itoa(warn) + ` to review</span>`
	}
	return `<span class="mon-chip mon-chip--on">● Verified</span>`
}

// logShieldPosture writes the posture report to the boot log.
//
// The panel is only seen by someone who goes looking. A defect that makes a
// whole tier inert should be visible in the log of an install nobody has opened
// — that is the difference between a regression found in a week and one found
// during an attack. Only Fail and Warn rows are logged: a boot log that recites
// every passing control is a boot log people stop reading.
func (a *App) logShieldPosture() {
	checks := shieldaudit.Run(a.shieldAuditInputs(nil))
	_, warn, _, fail := shieldaudit.Summary(checks)

	for _, c := range checks {
		switch c.Status {
		case shieldaudit.Fail:
			// The permanent volumetric row is a product limit, not this install's
			// problem, and logging it as an error every boot would be crying wolf.
			if c.Title == "Volumetric absorption" {
				continue
			}
			logging.LogWarn("vayushield", "posture: "+c.Title+" — "+c.Detail)
		case shieldaudit.Warn:
			logging.LogInfo("vayushield", "posture: "+c.Title+" — "+c.Detail)
		}
	}
	if fail <= shieldaudit.BaselineFails && warn == 0 {
		logging.LogInfo("vayushield", "posture: every control verified enforcing (volumetric absorption remains out of scope for any single origin)")
	}
}

// --- Metrics ------------------------------------------------------------------

// writeShieldMetrics exports VayuShield's live state in Prometheus text format.
//
// Everything here was already in memory and thrown away every ten seconds into
// an HTML fragment, so a panel was the only way to see it — and a panel cannot
// page anyone at 3am, cannot retain history, and cannot alert on a trend. The
// series a shield most needs are exactly the ones nobody is watching when they
// matter.
//
// What this deliberately does NOT cover, because pretending otherwise would be
// worse than the gap: Tier 2 and Tier 3 are invisible from in here. An nginx 429
// and an nft drop never reach this process, so nothing below counts them. The
// two layers that were switched from no-op to enforcing are the two with the
// least telemetry, and the posture report is what covers them instead.
func (a *App) writeShieldMetrics(w io.Writer) {
	if a.vayuShield == nil {
		return
	}
	st := a.vayuShield.Status()

	b := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	fmt.Fprintf(w,
		"# HELP vayushield_under_attack Whether adaptive under-attack mode is engaged.\n"+
			"# TYPE vayushield_under_attack gauge\n"+
			"vayushield_under_attack %d\n"+
			"# HELP vayushield_surge_active Whether Sovereign Surge is challenging every unproven visitor.\n"+
			"# TYPE vayushield_surge_active gauge\n"+
			"vayushield_surge_active %d\n"+
			"# HELP vayushield_requests_per_second Requests observed by the attack meter.\n"+
			"# TYPE vayushield_requests_per_second gauge\n"+
			"vayushield_requests_per_second %d\n"+
			"# HELP vayushield_in_flight Requests currently in flight against the load-shed cap.\n"+
			"# TYPE vayushield_in_flight gauge\n"+
			"vayushield_in_flight %d\n"+
			"# HELP vayushield_blocklisted Sources currently in the O(1) jail.\n"+
			"# TYPE vayushield_blocklisted gauge\n"+
			"vayushield_blocklisted %d\n"+
			"# HELP vayushield_suspects Sources the reputation brain is tracking.\n"+
			"# TYPE vayushield_suspects gauge\n"+
			"vayushield_suspects %d\n"+
			"# HELP vayushield_reputation_jailed Sources serving a reputation sentence.\n"+
			"# TYPE vayushield_reputation_jailed gauge\n"+
			"vayushield_reputation_jailed %d\n"+
			"# HELP vayushield_fair_shed_total Requests shed by the L2 fair-share pre-filter.\n"+
			"# TYPE vayushield_fair_shed_total counter\n"+
			"vayushield_fair_shed_total %d\n"+
			"# HELP vayushield_pardons_total Sentences lifted by a solved challenge.\n"+
			"# TYPE vayushield_pardons_total counter\n"+
			"vayushield_pardons_total %d\n"+
			"# HELP vayushield_surge_challenges_total Up-front surge interstitials served.\n"+
			"# TYPE vayushield_surge_challenges_total counter\n"+
			"vayushield_surge_challenges_total %d\n"+
			"# HELP vayushield_challenges_served_total Challenges issued in the live calibration window.\n"+
			"# TYPE vayushield_challenges_served_total counter\n"+
			"vayushield_challenges_served_total %d\n"+
			"# HELP vayushield_challenges_passed_total Challenges solved in the live calibration window.\n"+
			"# TYPE vayushield_challenges_passed_total counter\n"+
			"vayushield_challenges_passed_total %d\n"+
			"# HELP vayushield_calibration_bias Loosen-only threshold bias the L4 controller has applied.\n"+
			"# TYPE vayushield_calibration_bias gauge\n"+
			"vayushield_calibration_bias %.4f\n"+
			"# HELP vayushield_sig_cache_hits_total Signature lookups served from memory.\n"+
			"# TYPE vayushield_sig_cache_hits_total counter\n"+
			"vayushield_sig_cache_hits_total %d\n"+
			"# HELP vayushield_sig_cache_misses_total Signature lookups that fell through to SQLite.\n"+
			"# TYPE vayushield_sig_cache_misses_total counter\n"+
			"vayushield_sig_cache_misses_total %d\n"+
			"# HELP vayushield_observe_only Whether observe-only mode is engaged (1 = nothing is being enforced).\n"+
			"# TYPE vayushield_observe_only gauge\n"+
			"vayushield_observe_only %d\n",
		b(st.UnderAttack), b(st.SurgeActive), st.RPS, st.InFlight, st.Blocklisted,
		st.Suspects, st.RepJailed, st.FairShed, st.Pardons, st.SurgeChallenges,
		st.ChallengesServed, st.ChallengesPassed, st.CalibrationBias,
		st.SigCacheHits, st.SigCacheMisses, b(st.ObserveOnly),
	)

	// Per-gate would-have counters. These are the point of observe mode: an
	// operator can see that a proposed threshold would have blocked 40,000
	// requests before it blocks any of them. Labelled by gate rather than summed,
	// because "the rate limiter would have refused 40k" and "the classifier would
	// have blocked 40k" call for completely different responses.
	fmt.Fprint(w, "# HELP vayushield_would_have_total Requests observe-only mode let through that this gate would have acted on.\n"+
		"# TYPE vayushield_would_have_total counter\n")
	for i, n := range st.WouldHave {
		fmt.Fprintf(w, "vayushield_would_have_total{gate=%q} %d\n", vayushield.GateNames[i], n)
	}

	// The posture report as a metric, so a regression that makes a layer inert is
	// alertable rather than only visible to whoever opens the panel. Exported as
	// a count per status, with the permanent volumetric row reported separately —
	// alerting on `failing > 0` would fire forever on a healthy install, which is
	// how a page gets muted and then ignored.
	checks := shieldaudit.Run(a.shieldAuditInputs(nil))
	pass, warn, info, fail := shieldaudit.Summary(checks)
	fmt.Fprintf(w,
		"# HELP vayushield_posture_checks Posture-report rows by verdict.\n"+
			"# TYPE vayushield_posture_checks gauge\n"+
			"vayushield_posture_checks{status=\"pass\"} %d\n"+
			"vayushield_posture_checks{status=\"warn\"} %d\n"+
			"vayushield_posture_checks{status=\"info\"} %d\n"+
			"vayushield_posture_checks{status=\"fail\"} %d\n"+
			"# HELP vayushield_posture_failures_actionable Failing rows EXCLUDING the permanent volumetric-absorption limit. Alert on this, not on the raw fail count, which can never reach zero.\n"+
			"# TYPE vayushield_posture_failures_actionable gauge\n"+
			"vayushield_posture_failures_actionable %d\n",
		pass, warn, info, fail, max(0, fail-shieldaudit.BaselineFails))
}
