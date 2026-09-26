// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/vayuos/torspace"
)

func saSession(level int) *osSettings {
	return &osSettings{AccessLevel: level, UserName: "Ankush Johal", SiteName: "johal.in"}
}

// Shown means reachable. The rail and every app sidebar are judged against the
// route guard itself, so a registry entry that outruns the guard fails here
// whichever way the registry was built.
func TestStillAirShowsOnlyWhatTheSessionCanOpen(t *testing.T) {
	type session struct {
		name  string
		s     *osSettings
		guard func(string) bool
	}
	var sessions []session
	for _, lvl := range []int{accessAuthor, accessEditor, accessAdmin} {
		l := lvl
		sessions = append(sessions, session{"level " + string(rune('0'+l)), saSession(l), func(h string) bool { return l >= osPathMinLevel(h) }})
	}
	mailbox := saSession(accessMailOnly)
	mailbox.MailOnly = true
	sessions = append(sessions, session{"mailbox-only", mailbox, mailOnlyPathAllowed})
	client := saSession(accessMailOnly)
	client.UserRole = roleClientName
	sessions = append(sessions, session{"agency client", client, clientPathAllowed})
	for _, c := range sessions {
		apps := saVisibleApps(c.s)
		if len(apps) == 0 {
			t.Errorf("%s: an empty rail", c.name)
		}
		for _, app := range apps {
			if !c.guard(app.Href) {
				t.Errorf("%s: app %s lands on %s, which the guard refuses", c.name, app.Key, app.Href)
			}
			for _, sec := range app.Sections {
				if !c.guard(sec.Href) {
					t.Errorf("%s: %s › %s links %s, which the guard refuses", c.name, app.Label, sec.Label, sec.Href)
				}
			}
		}
	}
}

// One seed per rule the registry applies on top of the guard.
func TestStillAirRailPerRole(t *testing.T) {
	keys := func(s *osSettings) string {
		var k []string
		for _, a := range saVisibleApps(s) {
			k = append(k, a.Key)
		}
		return strings.Join(k, ",")
	}
	if got := keys(saSession(accessAdmin)); got != "home,content,audience,mail,talk,site,shield,system,settings" {
		t.Errorf("administrator rail = %s", got)
	}
	author := keys(saSession(accessAuthor))
	for _, adminOnly := range []string{"system", "shield", "audience"} {
		if strings.Contains(","+author+",", ","+adminOnly+",") {
			t.Errorf("an author's rail offers %s: %s", adminOnly, author)
		}
	}
	// VayuMail's infrastructure tabs are guarded by the administrator flag, not
	// by osPathMinLevel, so the registry carries that rule itself.
	for _, a := range saVisibleApps(saSession(accessEditor)) {
		for _, sec := range a.Sections {
			if sec.AdminOnly {
				t.Errorf("an editor sees the administrator-only %s", sec.Href)
			}
		}
	}
	mo := saSession(accessMailOnly)
	mo.MailOnly = true
	if got := keys(mo); got != "mail,settings" {
		t.Errorf("a mailbox-only session's rail = %s, want mail and profile only", got)
	}
	client := saSession(accessMailOnly)
	client.UserRole = roleClientName
	if got := keys(client); got != "mysite,mail,settings" {
		t.Errorf("an agency client's rail = %s, want their own site first", got)
	}
}

// Every link in the registry leads to a route that exists. A section pointing
// at a page that was renamed would otherwise be a dead end in the chrome.
func TestEveryStillAirLinkIsARealRoute(t *testing.T) {
	have := map[string]bool{}
	for _, r := range osRoutes(t) {
		have[r] = true
	}
	check := func(href string) {
		href = strings.TrimSuffix(href, "/")
		if have[href] {
			return
		}
		// A Settings category is one pattern route; the link is real only
		// when the handler knows its slug (an unknown one is sent back to
		// the front page, which would hide a renamed category).
		if slug, ok := strings.CutPrefix(href, "/os/settings/"); ok && have["/os/settings/{group}"] {
			if _, known := settingsCategoryFor(slug); known {
				return
			}
		}
		t.Errorf("%s is linked from the Still Air chrome but is not a route", href)
	}
	for _, apps := range [][]saApp{saClearnetApps, saTorApps} {
		for _, a := range apps {
			check(a.Href)
			for _, s := range a.Sections {
				check(s.Href)
			}
		}
	}
}

