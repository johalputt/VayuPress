// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_signin.go — Shield › Sign-in attempts: who tried to sign in, where
// from, through which door and how far they got, read from the record of
// attempts (internal/signin), with the controls that stop a source: block
// its address or its network, refuse its country, lift a block, or say it
// was one of the install's own people.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	htmpl "html/template"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/signin"
	"github.com/johalputt/vayupress/internal/ui"
)

const signinPage = "/os/shield/sign-ins"

// signinWindows are the periods the page offers, in days.
var signinWindows = []struct {
	label string
	days  int
}{{"24 hours", 1}, {"7 days", 7}, {"30 days", 30}}

// signinStageWords says what an attempt reached.
var signinStageWords = map[string]string{
	signin.StagePassword: "Wrong password", signin.StageCode: "Password right, code wrong",
	signin.StagePaused: "Tried while paused", signin.StageOK: "Signed in",
}

func (a *App) handleOSSignins(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	days := 7
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		for _, o := range signinWindows {
			if o.days == d {
				days = d
			}
		}
	}
	body := a.signinsBody(r.Context(), days, r.URL.Query().Get("source"), saTabsFor(cfg, "shield", signinPage))
	writeOSHTML(w, r, adminOSLayout(nonce, "Sign-in attempts", "shield", cfg, htmpl.HTML(body)))
}

// signinsBody is the page for the last days, with source's attempts listed
// in order when source names one.
func (a *App) signinsBody(ctx context.Context, days int, source string, tabs ui.HTML) string {
	segs := make([]ui.Segment, 0, len(signinWindows))
	for _, o := range signinWindows {
		segs = append(segs, ui.Segment{Label: o.label, Href: signinPage + "?days=" + strconv.Itoa(o.days), Count: -1, On: o.days == days})
	}
	page := ui.OverviewPage{Title: "Sign-in attempts", Tabs: tabs, Actions: ui.Segments("Window", segs...)}
	if a.signin == nil {
		page.State = ui.Text("Sign-in attempts are not being recorded")
		return string(ui.Overview(page, ui.Band{Title: "Sign-ins", Aside: ui.Empty("lock", "Nothing recorded",
			"The record of sign-in attempts could not be opened: its key, kept by the secrets store, was not available. The log says why.", "")}))
	}
	label := "the last " + signinWindowLabel(days)
	rep, err := a.signin.Report(ctx, time.Now().Add(-time.Duration(days)*24*time.Hour), "", 50)
	if err != nil {
		page.State = ui.Text("The attempts could not be read: " + err.Error())
		return string(ui.Overview(page, ui.Band{Title: "Sign-ins"}))
	}
	page.State = ui.Text("No failed sign-in in " + label)
	if rep.Failures > 0 {
		page.State = ui.Text(countOf(rep.Failures, "failed sign-in") + " in " + label)
	}
	// A figure shows only when it says something: none at all on a quiet
	// install, and a count of nought is left out beside the others.
	var figs []ui.Figure
	for _, f := range []struct {
		n     int
		label string
	}{{rep.Failures, "Failed attempts"}, {rep.Sources, "Sources"}, {rep.Accounts, "Accounts tried"}, {rep.PasswordKnown, "Knew a password"}} {
		if f.n > 0 {
			figs = append(figs, ui.Figure{Value: strconv.Itoa(f.n), Label: f.label})
		}
	}
	blocks := a.signin.Blocks("")
	band := ui.Band{Title: "Sign-ins", Hint: "The last " + signinWindowLabel(days), Figures: figs,
		Aside: ui.Section("Blocks in force", strconv.Itoa(len(blocks))+" now", ui.HTML(signinBlocksList(blocks)))}
	if len(figs) == 0 {
		band.Sentence = "No one failed to sign in."
	}

	var sections []ui.HTML
	if source != "" {
		rows, _ := a.signin.Attempts(ctx, source, "", 100)
		sections = append(sections, ui.Section("This source's attempts", "Newest first; each row is one minute",
			ui.HTML(signinAttemptsTable(rows, true))))
	}
	sections = append(sections,
		ui.Section("Sources", "Those that failed most first; an address is shown with its last part hidden", ui.HTML(signinSourcesTable(rep.Top, days))),
		ui.HTML(analyticsCols(
			string(ui.Section("Accounts most tried", "Failed attempts", ui.HTML(osBarList(signinBars(rep.ByAccount, false), osShareOf(rep.Failures), "No account was tried.")))),
			string(ui.Section("Doors", "Where the attempts came in", ui.HTML(osBarList(signinBars(rep.ByDoor, true), osShareOf(rep.Failures), "No attempt."))))),
		),
		ui.Section("Where from", "Failed attempts by country", ui.HTML(signinMap(rep.Countries)+osBarList(signinCountryBars(rep.Countries), osShareOf(rep.Failures), "No attempt came from a known country."))),
		ui.Section("By hour", "Failed attempts by hour of the day, UTC", ui.HTML(osBarList(signinHourBars(rep.Hours), osShareHidden(), "No attempt."))),
		ui.Explain(ui.HTML(`<p><strong>Automatic blocking is on.</strong> A source that fails on `+strconv.Itoa(signin.SprayAccounts)+
			` or more accounts within an hour is refused sign-in for `+strconv.Itoa(int(signin.SprayFirst.Hours()))+
			` hours; caught again within a day of that ending, for twice as long, up to `+strconv.Itoa(int(signin.SprayMax.Hours()))+
			` hours. An address on this install's own network is never blocked by itself. Sessions already signed in are not touched by any block.</p>`+
			`<p>An address is never kept whole: it is kept with its last part hidden, and as a fingerprint made with a key that is not in the database. A block holds exactly, though nobody can read the address back. Attempts are kept `+
			strconv.Itoa(int(signin.Retention.Hours()/24))+` days. A holder whose password was right and whose code was not is told at once.</p>`)),
	)
	return string(ui.Overview(page, band, sections...))
}

