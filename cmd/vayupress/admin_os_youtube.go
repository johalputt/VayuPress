// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_youtube.go — Content › YouTube: follow a channel as a blog, and
// see and steer each channel followed (youtube.go does the work).

import (
	"context"
	"errors"
	"html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
	"github.com/johalputt/vayupress/internal/i18n"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/youtube"
)

const youtubePage = "/os/youtube"

// youtubeSite is a site a channel can be followed on; Live is false while a
// hosted site's address does not yet answer here.
type youtubeSite struct {
	ID, Host string
	Live     bool
}

// youtubeNewSite is the site choice that makes a new hosted blog.
const youtubeNewSite = "new"

// youtubeSites is the primary site and every hosted site that is a blog.
func (a *App) youtubeSites(ctx context.Context) []youtubeSite {
	out := []youtubeSite{{ID: "", Host: config.Cfg.Domain, Live: true}}
	if a.domains == nil {
		return out
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return out
	}
	for _, d := range list {
		// What a site serves is its own setting (scopedSiteMode), as the
		// console and the connector read it; the registry's site_type column
		// is what it was created as, and on johal.in it disagreed for every
		// hosted blog, so the list offered none of them.
		// A website with its blog at /blog has posts too.
		if m := scopedSiteMode(d); d.IsPrimary || (m != "blog" && m != "business_subpath") {
			continue
		}
		out = append(out, youtubeSite{ID: d.ID, Host: d.Host, Live: d.TLSState == domain.TLSActive})
	}
	return out
}

func youtubeSiteFor(sites []youtubeSite, id string) (youtubeSite, bool) {
	for _, s := range sites {
		if s.ID == id {
			return s, true
		}
	}
	return youtubeSite{}, false
}

// youtubeNewHost checks the address of a new site: a valid host, and not one
// this install already serves. It returns the host, or why not in words.
func (a *App) youtubeNewHost(ctx context.Context, raw string) (host, refusal string) {
	lang := i18n.Lang(ctx)
	if a.domains == nil {
		return "", i18n.T(lang, "This install cannot host another site.")
	}
	host = domain.NormalizeHost(raw)
	if domain.ValidateHost(host) != nil {
		return "", i18n.T(lang, "That is not a site's address: write it as {example}.", "example", "blog.example.com")
	}
	if host == domain.NormalizeHost(config.Cfg.Domain) {
		return "", i18n.T(lang, "That is this install's own address; choose it from the list.")
	}
	if _, err := a.domains.Resolve(ctx, host); err == nil {
		return "", i18n.T(lang, "That site exists already; choose it from the list.")
	}
	return host, ""
}

func (a *App) handleOSYouTube(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	writeOSHTML(w, r, adminOSLayout(nonce, "YouTube", "content", cfg, htmpl.HTML(a.youtubeBody(r.Context()))))
}