// The current section comes from the route: the longest section that the route
// is, or is below. A prefix is not a match on its own (/os/theme is not the
// Theme store, /os/vayumail is not the Mailbox).
func TestStillAirFindsTheCurrentSection(t *testing.T) {
	apps := saVisibleApps(saSession(accessAdmin))
	for _, c := range []struct{ route, active, app, sec string }{
		{"/os/vayumail/dns", "vayuos", "mail", "DNS records"},
		{"/os/vayumail", "vayuos", "mail", "Overview"},
		{"/os/vayumail/inbox", "vayuos", "mail", "Mailbox"},
		{"/os/theme/store", "theme-store", "site", "Theme store"},
		{"/os/theme", "theme", "site", "Theme"},
		{"/os/", "dashboard", "home", ""},
		{"/os/talk", "talk", "talk", ""},
		{"/os/d/{id}/website", "website", "site", ""}, // a hosted site's page: the app, no section
		{"/os/posts/{id}/edit", "editor", "content", "Posts"},
	} {
		app, sec := saLocate(apps, c.active, c.route)
		gotApp, gotSec := "", ""
		if app != nil {
			gotApp = app.Key
		}
		if sec != nil {
			gotSec = sec.Label
		}
		if gotApp != c.app || gotSec != c.sec {
			t.Errorf("route %s: located %q › %q, want %q › %q", c.route, gotApp, gotSec, c.app, c.sec)
		}
	}
}

// pictograph finds a character standing in for an icon: any "other symbol"
// (emoji, dingbats, technical symbols such as ⏸) or an emoji variation
// selector. ⌘ is allowed: it is the key's name on the keyboard, not a picture.
func pictograph(s string) string {
	for _, r := range s {
		if r == '⌘' {
			continue
		}
		if unicode.Is(unicode.So, r) || r == '\uFE0F' {
			return string(r)
		}
	}
	return ""
}

// The chrome draws every icon from the one set and never uses an emoji in its
// place, in both worlds and for every role.
func TestStillAirChromeUsesOnlyTheIconSet(t *testing.T) {
	defer func(v bool) { config.Cfg.OnionMode = v }(config.Cfg.OnionMode)
	for _, onion := range []bool{false, true} {
		config.Cfg.OnionMode = onion
		for _, lvl := range []int{accessAuthor, accessEditor, accessAdmin} {
			s := saSession(lvl)
			s.Mode = mode.ModeReadOnly
			out := stillAirShellHead("n", "Page", "dashboard", s)
			if strings.Contains(out, "sa-ico--missing") {
				t.Errorf("onion=%v level %d: the chrome names an icon that is not in the set", onion, lvl)
			}
			if m := pictograph(out); m != "" {
				t.Errorf("onion=%v level %d: the chrome uses the emoji %q as an icon", onion, lvl, m)
			}
		}
	}
	// Every icon the registry names exists, whether or not the page under test
	// happens to render that app's sidebar.
	for _, apps := range [][]saApp{saClearnetApps, saTorApps} {
		for _, a := range apps {
			if strings.Contains(string(ui.Icon(a.Icon)), "sa-ico--missing") {
				t.Errorf("app %s names icon %q, which is not in the set", a.Key, a.Icon)
			}
			for _, sec := range a.Sections {
				if strings.Contains(string(ui.Icon(sec.Icon)), "sa-ico--missing") {
					t.Errorf("%s › %s names icon %q, which is not in the set", a.Label, sec.Label, sec.Icon)
				}
			}
		}
	}
}

// Text from settings reaches the chrome escaped exactly once.
func TestStillAirChromeEscapesOnce(t *testing.T) {
	s := saSession(accessAdmin)
	s.SiteName = `Rock & "Roll" <b>`
	s.UserName = `Dr. <script>`
	out := stillAirShellHead("n", `A & B`, "dashboard", s)
	if strings.Contains(out, "<b>") || strings.Contains(out, "<script>") {
		t.Error("a setting reached the chrome as markup")
	}
	if strings.Contains(out, "&amp;amp;") || strings.Contains(out, "&amp;lt;") {
		t.Error("a setting was escaped twice")
	}
	if !strings.Contains(out, "Rock &amp; &#34;Roll&#34; &lt;b&gt;") {
		t.Error("the site name should appear, escaped once")
	}
}