func signinWindowLabel(days int) string {
	for _, o := range signinWindows {
		if o.days == days {
			return o.label
		}
	}
	return strconv.Itoa(days) + " days"
}

// signinWhere is a masked address and its country, as the page writes them.
func signinWhere(masked, country string) string {
	if country == "" {
		return masked
	}
	return masked + " · " + countryName(country)
}

// signinDoorList names a source's doors.
func signinDoorList(doors []string) string {
	names := make([]string, 0, len(doors))
	for _, d := range doors {
		if n := signinDoorNames[d]; n != "" {
			names = append(names, n)
		} else {
			names = append(names, d)
		}
	}
	return strings.Join(names, ", ")
}

// signinSourcesTable lists the sources, each with what it did, its state and
// the menu that stops it. Each control is a form of its own: the source's
// fingerprint rides in a hidden field, and the chosen button names what to
// do, so no value is written into an attribute as code.
func signinSourcesTable(list []signin.Source, days int) string {
	rows := make([][]ui.HTML, 0, len(list))
	for _, s := range list {
		fp := html.EscapeString(s.FP)
		what := countOf(s.Failures, "attempt") + " on " + countOf(s.Accounts, "account") + " through " + signinDoorList(s.Doors)
		var state []string
		if s.Block != nil {
			state = append(state, `<span class="badge badge--danger">`+html.EscapeString(signinBlockState(*s.Block))+`</span>`)
		}
		if s.Known {
			state = append(state, `<span class="badge badge--warn">Knew a password</span>`)
		}
		if s.Mine {
			state = append(state, `<span class="badge badge--ok">It was us</span>`)
		}
		menu := `<details class="sa-pop"><summary class="btn btn--sm">Block</summary><div class="sa-pop__panel sa-menu" role="menu">` +
			`<form hx-post="/os/api/shield/sign-ins/block" hx-swap="none"><input type="hidden" name="fp" value="` + fp + `">` +
			`<button class="sa-menu__item" role="menuitem" name="for" value="1">This address, for a day</button>` +
			`<button class="sa-menu__item" role="menuitem" name="for" value="30">This address, for 30 days</button>` +
			`<button class="sa-menu__item" role="menuitem" name="for" value="0">This address, always</button>` +
			`<div class="sa-menu__sep"></div>` +
			`<button class="sa-menu__item" role="menuitem" name="for" value="net">Its network, for 30 days<span class="sa-menu__meta">` + html.EscapeString(signin.NetworkLabel(s.Masked)) + `</span></button></form>`
		if s.Country != "" {
			menu += `<form hx-post="/os/api/shield/sign-ins/country" hx-swap="none"><input type="hidden" name="country" value="` + html.EscapeString(s.Country) + `">` +
				`<button class="sa-menu__item" role="menuitem">Refuse sign-ins from ` + html.EscapeString(countryName(s.Country)) + `<span class="sa-menu__meta">Pages stay readable</span></button></form>`
		}
		menu += `<div class="sa-menu__sep"></div><form hx-post="/os/api/shield/sign-ins/mine" hx-swap="none" hx-confirm="Clear this source and stop counting its failures on the accounts it tried?"><input type="hidden" name="fp" value="` + fp + `"><input type="hidden" name="days" value="` + strconv.Itoa(days) + `">` +
			`<button class="sa-menu__item" role="menuitem">It was one of us<span class="sa-menu__meta">Clears it and remembers the place</span></button></form></div></details>`
		rows = append(rows, []ui.HTML{
			ui.HTML(`<a href="` + signinPage + `?days=` + strconv.Itoa(days) + `&amp;source=` + fp + `"><strong>` + html.EscapeString(signinWhere(s.Masked, s.Country)) + `</strong></a>`),
			ui.Text(what),
			ui.Text(config.FormatSite(s.Last, "2 Jan 15:04")),
			ui.HTML(strings.Join(state, " ")),
			ui.HTML(menu),
		})
	}
	return string(ui.Table([]string{"Source", "What it did", "Last", "", ""}, rows, "No failed sign-in in this window."))
}