// youtubeBody is the page, in the language of the request it answers.
func (a *App) youtubeBody(ctx context.Context) string {
	lang := i18n.Lang(ctx)
	page := ui.OverviewPage{Title: "YouTube"}
	channels := i18n.T(lang, "Channels")
	if !a.youtubeAvailable() {
		page.State = ui.Text(i18n.T(lang, "Not available here"))
		return string(ui.Overview(page, ui.Band{Title: channels, Aside: ui.Empty("video", i18n.T(lang, "YouTube cannot be reached from here"),
			i18n.T(lang, "This console is the Tor world's, or the install sends nothing out but through Tor, and a channel is read from YouTube by name. Follow channels from the clearnet console."), "")}))
	}
	chans, err := a.youtube.Channels(ctx)
	if err != nil {
		page.State = ui.Text(i18n.T(lang, "The channels could not be read: {error}", "error", err.Error()))
		return string(ui.Overview(page, ui.Band{Title: channels}))
	}
	sites := a.youtubeSites(ctx)
	keyed := a.youtubeKey(ctx) != ""

	posted, waiting, failing := 0, 0, 0
	counts := make([]map[string]int, len(chans))
	for i, f := range chans {
		counts[i], _ = a.youtube.Counts(ctx, f.ID)
		posted += counts[i][youtube.StatePosted] + counts[i][youtube.StateUpcoming]
		waiting += counts[i][youtube.StateHeld]
		if f.Failures >= 3 && !f.Paused {
			failing++
		}
	}
	page.State = ui.Text(i18n.T(lang, "No channel followed yet"))
	if len(chans) > 0 {
		page.State = ui.Text(i18n.T(lang, "{n, plural, one {# channel followed} other {# channels followed}}", "n", len(chans)))
	}
	var figs []ui.Figure
	for _, f := range []struct {
		n     int
		label string
		tone  string
	}{{len(chans), channels, ""}, {posted, i18n.T(lang, "Posts made"), ""}, {waiting, i18n.T(lang, "Waiting for Post"), ""}, {failing, i18n.T(lang, "Cannot be read"), "danger"}} {
		if f.n > 0 {
			figs = append(figs, ui.Figure{Value: strconv.Itoa(f.n), Label: f.label, Tone: f.tone})
		}
	}
	followLabel := i18n.T(lang, "Follow a channel")
	page.Actions = ui.HTML(`<button type="button" class="btn btn--primary btn--sm" data-sheet="yt-follow-sheet">` + html.EscapeString(followLabel) + `</button>`)
	band := ui.Band{Title: channels, Figures: figs}
	if len(figs) == 0 {
		band.Aside = ui.Empty("video", i18n.T(lang, "No channel followed yet"),
			i18n.T(lang, "Paste a channel, and every video it has posted, and every one it posts from now on, becomes a post on your site."),
			ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="yt-follow-sheet">`+html.EscapeString(followLabel)+`</button>`))
	}
	sections := []ui.HTML{ui.SheetClosing("yt-follow-sheet", followLabel, i18n.T(lang, "Close"), ui.HTML(youtubeFollowForm(lang, sites)))}
	for i, f := range chans {
		site, ok := youtubeSiteFor(sites, f.DomainID)
		if !ok {
			site = youtubeSite{Host: i18n.T(lang, "a site no longer served"), Live: true}
		}
		held, _ := a.youtube.Videos(ctx, f.ID, youtube.StateHeld, 50)
		sections = append(sections, youtubeChannelSection(lang, f, site, counts[i], held, keyed))
	}
	keyLine := `<p><strong>` + esc(i18n.T(lang, "No YouTube API key is set.")) + `</strong> ` +
		esc(i18n.T(lang, "A channel is read from its public feeds: its newest videos and Shorts when it is first followed, and every new one after.")) + ` ` +
		escLinked(i18n.T(lang, "{link} to bring the whole back catalogue too, and to post premieres and streams before they start.", "link", linkMark), "/os/apikeys", i18n.T(lang, "Add a YouTube API key")) + `</p>`
	if keyed {
		keyLine = `<p><strong>` + esc(i18n.T(lang, "A YouTube API key is set.")) + `</strong> ` +
			esc(i18n.T(lang, "Each channel's back catalogue is read a few pages at a time, and premieres and streams are posted before they start, then rewritten as the video once they have aired.")) + `</p>`
	}
	sections = append(sections, ui.ExplainTitled(i18n.T(lang, "How this works"), ui.HTML(keyLine+
		`<p>`+html.EscapeString(i18n.T(lang, "Each channel is checked every 15 minutes. A post holds the video's title, its description with its links and timestamps, its hashtags as tags, its picture copied into your media library, and the player, which loads from YouTube only when a reader presses play. A Short is drawn upright. A video is posted once: a post you delete is not made again."))+`</p>`+
		`<p>`+html.EscapeString(i18n.T(lang, "Follow a channel you own or may republish. The posts show the video through YouTube's own player and link back to it."))+`</p>`)))
	return string(ui.Overview(page, band, sections...))
}

// youtubeFollowForm is the address field and site choice; its answer is the
// channel to confirm, drawn under it.
func youtubeFollowForm(lang string, sites []youtubeSite) string {
	var b strings.Builder
	b.WriteString(`<form class="yt-follow" hx-post="/os/api/youtube/resolve" hx-target="#yt-preview" hx-swap="innerHTML">`)
	b.WriteString(`<div class="field"><label class="field-label" for="yt-address">` + esc(i18n.T(lang, "Channel or video address")) + `</label>` +
		`<input type="text" class="input" id="yt-address" name="address" required autocomplete="off" spellcheck="false" placeholder="youtube.com/@channel"></div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="yt-site">` + esc(i18n.T(lang, "Post on")) + `</label><select class="input" id="yt-site" name="site">`)
	for _, s := range sites {
		b.WriteString(`<option value="` + html.EscapeString(s.ID) + `" translate="no">` + html.EscapeString(s.Host) + `</option>`)
	}
	b.WriteString(`<option value="` + youtubeNewSite + `">` + esc(i18n.T(lang, "A new site…")) + `</option></select></div>`)
	b.WriteString(`<div class="field"><label class="field-label" for="yt-new-host">` + esc(i18n.T(lang, "The new site's address")) + `</label>` +
		`<input type="text" class="input" id="yt-new-host" name="new_host" autocomplete="off" spellcheck="false" placeholder="videos.example.com">` +
		`<p class="field-hint">` + esc(i18n.T(lang, "Only for a new site. It is made with the channel's posts on it, and answers once its address points to this server ({where}).", "where", i18n.T(lang, "Site › Domains"))) + `</p></div>`)
	b.WriteString(`<button class="btn btn--primary btn--sm" type="submit">` + esc(i18n.T(lang, "Find the channel")) + `</button></form><div id="yt-preview" aria-live="polite"></div>`)
	return b.String()
}

// youtubeChannelSection is one followed channel: what it has done, its
// switches and its actions, and the videos waiting for Post.
func youtubeChannelSection(lang string, f youtube.Followed, site youtubeSite, counts map[string]int, held []youtube.Row, keyed bool) ui.HTML {
	id := html.EscapeString(f.ID)
	title := f.Title
	if title == "" {
		title = f.ChannelID
	}
	stamp := func(t time.Time) string {
		at := config.InSite(t)
		return i18n.T(lang, "{date}, {time}", "date", i18n.Date(lang, at, i18n.DayMonthShort), "time", at.Format("15:04"))
	}
	last := i18n.T(lang, "Not checked yet")
	switch {
	case f.Paused:
		last = i18n.T(lang, "Paused")
	case !f.LastCheck.IsZero() && f.LastError != "":
		last = i18n.T(lang, "Last check failed {when}", "when", stamp(f.LastCheck))
	case !f.LastCheck.IsZero():
		last = i18n.T(lang, "Checked {when}", "when", stamp(f.LastCheck))
		if f.LastFound > 0 {
			last = i18n.T(lang, "{n, plural, one {Checked {when}, # new video} other {Checked {when}, # new videos}}", "n", f.LastFound, "when", stamp(f.LastCheck))
		}
	}
	facts := []ui.Fact{
		{Key: i18n.T(lang, "Posts"), Value: ui.Text(strconv.Itoa(counts[youtube.StatePosted] + counts[youtube.StateUpcoming]))},
		{Key: i18n.T(lang, "Last check"), Value: ui.Text(last)},
	}
	if !f.Paused {
		facts = append(facts, ui.Fact{Key: i18n.T(lang, "Next check"), Value: ui.Text(youtubeNext(lang, f.NextCheck))})
	}
	if f.LastError != "" && !f.Paused {
		// The reason is what the reading recorded, in English: it is the
		// engine's account of the check, written when it ran.
		facts = append(facts, ui.Fact{Key: i18n.T(lang, "Why it failed"), Value: ui.HTML(`<span class="badge badge--danger" translate="no">` + html.EscapeString(f.LastError) + `</span>`)})
	}
	if keyed {
		bf := i18n.T(lang, "Read whole")
		if !f.BackfillDone {
			bf = i18n.T(lang, "{seen} of {total} read so far", "seen", f.BackfillSeen, "total", f.BackfillTotal)
			if f.BackfillTotal == 0 {
				bf = i18n.T(lang, "Starts at the next check")
			}
		}
		facts = append(facts, ui.Fact{Key: i18n.T(lang, "Back catalogue"), Value: ui.Text(bf)})
	}
	if n := counts[youtube.StateSkipped]; n > 0 {
		facts = append(facts, ui.Fact{Key: i18n.T(lang, "Shorts left out"), Value: ui.Text(strconv.Itoa(n))})
	}
	toggle := func(field, label, hint string, on bool) ui.Row {
		checked := ""
		if on {
			checked = " checked"
		}
		cid := "yt-" + field + "-" + id
		return ui.Row{Label: label, Hint: hint, ID: cid, Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="` + cid + `" name="on" value="1"` + checked +
			` hx-post="/os/api/youtube/` + id + `/set?f=` + field + `" hx-trigger="change" hx-swap="none">`)}
	}
	rows := ui.Rows(
		toggle("auto", i18n.T(lang, "Auto-post"), i18n.T(lang, "New videos become posts by themselves. Off, they wait below for Post."), f.AutoPost),
		toggle("publish", i18n.T(lang, "Publish at once"), i18n.T(lang, "Off, each post is kept as a draft for you to publish."), !f.AsDraft),
		toggle("shorts", i18n.T(lang, "Shorts"), i18n.T(lang, "Make posts of the channel's Shorts too."), f.Shorts),
	)
	pause := `<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/youtube/` + id + `/set?f=paused" hx-vals='{"on":"1"}' hx-swap="none">` + esc(i18n.T(lang, "Pause")) + `</button>`
	if f.Paused {
		pause = `<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/youtube/` + id + `/set?f=paused" hx-swap="none">` + esc(i18n.T(lang, "Resume")) + `</button>`
	}
	actions := `<div class="yt-actions">` +
		`<button type="button" class="btn btn--sm" hx-post="/os/api/youtube/` + id + `/check" hx-swap="none">` + esc(i18n.T(lang, "Fetch now")) + `</button>` + pause +
		`<a class="btn btn--ghost btn--sm" href="` + html.EscapeString(youtube.ChannelURL(f.ChannelID)) + `" target="_blank" rel="noopener noreferrer">` + esc(i18n.T(lang, "Open on YouTube")) + `</a>` +
		`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/youtube/` + id + `/unfollow" hx-swap="none" hx-confirm="` +
		html.EscapeString(i18n.T(lang, "Stop following {channel}? Its posts stay.", "channel", title)) + `">` + esc(i18n.T(lang, "Stop following")) + `</button></div>`
	body := string(ui.Facts(facts...)) + string(rows) + actions
	if len(held) > 0 {
		postLabel := esc(i18n.T(lang, "Post"))
		trs := make([][]ui.HTML, 0, len(held))
		for _, v := range held {
			kind := i18n.T(lang, "Video")
			if v.Kind == youtube.KindShort {
				kind = i18n.T(lang, "Short")
			}
			trs = append(trs, []ui.HTML{
				ui.HTML(`<span translate="no">` + html.EscapeString(v.Title) + `</span>`), ui.Text(kind), ui.Text(i18n.Date(lang, config.InSite(v.Published), i18n.DayMonthYearShort)),
				ui.HTML(`<button type="button" class="btn btn--sm" hx-post="/os/api/youtube/` + id + `/post" hx-vals='{"video":"` + html.EscapeString(v.VideoID) + `"}' hx-swap="none">` + postLabel + `</button>`),
			})
		}
		body += string(ui.Section(i18n.T(lang, "Waiting for Post"), i18n.T(lang, "{n, plural, one {# video} other {# videos}}", "n", len(held)), ui.Join(
			ui.Table([]string{i18n.T(lang, "Video"), i18n.T(lang, "Kind"), i18n.T(lang, "On YouTube"), ""}, trs, ""),
			ui.HTML(`<div class="yt-actions"><button type="button" class="btn btn--sm" hx-post="/os/api/youtube/`+id+`/post-all" hx-swap="none">`+esc(i18n.T(lang, "Post all"))+`</button></div>`))))
	}
	if !site.Live {
		body = string(ui.Callout("info", ui.HTML(escLinked(i18n.T(lang, "The posts are made, and {site} shows them once its address points to this server and its certificate is issued: {link}.", "site", site.Host, "link", linkMark),
			"/os/domains", i18n.T(lang, "Site › Domains"))))) + body
	}
	return ui.Section(title, i18n.T(lang, "On {site}", "site", site.Host), ui.HTML(body))
}