// The state strip appears for the modes that change what an action does, and
// only for those: a banner for a state that changes nothing teaches people to
// ignore banners.
func TestStillAirStripOnlyWhenActionsChange(t *testing.T) {
	for m, want := range map[mode.Mode]bool{
		mode.ModeNormal: false, mode.ModeDegraded: false, mode.ModeMaintenance: false,
		mode.ModeReadOnly: true, mode.ModeRecovery: false, mode.ModeQuarantined: true,
	} {
		s := saSession(accessAdmin)
		s.Mode = m
		got := strings.Contains(stillAirShellHead("n", "P", "dashboard", s), `class="sa-strip`)
		if got != want {
			t.Errorf("mode %s: strip shown = %v, want %v", m, got, want)
		}
	}
}

// Hub URLs redirect to their app, so a bookmark still lands.
func TestHubURLsRedirectToTheirApp(t *testing.T) {
	a := &App{}
	for _, c := range []struct{ hub, want string }{{"system", "/os/modes"}, {"site", "/os/website"}, {"audience", "/os/members"}} {
		rec := httptest.NewRecorder()
		a.hubRedirect(c.hub)(rec, httptest.NewRequest(http.MethodGet, "/os/x", nil))
		if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != c.want {
			t.Errorf("%s hub → %d %s, want 303 %s", c.hub, rec.Code, loc, c.want)
		}
	}
	// An editor cannot open Members, so the Audience hub lands where they can.
	if got := saAppHref(saSession(accessEditor), "audience", osHome); got != "/os/analytics" {
		t.Errorf("an editor's Audience lands on %s, want /os/analytics", got)
	}
}

const stillAirCSSPath = "../../static/css/vayuos.css"

func readStillAirCSS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(stillAirCSSPath)
	if err != nil {
		t.Fatalf("read %s: %v", stillAirCSSPath, err)
	}
	return string(b)
}

// %23 is "#" inside a data: URL, where an inline SVG hid a slate-blue
// select arrow from the first form of this check.
var colourLiteralRe = regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|%23[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)

// Components read tokens; only a token declaration may name a colour. This is
// what keeps a later "just this once" hex from starting a second palette.
func TestStillAirComponentsNameNoColour(t *testing.T) {
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(readStillAirCSS(t), "")
	for i, line := range strings.Split(css, "\n") {
		for _, decl := range strings.Split(line, ";") {
			d := strings.TrimSpace(decl)
			if d == "" || strings.HasPrefix(d, "--") {
				continue
			}
			// A selector line with a declaration on it: judge the declaration.
			if j := strings.LastIndex(d, "{"); j >= 0 {
				d = strings.TrimSpace(d[j+1:])
				if strings.HasPrefix(d, "--") {
					continue
				}
			}
			if colourLiteralRe.MatchString(d) {
				t.Errorf("vayuos.css line %d names a colour outside a token: %s", i+1, d)
			}
		}
	}
}

var rgbaTokenRe = regexp.MustCompile(`(--[a-z0-9-]+):\s*(rgba\([^)]*\))`)

// blend composites an rgba() token over an opaque hex one, as the browser paints
// a tint over the surface under it.
func blend(t *testing.T, rgba, under string) string {
	t.Helper()
	var r, g, b int
	var a float64
	if _, err := fmt.Sscanf(strings.ReplaceAll(rgba, " ", ""), "rgba(%d,%d,%d,%g)", &r, &g, &b, &a); err != nil {
		t.Fatalf("cannot read %q as rgba: %v", rgba, err)
	}
	var ur, ug, ub int
	if _, err := fmt.Sscanf(under, "#%02x%02x%02x", &ur, &ug, &ub); err != nil {
		t.Fatalf("cannot read %q as hex: %v", under, err)
	}
	mix := func(c, u int) int { return int(math.Round(float64(c)*a + float64(u)*(1-a))) }
	return fmt.Sprintf("#%02x%02x%02x", mix(r, ur), mix(g, ug), mix(b, ub))
}

var consoleTokenRe = regexp.MustCompile(`(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{3,8})`)