// signinBlockState says a block's reason and how long it has left.
func signinBlockState(b signin.Block) string {
	who := "Blocked"
	if b.Reason == "spraying" {
		who = "Blocked automatically"
	}
	if b.Until.IsZero() {
		return who + ", always"
	}
	left := time.Until(b.Until).Round(time.Hour)
	if left < time.Hour {
		return who + ", under an hour left"
	}
	if left >= 48*time.Hour {
		return who + ", " + strconv.Itoa(int(left.Hours()/24)) + " days left"
	}
	return who + ", " + strconv.Itoa(int(left.Hours())) + " h left"
}

// signinBlocksList is the blocks in force, each with its Lift.
func signinBlocksList(blocks []signin.Block) string {
	rows := make([][]ui.HTML, 0, len(blocks))
	for _, x := range blocks {
		what := x.Label
		switch x.Kind {
		case "country":
			what = "Sign-ins from " + countryName(x.Key)
		case "network":
			what = "The network " + x.Label
		}
		if x.Account != "" {
			what += ", for " + x.Account
		}
		rows = append(rows, []ui.HTML{ui.Text(what), ui.Text(signinBlockState(x)), ui.Text(strconv.Itoa(x.Hits)),
			ui.HTML(`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/shield/sign-ins/lift?id=` +
				strconv.FormatInt(x.ID, 10) + `" hx-swap="none">Lift</button>`)})
	}
	return string(ui.Table([]string{"Blocked", "For", "Refused", ""}, rows, "No source is blocked."))
}

// signinAttemptsTable lists attempts in order; withAccount adds the account
// tried, which the holder's own list leaves out.
func signinAttemptsTable(rows []signin.Row, withAccount bool) string {
	head := []string{"When", "From", "Door", "What happened"}
	if withAccount {
		head = []string{"When", "Account", "From", "Door", "What happened"}
	}
	out := make([][]ui.HTML, 0, len(rows))
	for _, x := range rows {
		what := signinStageWords[x.Stage]
		if x.N > 1 {
			what += " ×" + strconv.Itoa(x.N)
		}
		door := signinDoorNames[x.Door]
		if door == "" {
			door = x.Door
		}
		row := []ui.HTML{ui.Text(config.FormatSite(x.At, "2 Jan 15:04"))}
		if withAccount {
			row = append(row, ui.Text(x.Account))
		}
		out = append(out, append(row, ui.Text(signinWhere(x.Masked, x.Country)), ui.Text(door), ui.Text(what)))
	}
	return string(ui.Table(head, out, "No attempt recorded."))
}

func signinBars(list []signin.Count, doors bool) []osChartBar {
	out := make([]osChartBar, 0, len(list))
	for _, c := range list {
		label := c.Name
		if doors && signinDoorNames[c.Name] != "" {
			// A row's label starts the line, so it starts with a capital;
			// the same names run on in a sentence elsewhere.
			label = strings.ToUpper(signinDoorNames[c.Name][:1]) + signinDoorNames[c.Name][1:]
		}
		out = append(out, osChartBar{Label: label, Value: c.N})
	}
	return out
}

