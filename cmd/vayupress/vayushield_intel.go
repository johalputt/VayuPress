// SPDX-License-Identifier: Apache-2.0

package main

// vayushield_intel.go — the operator-facing half of third-party network
// intelligence: which feeds are on, when they refresh, and what the panel says
// about them.
//
// internal/vayushield/intel holds the rule that matters (a feed can never
// produce an allow) and the integrity controls. This file holds the decisions
// that belong to a running install: how often to refresh, what happens on a
// restart, and what an operator sees before they trust a list.

import (
	"context"
	"html"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/shieldaudit"
	"github.com/johalputt/vayupress/internal/vayushield/intel"
)

// intelRefreshInterval is how often enabled feeds are re-fetched.
//
// Twelve hours, because of what these lists are. Cloud vendors publish range
// changes on the order of days and Spamhaus DROP moves slowly by design — it
// lists hijacked netblocks, not this hour's spam sources. Polling four public
// endpoints every few minutes would be someone else's bandwidth spent to learn
// nothing, and the delta bound already means a fast-moving change is refused
// rather than applied.
const intelRefreshInterval = 12 * time.Hour

// bootShieldIntel builds the feed set, seeds it from the last-good on-disk copy
// and starts the refresh loop.
//
// Always leaves a.shieldIntel non-nil, so the Match hook wired into the shield
// is safe to call unconditionally. With no feed enabled it answers from an empty
// map and the request path pays one map iteration over nothing.
func (a *App) bootShieldIntel(ctx context.Context) {
	a.shieldIntel = intel.NewFetcher(filepath.Join(config.Cfg.CacheDir, "intel"))
	a.applyShieldIntelSettings(ctx)
	// Seed from disk before anything serves a request. Without this a restart
	// during an outage — or on a machine that cannot reach the publishers at all
	// — would run with no intelligence indefinitely while the panel showed the
	// feeds as enabled.
	a.shieldIntel.LoadCache()

	enabled := a.enabledIntelFeeds(ctx)
	if len(enabled) == 0 {
		return
	}
	if safefetch.ClearnetBlocked() {
		// Say it once, plainly. A Tor Space makes no outbound request, so these
		// feeds will never refresh here — and a feed frozen at its last-good copy
		// is indistinguishable from a working one unless somebody says so.
		logging.LogInfo("vayushield", "network intelligence: "+strings.Join(enabled, ", ")+
			" enabled, but clearnet egress is blocked in this Space — feeds do not refresh here "+
			"and are serving their last-good cached copy, if any")
		return
	}
	logging.LogInfo("vayushield", "network intelligence: "+strings.Join(enabled, ", ")+
		" enabled — datacenter ranges weigh a score, hostile ranges refuse; no feed can grant access")

	go func() {
		// One refresh shortly after boot rather than immediately: startup is
		// already the busiest the process gets, and nothing here is urgent enough
		// to compete with warming caches and opening the listener.
		first := time.NewTimer(30 * time.Second)
		defer first.Stop()
		select {
		case <-queue.DoneCh:
			return
		case <-first.C:
		}
		a.refreshShieldIntel()

		t := time.NewTicker(intelRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case <-t.C:
				a.refreshShieldIntel()
			}
		}
	}()
}

// refreshShieldIntel runs one refresh pass and reports what changed.
//
// A refused refresh is logged at WARN rather than folded into the same line as a
// network error. They look alike in a status column and mean opposite things: a
// transport error is the publisher being unreachable, a refusal is the publisher
// answering with something unlike what they served yesterday — which is the one
// event this whole layer's integrity story is built around.
func (a *App) refreshShieldIntel() {
	if a.shieldIntel == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a.shieldIntel.Refresh(ctx)
	for _, s := range a.shieldIntel.Statuses() {
		switch {
		case !s.Enabled:
		case s.Refused != "":
			logging.LogWarn("vayushield", "network intelligence: "+s.Name+" refused — "+s.Refused)
		case s.LastError != "":
			logging.LogWarn("vayushield", "network intelligence: "+s.Name+" did not refresh — "+s.LastError)
		}
	}
}

// applyShieldIntelSettings registers every shipped feed with the operator's
// current opt-in state. Called at boot and again on every settings save, so
// turning a feed off takes effect on the next request rather than the next
// restart.
func (a *App) applyShieldIntelSettings(ctx context.Context) {
	if a.shieldIntel == nil {
		return
	}
	on := map[string]bool{}
	for _, id := range a.enabledIntelFeeds(ctx) {
		on[id] = true
	}
	for _, def := range intel.DefaultFeeds() {
		a.shieldIntel.Add(def, on[def.ID])
	}
}

