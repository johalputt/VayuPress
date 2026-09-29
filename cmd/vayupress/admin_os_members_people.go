// SPDX-License-Identifier: Apache-2.0

package main

import (
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/members"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// handleOSMembersPeople renders everyone who has joined as the List kind: a
// row per member, views for paying, free and unconfirmed, a search, and the
// selected member in the inspector, where their plan and labels are changed.
// Members is the overview; this is the list it keeps one click away.
func (a *App) handleOSMembersPeople(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	if a.members == nil {
		http.Redirect(w, r, "/os/members", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	view := r.URL.Query().Get("view")
	all, _ := a.members.List(ctx, 500)
	tiers, _ := a.members.ListTiers(ctx, true)
	esc := html.EscapeString

	paying, free, unconfirmed := 0, 0, 0
	var shown []members.Member
	for _, m := range all {
		switch {
		case m.VerifiedAt == nil:
			unconfirmed++
		case m.IsPaid():
			paying++
		default:
			free++
		}
		if (view == "paying" && !m.IsPaid()) || (view == "free" && (m.IsPaid() || m.VerifiedAt == nil)) || (view == "unconfirmed" && m.VerifiedAt != nil) {
			continue
		}
		shown = append(shown, m)
	}

	tierName := map[string]string{}
	for _, t := range tiers {
		tierName[t.Slug] = t.Name
	}
	plan := func(m members.Member) string {
		if n := tierName[m.Tier]; n != "" {
			return n
		}
		return titleFirst(m.Tier)
	}

	var rows, panels strings.Builder
	for i, m := range shown {
		key := "mem-" + strconv.Itoa(i)
		sel, hidden := "false", " hidden"
		if i == 0 {
			sel, hidden = "true", ""
		}
		st := ui.State("neutral", "Free")
		switch {
		case m.VerifiedAt == nil:
			st = ui.State("warn", "Unconfirmed")
		case m.IsPaid():
			st = ui.State("ok", plan(m))
		}
		lastSeen := "Never"
		if m.LastSeenAt != nil {
			lastSeen = config.FormatSite(*m.LastSeenAt, "2 Jan 2006")
		}
		search := esc(strings.ToLower(m.Email + " " + m.Name + " " + strings.Join(m.Labels, " ")))
		rows.WriteString(`<tr class="post-row" data-list-row data-member-row data-search="` + search + `" data-list-panel="` + key + `" tabindex="0" aria-selected="` + sel + `">` +
			`<td class="post-row__name">` + esc(m.Email) + `</td>` +
			`<td>` + esc(m.Name) + `</td>` +
			`<td>` + string(st) + `</td>` +
			`<td class="post-row__date">` + esc(lastSeen) + `</td>` +
			`<td class="post-row__date">` + config.FormatSite(m.CreatedAt, "2 Jan 2006") + `</td></tr>`)
		panels.WriteString(`<div data-list-panel-id="` + key + `"` + hidden + `>` + memberInspector(m, tiers, st, lastSeen) + `</div>`)
	}

	list := `<p class="table-empty">No one here yet.</p>`
	if len(shown) > 0 {
		list = `<div class="table-wrap"><table class="table post-table"><thead><tr><th>Email</th><th>Name</th><th class="sa-col--state">Plan</th><th class="sa-col--date">Last seen</th><th class="sa-col--date">Joined</th></tr></thead><tbody>` +
			rows.String() + `</tbody></table></div><p class="table-empty" data-members-empty hidden>No member matches that.</p>`
	}
	href := func(v string) string {
		if v == "" {
			return "/os/members/people"
		}
		return "/os/members/people?view=" + v
	}
	segs := []ui.Segment{
		{Label: "All", Href: href(""), Count: len(all), On: view != "paying" && view != "free" && view != "unconfirmed"},
		{Label: "Paying", Href: href("paying"), Count: paying, On: view == "paying"},
		{Label: "Free", Href: href("free"), Count: free, On: view == "free"},
	}
	// Unconfirmed is a view only while there is someone in it: rows from before
	// sign-up required a used link, which a fresh install never has.
	if unconfirmed > 0 {
		segs = append(segs, ui.Segment{Label: "Unconfirmed", Href: href("unconfirmed"), Count: unconfirmed, On: view == "unconfirmed"})
	}
	body := string(ui.List(ui.ListPage{
		Title:   "Everyone",
		Count:   strconv.Itoa(len(all)),
		Views:   ui.Segments("Show", segs...),
		Search:  ui.Search(ui.SearchBox{Placeholder: "Search members", Hook: "data-member-search"}),
		Actions: `<a class="btn btn--ghost" href="/os/api/members/export.csv" download>Export</a><a class="btn btn--ghost" href="/os/members">Members</a>`,
	}, ui.HTML(list), ui.HTML(panels.String()))) +
		`<script nonce="` + nonce + `" src="/os/static/js/admin-os-members.js?v=` + assetVer("js/admin-os-members.js") + `"></script>`
	writeOSHTML(w, r, adminOSLayout(nonce, "Everyone", "members", cfg, htmpl.HTML(body)))
}

// memberInspector is one member in the list's inspector: who they are, their
// plan (changed here), their labels, and what can be done to them.
func memberInspector(m members.Member, tiers []members.Tier, st ui.HTML, lastSeen string) string {
	esc := html.EscapeString
	var opts strings.Builder
	seen := false
	for _, t := range tiers {
		s := ""
		if t.Slug == m.Tier {
			s, seen = " selected", true
		}
		opts.WriteString(`<option value="` + esc(t.Slug) + `"` + s + `>` + esc(t.Name) + `</option>`)
	}
	if !seen && m.Tier != "" {
		opts.WriteString(`<option value="` + esc(m.Tier) + `" selected>` + esc(m.Tier) + `</option>`)
	}
	labels := ""
	for _, l := range m.Labels {
		labels += `<span class="chip chip--removable">` + esc(l) +
			`<button type="button" data-remove-label data-email="` + esc(m.Email) + `" data-label="` + esc(l) + `" aria-label="Remove the label ` + esc(l) + `">×</button></span> `
	}
	labels += `<button type="button" class="btn btn--xs btn--ghost" data-add-label data-email="` + esc(m.Email) + `">Add a label</button>`

	var b strings.Builder
	b.WriteString(`<div class="sa-insp__title">` + esc(m.Email) + `</div>`)
	if m.Name != "" {
		b.WriteString(`<div class="sa-insp__meta">` + esc(m.Name) + `</div>`)
	}
	b.WriteString(`<dl class="sa-insp__facts"><dt>State</dt><dd>` + string(st) + `</dd>` +
		`<dt>Plan</dt><dd><select class="select input--sm" data-member-tier data-email="` + esc(m.Email) + `" aria-label="Plan">` + opts.String() + `</select></dd>` +
		`<dt>Labels</dt><dd>` + labels + `</dd>`)
	if loc := geoDisplayHTML(m.Country, m.City); loc != "" {
		b.WriteString(`<dt>From</dt><dd>` + loc + `</dd>`)
	}
	b.WriteString(`<dt>Last seen</dt><dd>` + esc(lastSeen) + `</dd><dt>Joined</dt><dd>` + config.FormatSite(m.CreatedAt, "2 Jan 2006") + `</dd></dl>`)
	// An unconfirmed row predates the rule that a member must prove control of
	// their address, so removing it is the one action that makes sense for it.
	switch {
	case m.VerifiedAt == nil:
		b.WriteString(`<div class="sa-insp__actions"><button type="button" class="btn btn--sm btn--danger" data-remove-member data-email="` + esc(m.Email) + `">Remove</button></div>`)
	case m.IsPaid():
		b.WriteString(`<div class="sa-insp__actions"><button type="button" class="btn btn--sm btn--danger" data-cancel-member data-email="` + esc(m.Email) + `">Cancel the subscription</button></div>`)
	}
	return b.String()
}