// youtubeNext says when a channel is next checked.
func youtubeNext(lang string, t time.Time) string {
	d := time.Until(t)
	switch {
	case d <= time.Minute:
		return i18n.T(lang, "Within a minute")
	case d < time.Hour:
		return i18n.T(lang, "In {n} min", "n", int(d.Minutes()))
	case d < 48*time.Hour:
		return i18n.T(lang, "In {n} h", "n", int(d.Hours()))
	}
	at := config.InSite(t)
	return i18n.T(lang, "{date}, {time}", "date", i18n.Date(lang, at, i18n.DayMonthShort), "time", at.Format("15:04"))
}

// youtubeError answers an action that could not be done, in words.
func youtubeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeAPIError(w, r, status, "youtube", msg, "")
}

// youtubeWords says a resolve failure the way an operator can act on. What
// YouTube itself answered is quoted as it came.
func youtubeWords(lang string, err error) string {
	var ae *youtube.APIError
	switch {
	case errors.Is(err, youtube.ErrNotYouTube):
		return i18n.T(lang, "That address is not on YouTube.")
	case errors.Is(err, youtube.ErrUnrecognised):
		return i18n.T(lang, "That address names no YouTube channel or video. Paste the channel's page, @handle, or any of its videos.")
	case errors.Is(err, youtube.ErrConsent):
		return err.Error() + "."
	case errors.As(err, &ae) && ae.QuotaSpent():
		return i18n.T(lang, "The YouTube API key has spent today's quota; it is renewed at midnight Pacific time.")
	case errors.As(err, &ae):
		return i18n.T(lang, "{error}. Check the key on API keys.", "error", ae.Error())
	}
	return i18n.T(lang, "Reading the channel from YouTube failed: {error}", "error", err.Error())
}

