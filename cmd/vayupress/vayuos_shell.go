// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_shell.go — the Still Air console shell.
//
// The classic console is three levels deep: sidebar, a hub page of identical
// cards, then tabs. Still Air replaces the hub layer with apps: a rail of eight
// apps, and inside each app a sidebar of its sections. The registry below is
// the whole map; every rail item and section is gated by osPathMinLevel, the
// same rule the route guard applies, so what is shown is exactly what is
// reachable.
//
// The shell keeps the classic DOM skeleton (.shell > .main > main#main-content)
// because adminOSShellFoot closes it and has twelve callers that know nothing
// about the design in use. The shell's behaviour hooks (menu toggle, command
// bar trigger, notification bell, theme, PWA install, world switch) keep their
// classic selectors, so admin-os.js drives both designs unchanged.

import (
	"context"
	"html"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/mode"
)

type saSection struct {
	Label, Href, Icon string
	// Group starts a labelled group at this section.
	Group string
	// AdminOnly is for pages whose own guard is the administrator flag rather
	// than osPathMinLevel (VayuMail's infrastructure tabs).
	AdminOnly bool
}

type saApp struct {
	Key, Label, Icon, Href string
	Sections               []saSection
}

// saClearnetApps is the clearnet console. Hub pages are gone from it: each hub's
// cards are an app's sections now.
var saClearnetApps = []saApp{
	{Key: "home", Label: "Home", Icon: "home", Href: osHome},
	{Key: "content", Label: "Content", Icon: "content", Href: "/os/posts", Sections: []saSection{
		{Label: "Posts", Href: "/os/posts", Icon: "content"},
		{Label: "New post", Href: "/os/editor", Icon: "pencil"},
		{Label: "Pages", Href: "/os/pages", Icon: "doc"},
		{Label: "Comments", Href: "/os/comments", Icon: "talk"},
		{Label: "Messages", Href: "/os/messages", Icon: "inbox"},
		{Label: "Media", Href: "/os/media", Icon: "image"},
	}},
	{Key: "audience", Label: "Audience", Icon: "audience", Href: "/os/members", Sections: []saSection{
		{Label: "Members", Href: "/os/members", Icon: "audience"},
		{Label: "Newsletter", Href: "/os/newsletter", Icon: "send"},
		{Label: "Analytics", Href: "/os/analytics", Icon: "pulse"},
		{Label: "Monetization", Href: "/os/monetization", Icon: "coin", Group: "Revenue"},
		{Label: "Advertising", Href: "/os/ads", Icon: "megaphone"},
	}},
	{Key: "mail", Label: "Mail", Icon: "mail", Href: "/os/vayumail/inbox", Sections: []saSection{
		{Label: "Mailbox", Href: "/os/vayumail/inbox", Icon: "inbox"},
		{Label: "Compose", Href: "/os/vayumail/compose", Icon: "pencil"},
		{Label: "Outbox", Href: "/os/vayumail/sent", Icon: "send"},
		{Label: "Overview", Href: "/os/vayumail", Icon: "grid"},
		{Label: "Connect a device", Href: "/os/vayumail/connect", Icon: "link"},
		{Label: "Accounts", Href: "/os/vayumail/accounts", Icon: "audience", Group: "Administration", AdminOnly: true},
		{Label: "DNS records", Href: "/os/vayumail/dns", Icon: "site", AdminOnly: true},
		{Label: "PGP keys", Href: "/os/vayumail/pgp", Icon: "key", AdminOnly: true},
		{Label: "Security", Href: "/os/vayumail/security", Icon: "lock", AdminOnly: true},
	}},
	{Key: "talk", Label: "Talk", Icon: "talk", Href: "/os/talk"},
	{Key: "site", Label: "Site", Icon: "site", Href: "/os/website", Sections: []saSection{
		{Label: "Website", Href: "/os/website", Icon: "site"},
		{Label: "Theme", Href: "/os/theme", Icon: "sun"},
		{Label: "Theme store", Href: "/os/theme/store", Icon: "grid"},
		{Label: "SEO", Href: "/os/seo", Icon: "search"},
		{Label: "Outside services", Href: "/os/website/services", Icon: "shield"},
		{Label: "Domains", Href: "/os/domains", Icon: "globe", Group: "Addresses"},
		{Label: "DNS", Href: "/os/dns", Icon: "list"},
		{Label: "Tor", Href: "/os/tor", Icon: "tor"},
	}},
	{Key: "shield", Label: "Shield", Icon: "shield", Href: "/os/shield", Sections: []saSection{
		{Label: "VayuShield", Href: "/os/shield", Icon: "shield"},
		{Label: "Sign-in security", Href: "/os/security", Icon: "lock"},
		{Label: "VayuVeil", Href: "/os/vayuveil", Icon: "eye"},
	}},
	{Key: "system", Label: "System", Icon: "system", Href: "/os/modes", Sections: []saSection{
		{Label: "System state", Href: "/os/modes", Icon: "pulse"},
		{Label: "Updates", Href: "/os/update", Icon: "refresh"},
		{Label: "Backups", Href: "/os/vayukeep", Icon: "db"},
		{Label: "Storage", Href: "/os/storage", Icon: "disk"},
		{Label: "Power", Href: "/os/power", Icon: "power"},
		{Label: "Monitoring", Href: "/os/monitoring", Icon: "pulse"},
		{Label: "VayuFlow", Href: "/os/vayuflow", Icon: "flow"},
		{Label: "Faults", Href: "/os/faults", Icon: "warn", Group: "Diagnostics"},
		{Label: "Topology", Href: "/os/topology", Icon: "topology"},
		{Label: "Replay", Href: "/os/replay", Icon: "refresh"},
		{Label: "Governance", Href: "/os/governance", Icon: "doc"},
		{Label: "Decisions", Href: "/os/adr", Icon: "list"},
	}},
	{Key: "settings", Label: "Settings", Icon: "settings", Href: "/os/settings", Sections: append(settingsSections(), []saSection{
		{Label: "My profile", Href: "/os/profile", Icon: "audience", Group: "Account"},
		{Label: "Worlds", Href: "/os/spaces", Icon: "tor"},
		{Label: "Tools and plugins", Href: "/os/tools", Icon: "grid", Group: "Integrations"},
		{Label: "API keys", Href: "/os/apikeys", Icon: "key"},
		{Label: "VayuMCP", Href: "/os/connector", Icon: "link"},
		{Label: "Claude Code", Href: "/os/claudecode", Icon: "cmd"},
		{Label: "Buzz", Href: "/os/buzz", Icon: "flow"},
	}...)},
}