// postedIntelFeeds reads the feed checkboxes out of a settings save, keeping
// only IDs this build ships and preserving the shipped order so the stored value
// does not churn on every save.
func postedIntelFeeds(r *http.Request) []string {
	var out []string
	for _, def := range intel.DefaultFeeds() {
		if strings.TrimSpace(r.PostFormValue("sh_intel_"+def.ID)) != "" {
			out = append(out, def.ID)
		}
	}
	return out
}

// kickShieldIntelRefresh fetches in the background when a feed has been switched
// on and has nothing loaded yet.
//
// Without it, enabling a feed would leave it empty until the next scheduled
// refresh — up to twelve hours of a panel row reading "enabled" over a set that
// contains nothing, which is the failure mode this whole surface exists to make
// impossible to miss.
func (a *App) kickShieldIntelRefresh() {
	if a.shieldIntel == nil || safefetch.ClearnetBlocked() {
		return
	}
	for _, s := range a.shieldIntel.Statuses() {
		if s.Enabled && s.Entries == 0 {
			go a.refreshShieldIntel()
			return
		}
	}
}

// shieldIntelAudit reports the ENABLED feeds to the posture report. Disabled
// ones are omitted rather than listed as off: the report answers "what is
// enforcing", and a row per unused option is how a report stops being read.
func (a *App) shieldIntelAudit() []shieldaudit.IntelFeed {
	if a.shieldIntel == nil {
		return nil
	}
	var out []shieldaudit.IntelFeed
	for _, s := range a.shieldIntel.Statuses() {
		if !s.Enabled {
			continue
		}
		out = append(out, shieldaudit.IntelFeed{
			Name:      s.Name,
			Hostile:   s.Kind == intel.KindHostile.String(),
			Entries:   s.Entries,
			Refused:   s.Refused,
			LastError: s.LastError,
		})
	}
	return out
}

// shieldIntelStatus reports the ENABLED feeds for the MCP read tools.
//
// The publisher's URL is included, unlike anything in the allow/deny lists. The
// asymmetry is deliberate: knowing which networks bypass the shield names the
// addresses worth impersonating, while a feed URL is a public endpoint anyone
// can already fetch, and withholding it would only stop a reader answering
// "whose list is this?" — the question that decides whether the feed should be
// trusted at all.
func (a *App) shieldIntelStatus() []map[string]any {
	if a.shieldIntel == nil {
		return nil
	}
	byID := map[string]intel.Feed{}
	for _, def := range intel.DefaultFeeds() {
		byID[def.ID] = def
	}
	var out []map[string]any
	for _, s := range a.shieldIntel.Statuses() {
		if !s.Enabled {
			continue
		}
		row := map[string]any{
			"id": s.ID, "name": s.Name, "kind": s.Kind,
			"entries": s.Entries, "ranges": s.Ranges, "checksum": s.Checksum,
			"url": byID[s.ID].URL,
			// What a match MEANS, spelled out rather than left to be inferred
			// from the kind string. A reader that treats "datacenter" as grounds
			// to block has misread the whole design.
			"effect": intelEffect(s.Kind),
		}
		if !s.FetchedAt.IsZero() {
			row["fetched_at"] = s.FetchedAt.UTC().Format(time.RFC3339)
		}
		if s.Refused != "" {
			row["refused"] = s.Refused
		}
		if s.LastError != "" {
			row["last_error"] = s.LastError
		}
		out = append(out, row)
	}
	return out
}

func intelEffect(kind string) string {
	if kind == intel.KindHostile.String() {
		return "refuses matching sources at their own gate; a solved challenge does not bypass it"
	}
	return "adds " + strconv.FormatFloat(intel.DatacenterDelta, 'f', 2, 64) +
		" to the bot score, sharing one clamped budget with the other heuristics; never blocks alone"
}