// sectionTokens reads the hex tokens of one block. The first declaration wins:
// later ones are the media-query mirror of the same values.
func sectionTokens(section string) map[string]string {
	out := map[string]string{}
	for _, m := range consoleTokenRe.FindAllStringSubmatch(section, -1) {
		if _, ok := out[m[1]]; !ok {
			out[m[1]] = strings.ToLower(m[2])
		}
	}
	return out
}

func stillAirTokens(t *testing.T) (dark, light map[string]string) {
	t.Helper()
	css := readStillAirCSS(t)
	darkAt := strings.Index(css, `.vp-os[data-ui="still-air"] {`)
	lightAt := strings.Index(css, `.vp-os[data-ui="still-air"][data-theme="light"] {`)
	if darkAt < 0 || lightAt < darkAt {
		t.Fatal("vayuos.css has no dark and light token blocks in that order")
	}
	dark, light = sectionTokens(css[darkAt:lightAt]), sectionTokens(css[lightAt:])
	for _, part := range []struct {
		m   map[string]string
		css string
	}{{dark, css[darkAt:lightAt]}, {light, css[lightAt:]}} {
		for _, m := range rgbaTokenRe.FindAllStringSubmatch(part.css, -1) {
			if _, ok := part.m[m[1]]; !ok {
				part.m[m[1]] = m[2]
			}
		}
	}
	return dark, light
}

// The palette as shipped, measured on every surface it sits on. Each text
// colour must clear AA against the canvas, both workspace tones and the
// floating surface; ink must clear it on the fills it labels.
func TestStillAirPaletteClearsAA(t *testing.T) {
	dark, light := stillAirTokens(t)
	surfaces := []string{"--color-canvas", "--surface-1", "--surface-2", "--surface-overlay", "--surface-sunken"}
	texts := []string{"--text-1", "--text-2", "--text-3", "--accent", "--ok", "--warn", "--danger"}
	for _, theme := range []struct {
		name string
		tok  map[string]string
	}{{"graphite", dark}, {"paper", light}} {
		for _, fg := range texts {
			for _, bg := range surfaces {
				f, b := theme.tok[fg], theme.tok[bg]
				if f == "" || b == "" {
					t.Fatalf("%s: token %s or %s missing", theme.name, fg, bg)
				}
				if r := contrastRatio(f, b); r < wcagAANormal {
					t.Errorf("%s: %s %s on %s %s is %.2f:1, below %.1f", theme.name, fg, f, bg, b, r, wcagAANormal)
				}
			}
		}
		// Text on the blended surfaces it really sits on: a selected row, and a
		// tag on its own tint. These are where the first draft failed.
		for _, base := range []string{"--color-canvas", "--surface-1", "--surface-2"} {
			sel := blend(t, theme.tok["--surface-select"], theme.tok[base])
			for _, fg := range []string{"--text-1", "--text-2", "--text-3"} {
				if r := contrastRatio(theme.tok[fg], sel); r < wcagAANormal {
					t.Errorf("%s: %s on a selected row over %s is %.2f:1", theme.name, fg, base, r)
				}
			}
			for _, tone := range [][2]string{{"--accent", "--accent-soft"}, {"--ok", "--ok-soft"}, {"--warn", "--warn-soft"}, {"--danger", "--danger-soft"}} {
				tint := blend(t, theme.tok[tone[1]], theme.tok[base])
				if r := contrastRatio(theme.tok[tone[0]], tint); r < wcagAANormal {
					t.Errorf("%s: a %s tag over %s is %.2f:1", theme.name, tone[0], base, r)
				}
			}
		}
		for _, p := range [][2]string{{"--on-accent", "--accent"}, {"--on-danger", "--danger"}} {
			if r := contrastRatio(theme.tok[p[0]], theme.tok[p[1]]); r < wcagAANormal {
				t.Errorf("%s: %s on %s is %.2f:1", theme.name, p[0], p[1], r)
			}
		}
	}
}