// saTorApps is the Tor world: its own database and identity, so only what
// belongs in the anonymous world (ADR-0141).
var saTorApps = []saApp{
	{Key: "home", Label: "Home", Icon: "home", Href: osHome},
	{Key: "content", Label: "Content", Icon: "content", Href: "/os/posts", Sections: []saSection{
		{Label: "Posts", Href: "/os/posts", Icon: "content"},
		{Label: "New post", Href: "/os/editor", Icon: "pencil"},
		{Label: "Pages", Href: "/os/pages", Icon: "doc"},
		{Label: "Comments", Href: "/os/comments", Icon: "talk"},
		{Label: "Media", Href: "/os/media", Icon: "image"},
	}},
	{Key: "audience", Label: "Audience", Icon: "audience", Href: "/os/analytics", Sections: []saSection{
		{Label: "Analytics", Href: "/os/analytics", Icon: "pulse"},
	}},
	saClearnetApps[3], // Mail
	saClearnetApps[4], // Talk
	{Key: "site", Label: "Site", Icon: "site", Href: "/os/theme", Sections: []saSection{
		{Label: "Theme", Href: "/os/theme", Icon: "sun"},
		{Label: "Domains", Href: "/os/domains", Icon: "globe"},
	}},
	// Backups are the Tor world's own: VayuKeep runs in it against its own
	// database, with clearnet targets refused.
	{Key: "system", Label: "System", Icon: "system", Href: "/os/storage", Sections: []saSection{
		{Label: "Storage", Href: "/os/storage", Icon: "disk"},
		{Label: "Backups", Href: "/os/vayukeep", Icon: "db"},
	}},
	{Key: "settings", Label: "Settings", Icon: "settings", Href: "/os/settings", Sections: append(settingsSections(),
		saSection{Label: "My profile", Href: "/os/profile", Icon: "audience", Group: "Account"})},
}