// shieldIntelBand renders "Published network lists" inside the protection form.
//
// The copy leads with what a match MEANS rather than with what the feed is,
// because the two tiers do genuinely different things and an operator who reads
// them as one switch will turn on a datacenter list expecting it to block. It
// also states the licence position plainly: these are other people's lists on
// other people's terms, and shipping them switched on would be making that
// decision for the operator.
func (a *App) shieldIntelBand(ctx context.Context) string {
	on := map[string]bool{}
	for _, id := range a.enabledIntelFeeds(ctx) {
		on[id] = true
	}
	status := map[string]intel.Status{}
	if a.shieldIntel != nil {
		for _, s := range a.shieldIntel.Statuses() {
			status[s.ID] = s
		}
	}

	var b strings.Builder
	b.WriteString(`<div class="card-title vs-section">Published network lists</div>`)
	b.WriteString(`<p class="muted text-sm">Lists published by other people about the internet, refreshed every 12 hours. ` +
		`A <strong>datacenter</strong> list says an address belongs to hosting or cloud infrastructure &mdash; evidence a visitor is automated, never proof, since a VPN exit and an office connection live there too; it nudges the score toward a check and can never block on its own. A <strong>hostile</strong> list says a network is hijacked or under criminal control, and that one refuses. ` +
		`<strong>No list can ever allow anything.</strong> That is a property of the code rather than a setting: if one of these endpoints were ever hijacked, the worst it could do is over-block, which you would see. Everything is off until you turn it on, because these are third-party lists under third-party terms &mdash; some restrict commercial use, and that is your call to make.</p>`)

	if config.Cfg.OnionMode {
		b.WriteString(`<p class="muted text-sm"><strong>This Space makes no outbound requests</strong>, so nothing here will refresh. A list you enable serves whatever was last cached on this machine, or nothing at all.</p>`)
	}

	for _, def := range intel.DefaultFeeds() {
		s := status[def.ID]
		b.WriteString(`<div class="vs-feat">`)
		b.WriteString(vsRow("sh_intel_"+def.ID, def.Name, def.Note, on[def.ID], false))
		b.WriteString(`<p class="muted text-xs">` + intelKindLabel(def.Kind) + intelFeedState(on[def.ID], s) + `</p>`)
		b.WriteString(`</div>`)
	}

	if a.vayuShield != nil {
		if hostile, datacenter := a.vayuShield.IntelHits(); hostile+datacenter > 0 {
			b.WriteString(`<p class="muted text-xs">Since the last restart: ` +
				strconv.FormatInt(datacenter, 10) + ` request(s) came from a datacenter range and ` +
				strconv.FormatInt(hostile, 10) + ` from a listed hostile network.</p>`)
		}
	}
	return b.String()
}

func intelKindLabel(k intel.Kind) string {
	if k == intel.KindHostile {
		return `Refuses matching networks. `
	}
	return `Weighs the score; never blocks alone. `
}

// intelFeedState is the one line that separates a working feed from one that
// merely says it is on.
//
// A feed that stopped updating months ago looks exactly like a healthy one
// unless the entry count, the checksum and the last fetch are on screen — and
// the two failure modes are reported separately because they mean opposite
// things. "Did not refresh" is the publisher being unreachable. "Refused" is the
// publisher answering with something unlike what they served yesterday, which is
// the event the whole integrity design exists for.
func intelFeedState(enabled bool, s intel.Status) string {
	if !enabled {
		return `Off &mdash; not fetched, and not consulted.`
	}
	var b strings.Builder
	if s.Entries > 0 {
		b.WriteString(strconv.Itoa(s.Entries) + ` published range(s), checksum ` + html.EscapeString(s.Checksum))
		if !s.FetchedAt.IsZero() {
			b.WriteString(`, fetched ` + s.FetchedAt.UTC().Format("2006-01-02 15:04") + ` UTC`)
		}
		b.WriteString(`. `)
	} else {
		b.WriteString(`Nothing loaded yet. `)
	}
	if s.Refused != "" {
		b.WriteString(`<strong>Last refresh refused:</strong> ` + html.EscapeString(s.Refused) + `. The previous copy is still in use.`)
	} else if s.LastError != "" {
		b.WriteString(`<strong>Last refresh failed:</strong> ` + html.EscapeString(s.LastError) + `.`)
	}
	return b.String()
}

// enabledIntelFeeds reads the operator's opt-in list, keeping only IDs this
// build actually ships.
//
// The filter is not defensive tidiness. The setting outlives the code: a feed
// removed in a later version leaves its ID behind in the settings row of every
// install that had it on, and an unfiltered read would carry a name the panel
// cannot explain and the fetcher cannot fetch.
func (a *App) enabledIntelFeeds(ctx context.Context) []string {
	if a.siteSettings == nil {
		return nil
	}
	known := map[string]bool{}
	for _, def := range intel.DefaultFeeds() {
		known[def.ID] = true
	}
	var out []string
	for _, id := range policyLines(a.siteSettings.Get(ctx, settings.KeyShieldIntelFeeds)) {
		if id = strings.ToLower(strings.TrimSpace(id)); known[id] {
			out = append(out, id)
		}
	}
	return out
}