// Every page that links the console stylesheet carries the scope its tokens
// live under. Without data-ui="still-air" on the body the sheet still loads,
// but every var() it reads is unset and the page paints in browser defaults.
func TestEveryConsolePageCarriesTheStillAirScope(t *testing.T) {
	a := &App{torSpace: torspace.New("", t.TempDir()+"/vayupress.db", "", 0)}
	tor := httptest.NewRecorder()
	a.renderTorWorldUnavailable(tor, httptest.NewRequest(http.MethodGet, "/os", nil))
	body := regexp.MustCompile(`<body[^>]*>`)
	for name, page := range map[string]string{
		"console shell": adminOSLayout("N", "Home", "home", saSession(3), "<p>x</p>"),
		"sign-in":       authPageShell("Sign in", "<p>x</p>"),
		"Tor holding":   tor.Body.String(),
	} {
		if !strings.Contains(page, `href="/os/static/css/vayuos.css?v=`) {
			t.Errorf("%s: does not link vayuos.css", name)
		}
		if tag := body.FindString(page); !strings.Contains(tag, `data-ui="still-air"`) {
			t.Errorf("%s: body %s has no data-ui=\"still-air\"; none of the sheet's tokens apply", name, tag)
		}
	}
}

// Every custom property the sheet reads is one it defines. A var() with no
// definition and no fallback is not an error to the browser: the declaration
// silently computes to its inherited or initial value. Two carried classic
// rules read --text-md and --ico-glow, which nothing defined.
func TestConsoleCSSReadsOnlyDefinedProperties(t *testing.T) {
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(readStillAirCSS(t), "")
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`(--[\w-]+)\s*:`).FindAllStringSubmatch(css, -1) {
		defined[m[1]] = true
	}
	for _, m := range regexp.MustCompile(`var\(\s*(--[\w-]+)\s*\)`).FindAllStringSubmatch(css, -1) {
		if !defined[m[1]] {
			t.Errorf("vayuos.css reads %s, which it never defines", m[1])
		}
	}
}

// Reduced motion is honoured by one rule over the whole design, not remembered
// per component.
func TestStillAirHonoursReducedMotion(t *testing.T) {
	css := readStillAirCSS(t)
	// The carried classic rules have reduced-motion blocks of their own for one
	// component each; the one this test holds is the block over everything.
	at := strings.Index(css, "@media (prefers-reduced-motion: reduce) {\n  .vp-os[data-ui=\"still-air\"] *,")
	if at < 0 {
		t.Fatal("vayuos.css has no reduced-motion rule over the whole design")
	}
	block := css[at:]
	if end := strings.Index(block, "\n}\n"); end > 0 {
		block = block[:end]
	}
	for _, need := range []string{"animation-duration: 80ms", "animation-iteration-count: 1", "transition-duration: 80ms"} {
		if !strings.Contains(block, need) {
			t.Errorf("the reduced-motion rule does not contain %q", need)
		}
	}
}