// saNavApp maps a page's classic nav key to its app. A key missing here falls
// back to matching the route against every app's sections.
var saNavApp = map[string]string{
	"dashboard": "home",
	"posts":     "content", "pages": "content", "comments": "content", "messages": "content", "media": "content", "editor": "content",
	"members": "audience", "newsletter": "audience", "monetization": "audience", "ads": "audience", "growth": "audience", "analytics": "audience",
	"vayuos":  "mail",
	"talk":    "talk",
	"website": "site", "theme": "site", "theme-store": "site", "seo": "site", "domains": "site", "tor": "site", "optimize": "site",
	"shield": "shield", "vayuveil": "shield", "security": "shield",
	"operations": "system", "system": "system", "storage": "system", "update": "system", "monitoring": "system", "governance": "system",
	"vayuflow": "system", "modes": "system", "faults": "system", "topology": "system", "replay": "system", "adr": "system",
	"settings": "settings", "tools": "settings", "apikeys": "settings", "connector": "settings", "claudecode": "settings",
	"buzz": "settings", "profile": "settings", "spaces": "settings",
}

// saVisibleApps returns the apps this session can reach, each with only the
// sections it can reach. An app left with no reachable section is dropped,
// unless its landing page itself is reachable (Home, Talk).
func saVisibleApps(s *osSettings) []saApp {
	lvl := accessAdmin
	if s != nil {
		lvl = s.AccessLevel
	}
	src := saClearnetApps
	if config.Cfg.OnionMode {
		src = saTorApps
	}
	if s != nil && s.MailOnly {
		src = []saApp{saClearnetApps[3], {Key: "settings", Label: "Profile", Icon: "audience", Href: "/os/profile"}}
	}
	if s != nil && s.UserRole == roleClientName {
		src = []saApp{
			{Key: "mysite", Label: "My site", Icon: "site", Href: "/os/mysite", Sections: []saSection{
				{Label: "My site", Href: "/os/mysite", Icon: "site"},
				{Label: "Visitors", Href: "/os/mysite/traffic", Icon: "pulse"},
			}},
			{Key: "mail", Label: "Mail", Icon: "mail", Href: "/os/vayumail/inbox"},
			{Key: "settings", Label: "Profile", Icon: "audience", Href: "/os/profile"},
		}
	}
	reach := func(href string, adminOnly bool) bool {
		if adminOnly && (lvl < accessAdmin || (s != nil && (s.MailOnly || s.UserRole == roleClientName))) {
			return false
		}
		if s != nil && s.MailReadOnly && href == "/os/vayumail/compose" {
			return false
		}
		return saCanOpen(s, href)
	}
	var out []saApp
	for _, app := range src {
		var secs []saSection
		for _, sec := range app.Sections {
			if reach(sec.Href, sec.AdminOnly) {
				secs = append(secs, sec)
			}
		}
		if len(app.Sections) > 0 && len(secs) == 0 {
			continue
		}
		if len(app.Sections) == 0 && !reach(app.Href, false) {
			continue
		}
		a := app
		a.Sections = secs
		if len(secs) > 0 && !reach(a.Href, false) {
			a.Href = secs[0].Href // land on the first section this session can open
		}
		out = append(out, a)
	}
	return out
}

// saRouteMatch reports how well href matches the current route: the length of
// href when the route is href or below it, else -1. The console home matches
// only itself.
func saRouteMatch(route, href string) int {
	if route == "" {
		return -1
	}
	if href == osHome {
		if route == osHome || route == "/os" {
			return len(href)
		}
		return -1
	}
	if route == href || strings.HasPrefix(route, strings.TrimSuffix(href, "/")+"/") {
		return len(href)
	}
	return -1
}

// saLocate finds the current app and section. The route decides when it names
// a section; otherwise the page's nav key picks the app.
func saLocate(apps []saApp, active, route string) (app *saApp, sec *saSection) {
	best := -1
	for i := range apps {
		if n := saRouteMatch(route, apps[i].Href); n > best && len(apps[i].Sections) == 0 {
			best, app, sec = n, &apps[i], nil
		}
		for j := range apps[i].Sections {
			if n := saRouteMatch(route, apps[i].Sections[j].Href); n > best {
				best, app, sec = n, &apps[i], &apps[i].Sections[j]
			}
		}
	}
	if app != nil {
		return app, sec
	}
	if key, ok := saNavApp[active]; ok {
		for i := range apps {
			if apps[i].Key == key {
				return &apps[i], nil
			}
		}
	}
	return nil, nil
}