// handleOSYouTubeResolve finds the channel an address names and draws it for
// the operator to confirm, with the switches it will be followed with.
func (a *App) handleOSYouTubeResolve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	lang := i18n.Lang(r.Context())
	if !a.youtubeAvailable() {
		_, _ = w.Write([]byte(ui.Callout("warn", ui.Text(i18n.T(lang, "YouTube cannot be reached from here.")))))
		return
	}
	site, host, newHost := r.PostFormValue("site"), "", ""
	if site == youtubeNewSite {
		h, refusal := a.youtubeNewHost(r.Context(), r.PostFormValue("new_host"))
		if refusal != "" {
			_, _ = w.Write([]byte(ui.Callout("warn", ui.Text(refusal))))
			return
		}
		host, newHost = h, h
	} else {
		s, ok := youtubeSiteFor(a.youtubeSites(r.Context()), site)
		if !ok {
			_, _ = w.Write([]byte(ui.Callout("warn", ui.Text(i18n.T(lang, "Choose a site to post on.")))))
			return
		}
		host = s.Host
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ch, vids, err := a.youtubeFollower().Resolve(ctx, r.PostFormValue("address"))
	if err != nil {
		_, _ = w.Write([]byte(ui.Callout("warn", ui.Text(youtubeWords(lang, err)))))
		return
	}
	what := i18n.T(lang, "{n, plural, one {# video in its feed now} other {# videos in its feed now}}", "n", len(vids))
	if ch.Videos > 0 {
		what = i18n.T(lang, "{n, plural, one {# video on the channel} other {# videos on the channel}}", "n", ch.Videos)
	}
	var newest []string
	for i, v := range vids {
		if i == 3 {
			break
		}
		newest = append(newest, `<li translate="no">`+html.EscapeString(v.Title)+`</li>`)
	}
	var b strings.Builder
	b.WriteString(`<div class="yt-confirm"><p><strong translate="no">` + html.EscapeString(ch.Title) + `</strong> · ` + html.EscapeString(what) + `</p>`)
	if len(newest) > 0 {
		b.WriteString(`<ul class="yt-newest">` + strings.Join(newest, "") + `</ul>`)
	}
	b.WriteString(`<form hx-post="/os/api/youtube/follow" hx-swap="none">` +
		`<input type="hidden" name="channel" value="` + html.EscapeString(ch.ID) + `"><input type="hidden" name="site" value="` + html.EscapeString(site) + `">` +
		`<input type="hidden" name="new_host" value="` + html.EscapeString(newHost) + `">` +
		string(ui.Rows(
			ui.Row{Label: i18n.T(lang, "Auto-post"), Hint: i18n.T(lang, "Every video, and every new one, becomes a post by itself."), ID: "yt-new-auto",
				Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="yt-new-auto" name="auto" value="1" checked>`)},
			ui.Row{Label: i18n.T(lang, "Publish at once"), Hint: i18n.T(lang, "Off, each post is kept as a draft."), ID: "yt-new-publish",
				Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="yt-new-publish" name="publish" value="1" checked>`)},
			ui.Row{Label: i18n.T(lang, "Shorts"), Hint: i18n.T(lang, "Make posts of its Shorts too."), ID: "yt-new-shorts",
				Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="yt-new-shorts" name="shorts" value="1" checked>`)},
		)) +
		`<button class="btn btn--primary btn--sm" type="submit">` + html.EscapeString(i18n.T(lang, "Follow on {site}", "site", host)) + `</button></form></div>`)
	_, _ = w.Write([]byte(b.String()))
}

// handleOSYouTubeFollow follows a confirmed channel and checks it at once.
func (a *App) handleOSYouTubeFollow(w http.ResponseWriter, r *http.Request) {
	if !a.youtubeAvailable() {
		youtubeError(w, r, http.StatusServiceUnavailable, i18n.T(i18n.Lang(r.Context()), "YouTube cannot be reached from here."))
		return
	}
	id := r.PostFormValue("channel")
	if !youtube.ValidChannelID(id) {
		youtubeError(w, r, http.StatusBadRequest, i18n.T(i18n.Lang(r.Context()), "That is not a channel."))
		return
	}
	site := r.PostFormValue("site")
	newHost := ""
	if site == youtubeNewSite {
		h, refusal := a.youtubeNewHost(r.Context(), r.PostFormValue("new_host"))
		if refusal != "" {
			youtubeError(w, r, http.StatusBadRequest, refusal)
			return
		}
		newHost = h
	} else if _, ok := youtubeSiteFor(a.youtubeSites(r.Context()), site); !ok {
		youtubeError(w, r, http.StatusBadRequest, i18n.T(i18n.Lang(r.Context()), "Choose a site to post on."))
		return
	}
	// The name is read again from the channel itself, never taken from the
	// form, and reading it is what proves the channel is there; a new site is
	// made only after that.
	ch, _, err := a.youtubeFollower().Resolve(r.Context(), id)
	if err != nil {
		youtubeError(w, r, http.StatusBadGateway, youtubeWords(i18n.Lang(r.Context()), err))
		return
	}
	if newHost != "" {
		d, err := a.domains.Create(r.Context(), newHost, domain.SiteBlog, false)
		if err != nil {
			youtubeError(w, r, http.StatusBadRequest, i18n.T(i18n.Lang(r.Context()), "The site could not be made: {error}", "error", err.Error()))
			return
		}
		dbpkg.AuditLog("vayudomains.create", dbpkg.AuditActor(r), d.Host, "for YouTube channel "+ch.ID)
		site = d.ID
	}
	on := func(k string) bool { return r.PostFormValue(k) == "1" }
	_, err = a.youtube.Follow(r.Context(), site, ch, on("auto"), !on("publish"), on("shorts"))
	if errors.Is(err, youtube.ErrFollowed) {
		youtubeError(w, r, http.StatusConflict, i18n.T(i18n.Lang(r.Context()), "That site follows this channel already."))
		return
	}
	if err != nil {
		youtubeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	dbpkg.AuditLog("youtube.follow", dbpkg.AuditActor(r), ch.ID, "site="+site+" title="+ch.Title)
	a.pokeYouTube()
	signinDone(w, r)
}

// youtubeChannelFor is the followed channel the path names.
func (a *App) youtubeChannelFor(w http.ResponseWriter, r *http.Request) (youtube.Followed, bool) {
	if a.youtube == nil {
		youtubeError(w, r, http.StatusServiceUnavailable, i18n.T(i18n.Lang(r.Context()), "YouTube cannot be reached from here."))
		return youtube.Followed{}, false
	}
	f, err := a.youtube.Channel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		youtubeError(w, r, http.StatusNotFound, i18n.T(i18n.Lang(r.Context()), "No such channel is followed."))
		return youtube.Followed{}, false
	}
	return f, true
}

// handleOSYouTubeSet turns one of a channel's switches on or off.
func (a *App) handleOSYouTubeSet(w http.ResponseWriter, r *http.Request) {
	f, ok := a.youtubeChannelFor(w, r)
	if !ok {
		return
	}
	on := r.PostFormValue("on") == "1"
	switch r.URL.Query().Get("f") {
	case "auto":
		f.AutoPost = on
	case "publish":
		f.AsDraft = !on
	case "shorts":
		f.Shorts = on
	case "paused":
		f.Paused = on
	default:
		youtubeError(w, r, http.StatusBadRequest, i18n.T(i18n.Lang(r.Context()), "Say which switch."))
		return
	}
	if err := a.youtube.Configure(r.Context(), f.ID, f.AutoPost, f.AsDraft, f.Shorts, f.Paused); err != nil {
		youtubeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	dbpkg.AuditLog("youtube.set", dbpkg.AuditActor(r), f.ChannelID, r.URL.Query().Get("f")+"="+strconv.FormatBool(on))
	if r.URL.Query().Get("f") == "paused" {
		if !on {
			_ = a.youtube.CheckNow(r.Context(), f.ID)
			a.pokeYouTube()
		}
		signinDone(w, r)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSYouTubeCheck checks a channel at once: Fetch now.
func (a *App) handleOSYouTubeCheck(w http.ResponseWriter, r *http.Request) {
	f, ok := a.youtubeChannelFor(w, r)
	if !ok {
		return
	}
	if err := a.youtube.CheckNow(r.Context(), f.ID); err != nil {
		youtubeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	a.pokeYouTube()
	signinDone(w, r)
}

// handleOSYouTubeUnfollow stops following a channel; its posts stay.
func (a *App) handleOSYouTubeUnfollow(w http.ResponseWriter, r *http.Request) {
	f, ok := a.youtubeChannelFor(w, r)
	if !ok {
		return
	}
	if err := a.youtube.Unfollow(r.Context(), f.ID); err != nil {
		youtubeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	dbpkg.AuditLog("youtube.unfollow", dbpkg.AuditActor(r), f.ChannelID, f.Title)
	signinDone(w, r)
}

// handleOSYouTubePost makes the post of one video waiting for Post.
func (a *App) handleOSYouTubePost(w http.ResponseWriter, r *http.Request) {
	f, ok := a.youtubeChannelFor(w, r)
	if !ok {
		return
	}
	v := r.PostFormValue("video")
	if !youtube.ValidVideoID(v) {
		youtubeError(w, r, http.StatusBadRequest, i18n.T(i18n.Lang(r.Context()), "That is not a video."))
		return
	}
	if err := a.youtubeFollower().PostHeld(r.Context(), f, v); err != nil {
		youtubeError(w, r, http.StatusConflict, err.Error())
		return
	}
	signinDone(w, r)
}

// handleOSYouTubePostAll makes the post of every video waiting for Post.
func (a *App) handleOSYouTubePostAll(w http.ResponseWriter, r *http.Request) {
	f, ok := a.youtubeChannelFor(w, r)
	if !ok {
		return
	}
	held, err := a.youtube.Videos(r.Context(), f.ID, youtube.StateHeld, 500)
	if err != nil {
		youtubeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	fw := a.youtubeFollower()
	for _, v := range held {
		if err := fw.PostHeld(r.Context(), f, v.VideoID); err != nil {
			youtubeError(w, r, http.StatusConflict, err.Error())
			return
		}
	}
	signinDone(w, r)
}