// Sanity for the helper the chrome relies on.
func TestSaInitials(t *testing.T) {
	for in, want := range map[string]string{"Ankush Choudhary Johal": "AC", "élan": "É", "  ": "", "42 Labs": "4L"} {
		if got := saInitials(in); got != want {
			t.Errorf("saInitials(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every refusal the System state page and the strip describe is enforced by a
// mode check in each file the table names. Remove a guard and the page would
// be describing a control that is not there; this fails first.
func TestEveryModeRefusalNamesItsGuard(t *testing.T) {
	constName := map[mode.Mode]string{
		mode.ModeNormal: "ModeNormal", mode.ModeDegraded: "ModeDegraded", mode.ModeReadOnly: "ModeReadOnly",
		mode.ModeRecovery: "ModeRecovery", mode.ModeMaintenance: "ModeMaintenance", mode.ModeQuarantined: "ModeQuarantined",
	}
	for _, e := range saModeEffects {
		if len(e.Guards) == 0 {
			t.Errorf("%q names no guard", e.What)
		}
		for _, g := range e.Guards {
			src, err := os.ReadFile("../../" + g)
			if err != nil {
				t.Errorf("%q: guard file %s: %v", e.What, g, err)
				continue
			}
			code := string(src)
			for _, m := range e.Modes {
				want := "== mode." + constName[m]
				if e.AllBut {
					want = "!= mode." + constName[m]
				}
				is := "Is(mode." + constName[m] + ")"
				if !strings.Contains(code, want) && !strings.Contains(code, `case "`+string(m)+`"`) && (e.AllBut || !strings.Contains(code, is)) {
					t.Errorf("%q claims a %s check in %s, but it has no %q", e.What, m, g, want)
				}
			}
		}
	}
	// And the read-only list carries what the code actually refuses, not the
	// classic page's "WAL writes blocked", which nothing enforces.
	if got := saModeRefusals(mode.ModeReadOnly); len(got) != 9 {
		t.Errorf("read-only refuses %d things in the table, want 9: %v", len(got), got)
	}
	if got := saModeRefusals(mode.ModeNormal); len(got) != 0 {
		t.Errorf("normal mode refuses %v", got)
	}
}

// Home puts what is failing now ahead of what needs attention soon, and both
// ahead of a todo, whatever order the sources were read in. One seed per step.
func TestHomeNeedsSortsWorstFirst(t *testing.T) {
	out := saHomeNeeds([]osNotification{
		{Title: "Comments to review", Detail: "awaiting moderation", Href: "/os/comments", Count: 3, Kind: "comment"},
		{Title: "Storage filling up", Detail: "of your storage quota is in use", Href: "/os/storage", Count: 80, Kind: "storage", Severity: "warn"},
		{Title: "Failed jobs", Detail: "failed", Href: "/os/monitoring", Count: 12, Kind: "jobs", Severity: "danger"},
	})
	danger, warn, todo := strings.Index(out, "Failed jobs"), strings.Index(out, "Storage filling up"), strings.Index(out, "Comments to review")
	if danger < 0 || warn < 0 || todo < 0 {
		t.Fatalf("an item is missing from Home:\n%s", out)
	}
	if danger > warn {
		t.Error("danger must come ahead of warn")
	}
	if warn > todo {
		t.Error("warn must come ahead of a todo")
	}
	if !strings.Contains(out, "80% of your storage quota") {
		t.Error("storage reads as a percentage")
	}
	if got := saHomeNeeds(nil); !strings.Contains(got, "Nothing needs you right now.") || strings.Contains(got, "sa-need ") {
		t.Error("an empty list says so plainly and draws no rows")
	}
}

// The Tor world has its own database and identity; a clearnet section offered
// there opens a page about an install the operator is not in (ADR-0141).
func TestTheTorWorldRailOffersNoClearnetSection(t *testing.T) {
	defer func(v bool) { config.Cfg.OnionMode = v }(config.Cfg.OnionMode)
	config.Cfg.OnionMode = true
	have := map[string]bool{}
	for _, app := range saVisibleApps(saSession(accessAdmin)) {
		have[app.Href] = true
		for _, sec := range app.Sections {
			have[sec.Href] = true
		}
	}
	for _, want := range []string{"/os/posts", "/os/analytics", "/os/vayumail/inbox", "/os/talk", "/os/domains", "/os/theme"} {
		if !have[want] {
			t.Errorf("the Tor world rail lost %s", want)
		}
	}
	for _, deny := range []string{
		"/os/monetization", "/os/ads", "/os/newsletter", "/os/members", "/os/connector", "/os/seo",
		"/os/shield", "/os/tor", "/os/website", "/os/governance", "/os/faults",
	} {
		if have[deny] {
			t.Errorf("the Tor world rail offers the clearnet-only %s", deny)
		}
	}
}

// An operator can leave either world from the system bar. In the Tor world the
// console is a separate instance in OnionMode; it must still offer the
// Clearnet link, or entering Tor is a one-way trip.
func TestTheSystemBarSwitchesWorldsFromEitherWorld(t *testing.T) {
	defer func(v bool) { config.Cfg.OnionMode = v }(config.Cfg.OnionMode)
	for _, c := range []struct {
		onion bool
		href  string
	}{{false, `data-space-switch`}, {true, `href="/os/world?target=clearnet"`}} {
		config.Cfg.OnionMode = c.onion
		out := stillAirShellHead("n", "Home", "dashboard", saSession(accessAdmin))
		bar := out[strings.Index(out, "sa-sysbar"):]
		if !strings.Contains(bar, `class="sa-pop sa-world"`) || !strings.Contains(bar, c.href) {
			t.Errorf("OnionMode=%v: the system bar offers no world switch (want %s); an operator cannot change worlds from here", c.onion, c.href)
		}
	}
}