// saModeLabel is the status-area wording for each mode: sentence case, and the
// words an operator would say, not the engine's constant names.
func saModeLabel(m mode.Mode) string {
	switch m {
	case mode.ModeNormal:
		return "Normal"
	case mode.ModeDegraded:
		return "Degraded"
	case mode.ModeReadOnly:
		return "Read-only"
	case mode.ModeRecovery:
		return "Recovering"
	case mode.ModeMaintenance:
		return "Maintenance"
	case mode.ModeQuarantined:
		return "Quarantined"
	}
	return string(m)
}

// saModeTone is the status colour of a mode: calm, attention or alarm.
func saModeTone(m mode.Mode) string {
	switch m {
	case mode.ModeNormal:
		return "ok"
	case mode.ModeQuarantined:
		return "danger"
	}
	return "warn"
}

// saModeStrip is the one-line strip for the modes that refuse a meaningful set
// of actions (read-only, quarantined), worded from saModeEffects so it can only
// name refusals the code enforces. Every other mode stays in the status area:
// a banner for a state that changes little teaches people to ignore banners.
func saModeStrip(m mode.Mode, since time.Time, admin bool) string {
	if m != mode.ModeReadOnly && m != mode.ModeQuarantined {
		return ""
	}
	what := saModeStripText(m)
	when := ""
	if !since.IsZero() {
		when = " since " + config.InSite(since).Format("15:04")
	}
	details := ""
	if admin {
		details = `<a class="sa-strip__link" href="/os/modes">Details</a>`
	}
	return `<div class="sa-strip sa-strip--` + saModeTone(m) + `" role="status">` + saIcon("warn") +
		`<span class="sa-strip__text"><b>` + html.EscapeString(saModeLabel(m)+when) + `.</b> ` + html.EscapeString(what) + `</span>` + details + `</div>`
}