func signinCountryBars(m map[string]int) []osChartBar {
	out := make([]osChartBar, 0, len(m))
	for c, n := range m {
		out = append(out, osChartBar{Label: countryName(c), Value: n, Flag: c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Value != out[j].Value {
			return out[i].Value > out[j].Value
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func signinHourBars(h [24]int) []osChartBar {
	out := make([]osChartBar, 0, 24)
	for i, n := range h {
		if n > 0 {
			out = append(out, osChartBar{Label: fmt.Sprintf("%02d:00", i), Value: n})
		}
	}
	return out
}

// signinMap shades each country by its failed attempts, as Audience shades
// readers.
func signinMap(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	cs := make(map[string]mapCountry, len(m))
	for c, n := range m {
		cs[c] = mapCountry{n: n, said: countOf(n, "failed attempt")}
	}
	return osWorldMap(cs, nil, false)
}

// ── Actions ──────────────────────────────────────────────────────────────────

// signinActionError answers a refused action with its reason, which the
// console's toast says.
func signinActionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, signin.ErrSelf) {
		writeAPIError(w, r, http.StatusConflict, "would-block-you", "This would stop your own sign-in, so it was not set.", "")
		return
	}
	writeAPIError(w, r, http.StatusBadRequest, "bad-request", err.Error(), "")
}

func signinDone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("HX-Refresh", "true")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSSigninBlock blocks a source's address for a day, 30 days or
// always, or its network for 30 days.
func (a *App) handleOSSigninBlock(w http.ResponseWriter, r *http.Request) {
	if a.signin == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "not-recording", "Sign-in attempts are not being recorded.", "")
		return
	}
	fp := r.PostFormValue("fp")
	network, d := false, 30*24*time.Hour
	switch r.PostFormValue("for") {
	case "1":
		d = 24 * time.Hour
	case "30":
	case "0":
		d = 0
	case "net":
		network = true
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "Say how long to block it for.", "")
		return
	}
	b, err := a.signin.BlockSource(r.Context(), fp, network, "", d, dbpkg.AuditActor(r), auth.ClientIP(r))
	if err != nil {
		signinActionError(w, r, err)
		return
	}
	dbpkg.AuditLog("signin.block", dbpkg.AuditActor(r), b.Label, b.Kind+" "+signinBlockState(*b))
	signinDone(w, r)
}

// handleOSSigninCountry refuses sign-ins from a source's country, for every
// account; pages stay readable.
func (a *App) handleOSSigninCountry(w http.ResponseWriter, r *http.Request) {
	if a.signin == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "not-recording", "Sign-in attempts are not being recorded.", "")
		return
	}
	b, err := a.signin.BlockCountry(r.Context(), r.PostFormValue("country"), "", 0, dbpkg.AuditActor(r), auth.ClientIP(r))
	if err != nil {
		signinActionError(w, r, err)
		return
	}
	dbpkg.AuditLog("signin.block", dbpkg.AuditActor(r), b.Key, "country")
	signinDone(w, r)
}

// handleOSSigninLift ends a block now.
func (a *App) handleOSSigninLift(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if a.signin == nil || err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "Name the block to lift.", "")
		return
	}
	if err := a.signin.Lift(r.Context(), id); err != nil {
		signinActionError(w, r, err)
		return
	}
	dbpkg.AuditLog("signin.lift", dbpkg.AuditActor(r), strconv.FormatInt(id, 10), "")
	signinDone(w, r)
}

// handleOSSigninMine says a source was the install's own people: it is
// cleared, and each account it tried in the window remembers the place.
func (a *App) handleOSSigninMine(w http.ResponseWriter, r *http.Request) {
	fp := r.PostFormValue("fp")
	days, _ := strconv.Atoi(r.PostFormValue("days"))
	if a.signin == nil || fp == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "Name the source.", "")
		return
	}
	if days < 1 || days > 30 {
		days = 30
	}
	rows, err := a.signin.Attempts(r.Context(), fp, "", 1000)
	if err != nil {
		signinActionError(w, r, err)
		return
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	seen := map[string]bool{}
	for _, x := range rows {
		if x.At.Before(since) || seen[x.Account] {
			continue
		}
		seen[x.Account] = true
		if err := a.signin.ItWasMe(r.Context(), x.Account, fp); err != nil {
			signinActionError(w, r, err)
			return
		}
	}
	dbpkg.AuditLog("signin.mine", dbpkg.AuditActor(r), fp, strconv.Itoa(len(seen))+" accounts")
	signinDone(w, r)
}