// saInitials is the avatar fallback: up to two initials of the display name.
func saInitials(name string) string {
	var out []rune
	for _, f := range strings.Fields(name) {
		for _, r := range f {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

func saRailItem(href, label, icon, count string, current bool) string {
	cur := ""
	if current {
		cur = ` aria-current="page"`
	}
	c := ""
	if count != "" {
		c = `<span class="sa-count">` + count + `</span>`
	}
	// The title names the link when the rail is collapsed to icons.
	return `<a class="nav-link sa-rail__item" href="` + href + `" title="` + html.EscapeString(label) + `"` + cur + `>` + saIcon(icon) +
		`<span class="sa-rail__label">` + html.EscapeString(label) + `</span>` + c + `</a>`
}

// saMailSide heads the Mail sidebar while a mailbox is open: the mailbox,
// its folders, and for an administrator the install's other mailboxes.
func saMailSide(m *osMailSide) string {
	var b strings.Builder
	b.WriteString(`<div class="sa-appside__title" title="` + html.EscapeString(m.Address) + `">` + html.EscapeString(m.Address) + `</div>`)
	b.WriteString(m.Folders)
	if len(m.Boxes) > 0 {
		b.WriteString(`<div class="sa-appside__group">Mailboxes on this install</div>`)
		for _, x := range m.Boxes {
			cur, count := "", ""
			if x.Current {
				cur = ` aria-current="true"`
			}
			if x.Unseen > 0 {
				count = `<span class="sa-appside__count" aria-label="` + itoaSafe(x.Unseen) + ` unread">` + notifCap(x.Unseen) + `</span>`
			}
			b.WriteString(`<a class="sa-appside__item" href="/os/vayumail/inbox?user=` + qparam(x.Key) + `"` + cur + `>` + saIcon("mail") +
				`<span class="sa-appside__label">` + html.EscapeString(x.Address) + `</span>` + count + `</a>`)
		}
		b.WriteString(`<a class="sa-appside__item" href="/os/vayumail/inbox?all=1">` + saIcon("grid") + `<span class="sa-appside__label">All mailboxes</span></a>`)
	}
	return b.String()
}

// stillAirShellHead is adminOSShellHead for the Still Air design. Same contract:
// it opens <main id="main-content">, and adminOSShellFoot closes it.
func stillAirShellHead(nonce, title, active string, s *osSettings) string {
	et := html.EscapeString(title)
	theme := "auto"
	if s.AdminTheme != "" {
		theme = s.AdminTheme
	}
	siteName := "VayuPress"
	if s.SiteName != "" {
		siteName = s.SiteName
	}
	admin := s.AccessLevel >= accessAdmin && !s.MailOnly && s.UserRole != roleClientName

	apps := saVisibleApps(s)
	app, sec := saLocate(apps, active, s.Route)
	// The mark leads to the session's first app: /os/ itself is outside an agency
	// client's and a mailbox's confinement, and would bounce them.
	home := osHome
	if len(apps) > 0 {
		home = apps[0].Href
	}

	// ── Rail ──
	var rail strings.Builder
	var settingsItem string
	for _, a := range apps {
		count := ""
		if a.Key == "mail" && s.UnreadMail > 0 {
			count = notifCap(s.UnreadMail)
		}
		item := saRailItem(a.Href, a.Label, a.Icon, count, app != nil && app.Key == a.Key)
		if a.Key == "settings" {
			settingsItem = item
			continue
		}
		rail.WriteString(item)
	}

	// ── App sidebar ──
	var side strings.Builder
	if app != nil && len(app.Sections) > 1 {
		side.WriteString(`<nav class="sa-appside" aria-label="` + html.EscapeString(app.Label) + `">`)
		mailOpen := app.Key == "mail" && s.MailSide != nil
		mailGroup := !mailOpen // under the folders, the rest of Mail is labelled
		switch {
		case app.Key == "settings":
			// The render puts the search where the title would be; the rail
			// and the crumb already say this is Settings.
			side.WriteString(settingsSearchIndex(app))
		case mailOpen:
			side.WriteString(saMailSide(s.MailSide))
		default:
			side.WriteString(`<div class="sa-appside__title">` + html.EscapeString(app.Label) + `</div>`)
		}
		for i := range app.Sections {
			x := &app.Sections[i]
			if mailOpen && x.Href == "/os/vayumail/inbox" {
				continue // the folders above are the mailbox
			}
			if !mailGroup && x.Group == "" {
				side.WriteString(`<div class="sa-appside__group">Mail</div>`)
			}
			mailGroup = true
			if x.Group != "" {
				side.WriteString(`<div class="sa-appside__group">` + html.EscapeString(x.Group) + `</div>`)
			}
			mark := ""
			if x.Href == "/os/vayumail/dns" && s.MailDNSAttention {
				mark = `<span class="sa-attn" role="img" aria-label="needs attention" title="A mail domain's DNS is not finished"></span>`
			}
			cur := ""
			if sec == x {
				cur = ` aria-current="page"`
			}
			side.WriteString(`<a class="sa-appside__item" href="` + x.Href + `"` + cur + `>` + saIcon(x.Icon) +
				`<span class="sa-appside__label">` + html.EscapeString(x.Label) + `</span>` + mark + `</a>`)
		}
		side.WriteString(`</nav>`)
	}

	// ── App header: where you are ──
	crumb := `<span class="sa-crumb__here">` + et + `</span>`
	if app != nil {
		here := title
		if sec != nil {
			here = sec.Label
		}
		if len(app.Sections) == 0 || here == app.Label {
			crumb = `<span class="sa-crumb__here">` + html.EscapeString(app.Label) + `</span>`
		} else {
			crumb = `<a class="sa-crumb__app" href="` + app.Href + `">` + html.EscapeString(app.Label) + `</a>` + saIcon("chev-r") +
				`<span class="sa-crumb__here">` + html.EscapeString(here) + `</span>`
		}
	}

	// ── Status area ──
	m := s.Mode
	if m == "" {
		m = mode.ModeNormal
	}
	modeDesc := modeDescription(m)
	modeHTML := `<span class="sa-dot sa-dot--` + saModeTone(m) + `"></span>` + html.EscapeString(saModeLabel(m))
	if admin {
		modeHTML = `<a class="sa-state sa-state--` + saModeTone(m) + `" href="/os/modes" title="` + html.EscapeString(modeDesc) + `">` + modeHTML + `</a>`
	} else {
		modeHTML = `<span class="sa-state sa-state--` + saModeTone(m) + `" title="` + html.EscapeString(modeDesc) + `">` + modeHTML + `</span>`
	}
	world := "Clearnet"
	if config.Cfg.OnionMode {
		world = "Tor"
	}
	worldHTML := `<span class="sa-state"><span class="sa-dot sa-dot--accent"></span>` + world + `</span>`
	// The switch is offered in both worlds. The Tor world is a separate
	// instance in OnionMode, and a plain label there left an operator who had
	// entered it with no way back to Clearnet; spaceSwitch gives it the
	// Clearnet link, which the parent always answers.
	if admin {
		worldHTML = `<details class="sa-pop sa-world"><summary class="sa-state" aria-label="World: ` + world + `"><span class="sa-dot sa-dot--accent"></span>` + world + `</summary>` +
			`<div class="sa-pop__panel sa-world__panel">` + spaceSwitch(s.AccessLevel, s) + `</div></details>`
	}

	strip := saModeStrip(m, s.ModeSince, admin)

	// ── Account menu ──
	// An API-key or legacy session has no person behind it, so no initials:
	// a neutral figure rather than a placeholder dot.
	avatar := `<span class="sa-avatar" aria-hidden="true">` + saIcon("audience") + `</span>`
	if strings.TrimSpace(s.UserName) != "" {
		avatar = `<span class="sa-avatar" aria-hidden="true">` + html.EscapeString(saInitials(s.UserName)) + `</span>`
	}
	if s.UserAvatar != "" {
		avatar = `<img class="sa-avatar" src="` + html.EscapeString(s.UserAvatar) + `" alt="">`
	}
	role := s.UserRole
	if role == "" {
		role = "administrator"
	}
	account := `<details class="sa-pop"><summary class="sa-account__btn" aria-label="Account and preferences">` + avatar + `</summary>
<div class="sa-pop__panel sa-menu" role="menu">
  <div class="sa-menu__who"><div class="sa-menu__name">` + html.EscapeString(saDisplayName(s)) + `</div><div class="sa-menu__role">` + html.EscapeString(role) + `</div></div>
  <a class="sa-menu__item" role="menuitem" href="/os/profile">` + saIcon("audience") + ` My profile</a>
  <div class="sa-menu__row"><span>Appearance</span><span class="sa-seg" role="group" aria-label="Colour scheme">` +
		saSeg("light", "Light", theme) + saSeg("dark", "Dark", theme) + saSeg("auto", "System", theme) + `</span></div>
  <div class="sa-menu__sep"></div>
  <button type="button" class="sa-menu__item" role="menuitem" data-pwa-install hidden>` + saIcon("download") + ` Install the app</button>
  <a class="sa-menu__item" role="menuitem" href="/os/vayumail/compose?feedback=1">` + saIcon("send") + ` Send feedback</a>
  <div class="sa-menu__sep"></div>
  <form method="POST" action="/os/logout"><button type="submit" class="sa-menu__item sa-menu__item--quiet" role="menuitem">Sign out</button></form>
</div></details>`

	// ── Phone tab bar ──
	tab := func(key, href, label, icon string) string {
		cur := ""
		if app != nil && app.Key == key {
			cur = ` aria-current="page"`
		}
		return `<a class="sa-tab" href="` + href + `"` + cur + `>` + saIcon(icon) + `<span>` + label + `</span></a>`
	}
	var tabs strings.Builder
	for _, a := range apps {
		switch a.Key {
		case "home", "content", "mail", "talk":
			tabs.WriteString(tab(a.Key, a.Href, a.Label, a.Icon))
		}
	}
	tabs.WriteString(`<button type="button" class="sa-tab" data-action="toggle-sidebar" aria-controls="vp-sidebar" aria-expanded="false">` + saIcon("more") + ` <span>More</span></button>`)

	spaceAttr := ""
	if config.Cfg.OnionMode {
		spaceAttr = ` data-space="tor"`
	}
	sideCls := ""
	if side.Len() == 0 {
		sideCls = " sa-main--solo"
	}

	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + et + ` — ` + html.EscapeString(siteName) + ` · VayuOS</title>
<meta name="robots" content="noindex, nofollow">
<meta name="htmx-config" content='{"includeIndicatorStyles":false,"globalViewTransitions":true}'>
<link rel="stylesheet" href="/os/static/css/vayuos.css?v=` + assetVer("css/vayuos.css") + `">
<link rel="preload" href="/static/fonts/inter-latin-400.woff2" as="font" type="font/woff2" crossorigin>
<link rel="preload" href="/static/fonts/inter-latin-500.woff2" as="font" type="font/woff2" crossorigin>
<link rel="preload" href="/static/fonts/inter-latin-600.woff2" as="font" type="font/woff2" crossorigin>
<link rel="icon" type="image/png" href="/static/favicon-light.png">
<link rel="manifest" href="/os/manifest.webmanifest">
` + osThemeColorMetas(theme) + `
<meta name="mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">
<meta name="apple-mobile-web-app-title" content="VayuOS">
<link rel="apple-touch-icon" href="/os/static/icons/vayuos-apple-180.png">
<script src="/os/static/js/vayuos.js?v=` + assetVer("js/vayuos.js") + `" defer></script>
</head>
<body class="vp-os" data-ui="still-air" data-theme="` + html.EscapeString(theme) + `" data-admin-theme="` + html.EscapeString(theme) + `" data-mode="` + html.EscapeString(string(m)) + `"` + spaceAttr + `>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="sidebar-overlay" aria-hidden="true"></div>
<div class="shell sa-shell">
<header class="sa-sysbar" role="banner">
  <button type="button" class="menu-toggle sa-iconbtn" data-action="toggle-sidebar" aria-label="Show or hide the app list" aria-controls="vp-sidebar" aria-expanded="true">` + saIcon("list") + `</button>
  ` + saBrand(s, home, siteName) + `
  <button type="button" class="topbar-cmd sa-search" aria-keyshortcuts="Control+K Meta+K">` + saIcon("search") + ` <span class="sa-search__text">Search or run a command</span><kbd aria-hidden="true">⌘K</kbd></button>
  <div class="sa-status" role="status" aria-label="System status">` + modeHTML + `<span class="sa-sep" aria-hidden="true"></span>` + worldHTML + `</div>
  ` + osNotifBell(s) + `
  ` + account + `
</header>
` + strip + `
<aside id="vp-sidebar" class="sidebar sa-rail" aria-label="Apps">
  <nav class="sa-rail__apps" aria-label="Apps">` + rail.String() + `</nav>
  <div class="sa-rail__foot">` + settingsItem + `<div class="sa-rail__version">` + saEdition() + `<span>` + html.EscapeString(Version) + `</span></div></div>
</aside>
<nav class="sa-tabbar" aria-label="Apps">` + tabs.String() + `</nav>
` + saPaletteIndex(apps) + saSprite + `<script nonce="` + nonce + `">` + vpIconScript + `</script>
<div class="main sa-main` + sideCls + `">
` + side.String() + `
<main id="main-content" class="content sa-content">
<div class="sa-apphead"><nav class="sa-crumb" aria-label="You are here">` + crumb + `</nav></div>
`
}

func saSeg(value, label, current string) string {
	on := ""
	if value == current {
		on = ` aria-pressed="true"`
	} else {
		on = ` aria-pressed="false"`
	}
	return `<button type="button" class="sa-seg__opt" data-sa-theme="` + value + `"` + on + `>` + label + `</button>`
}

// saDisplayName names the session in the account menu: the person, or what the
// session is when there is no person (an API key signs in as the install).
func saDisplayName(s *osSettings) string {
	if n := strings.TrimSpace(s.UserName); n != "" {
		return n
	}
	return "API key session"
}

// saAppHref is where an app lands for this session: its first reachable page,
// or fallback when the session cannot reach the app at all.
func saAppHref(s *osSettings, key, fallback string) string {
	for _, a := range saVisibleApps(s) {
		if a.Key == key {
			return a.Href
		}
	}
	return fallback
}

// saCanOpen answers "may this session open href" the way the route guard does
// (serveWithAccess): an agency client is confined to its surface, a mailbox-only
// session to VayuMail, and everyone else by access level. The mailbox-only and
// client levels are the same number, so the level alone cannot tell them apart.
func saCanOpen(s *osSettings, href string) bool {
	if s == nil {
		return accessAdmin >= osPathMinLevel(href)
	}
	switch {
	case s.UserRole == roleClientName:
		return clientPathAllowed(href)
	case s.MailOnly:
		return mailOnlyPathAllowed(href)
	}
	return s.AccessLevel >= osPathMinLevel(href)
}

// saPaletteIndex is what the command bar searches before anything else: every
// app and section this session can open, as plain links, each naming its icon.
// Hidden, and gated like the rail, so the command bar can never offer a page
// the rail would not.
func saPaletteIndex(apps []saApp) string {
	var idx strings.Builder
	idx.WriteString(`<nav hidden data-sa-index aria-hidden="true">`)
	for _, a := range apps {
		idx.WriteString(`<a href="` + a.Href + `" data-icon="` + a.Icon + `">` + html.EscapeString(a.Label) + `</a>`)
		for _, sec := range a.Sections {
			if sec.Label == a.Label || sec.Href == a.Href && len(a.Sections) == 1 {
				continue
			}
			idx.WriteString(`<a href="` + sec.Href + `" data-icon="` + sec.Icon + `">` +
				html.EscapeString(a.Label+" › "+sec.Label) + `</a>`)
		}
	}
	idx.WriteString(`</nav>`)
	return idx.String()
}

// hubRedirect answers a hub URL (/os/growth, /os/optimize, /os/operations,
// /os/system). The hubs are gone — each one's cards are an app's sections — so
// old links and bookmarks land on that app, as far as this session can see it.
func (a *App) hubRedirect(app string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, saAppHref(a.getOSSettings(r.Context()), app, osHome), http.StatusSeeOther)
	}
}

// saBrand is the system bar's left end: the mark, which goes home, and the
// site this page belongs to. With hosted sites it is a switcher to each
// site's console (ADR-0154, one console per site); inside one, it names that
// site, never the install's, so an operator always sees whose site they are
// changing.
func saBrand(s *osSettings, home, siteName string) string {
	label := siteName
	if s.Scope != nil {
		label = s.Scope.Host
	}
	if len(s.Sites) == 0 {
		return `<a class="sa-mark" href="` + home + `">` + saMark() + `<span class="sa-mark__site">` + html.EscapeString(label) + `</span></a>`
	}
	item := func(href, icon, text, meta string, current bool) string {
		cur := ""
		if current {
			cur = ` aria-current="page"`
		}
		if meta != "" {
			meta = `<span class="sa-menu__meta">` + html.EscapeString(meta) + `</span>`
		}
		return `<a class="sa-menu__item" role="menuitem" href="` + href + `"` + cur + `>` + saIcon(icon) + `<span class="sa-menu__text">` + html.EscapeString(text) + `</span>` + meta + `</a>`
	}
	var menu strings.Builder
	menu.WriteString(`<div class="sa-menu__label">Sites</div>`)
	menu.WriteString(item(osHome, "home", siteName, "Your console", s.Scope == nil))
	for _, site := range s.Sites {
		meta := ""
		if !site.Active {
			meta = "Off"
		}
		menu.WriteString(item("/os/d/"+site.ID, "globe", site.Host, meta, s.Scope != nil && s.Scope.ID == site.ID))
	}
	menu.WriteString(`<div class="sa-menu__sep"></div>` + item("/os/domains", "list", "All sites", "", false))
	return `<a class="sa-mark sa-mark--glyph" href="` + home + `" aria-label="VayuOS home">` + saMark() + `</a>` +
		`<details class="sa-pop sa-sites"><summary class="sa-sites__btn" aria-label="Switch site, now ` + html.EscapeString(label) + `">` +
		`<span class="sa-mark__site">` + html.EscapeString(label) + `</span>` + saIcon("chev-ud") + `</summary>` +
		`<div class="sa-pop__panel sa-menu sa-sites__menu" role="menu">` + menu.String() + `</div></details>`
}

// osSitesFor fills the switcher: the hosted sites this session may open, and
// the one the current page belongs to. Only a session that can open the site
// list gets one; everyone else sees the site's name, as before. A Tor site
// still minting its address has no console to go to yet, so it is left out.
func (a *App) osSitesFor(ctx context.Context, s *osSettings) {
	if a.domains == nil || s.MailOnly || s.AccessLevel < osPathMinLevel("/os/domains") {
		return
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return
	}
	scopeID := ""
	if rest, ok := strings.CutPrefix(s.Route, "/os/d/"); ok {
		scopeID, _, _ = strings.Cut(rest, "/")
	}
	at := -1
	for _, d := range list {
		if d.IsPrimary || isPendingTorSite(d.Host) {
			continue
		}
		if d.ID == scopeID {
			at = len(s.Sites)
		}
		s.Sites = append(s.Sites, osSite{ID: d.ID, Host: d.Host, Active: d.Status == domain.StatusActive})
	}
	if at >= 0 {
		s.Scope = &s.Sites[at] // taken once the slice has stopped growing
	}
}
