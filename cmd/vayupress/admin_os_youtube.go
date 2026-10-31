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
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
	"github.com/johalputt/vayupress/internal/youtube"
)

const youtubePage = "/os/youtube"

// youtubeSite is a site a channel can be followed on.
type youtubeSite struct{ ID, Host string }

// youtubeSites is the primary site and every hosted site that is a blog.
func (a *App) youtubeSites(ctx context.Context) []youtubeSite {
	out := []youtubeSite{{ID: "", Host: config.Cfg.Domain}}
	if a.domains == nil {
		return out
	}
	list, err := a.domains.List(ctx)
	if err != nil {
		return out
	}
	for _, d := range list {
		if d.IsPrimary || (d.SiteType != "" && d.SiteType != domain.SiteBlog) {
			continue
		}
		out = append(out, youtubeSite{ID: d.ID, Host: d.Host})
	}
	return out
}

func (a *App) youtubeSiteHost(sites []youtubeSite, id string) (string, bool) {
	for _, s := range sites {
		if s.ID == id {
			return s.Host, true
		}
	}
	return "", false
}

func (a *App) handleOSYouTube(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	writeOSHTML(w, r, adminOSLayout(nonce, "YouTube", "content", cfg, htmpl.HTML(a.youtubeBody(r.Context()))))
}

func (a *App) youtubeBody(ctx context.Context) string {
	page := ui.OverviewPage{Title: "YouTube"}
	if !a.youtubeAvailable() {
		page.State = ui.Text("Not available here")
		return string(ui.Overview(page, ui.Band{Title: "Channels", Aside: ui.Empty("video", "YouTube cannot be reached from here",
			"This console is the Tor world's, or the install sends nothing out but through Tor, and a channel is read from YouTube by name. Follow channels from the clearnet console.", "")}))
	}
	chans, err := a.youtube.Channels(ctx)
	if err != nil {
		page.State = ui.Text("The channels could not be read: " + err.Error())
		return string(ui.Overview(page, ui.Band{Title: "Channels"}))
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
	page.State = ui.Text("No channel followed yet")
	if len(chans) > 0 {
		page.State = ui.Text(countOf(len(chans), "channel") + " followed")
	}
	var figs []ui.Figure
	for _, f := range []struct {
		n     int
		label string
		tone  string
	}{{len(chans), "Channels", ""}, {posted, "Posts made", ""}, {waiting, "Waiting for Post", ""}, {failing, "Cannot be read", "danger"}} {
		if f.n > 0 {
			figs = append(figs, ui.Figure{Value: strconv.Itoa(f.n), Label: f.label, Tone: f.tone})
		}
	}
	follow := ui.HTML(`<button type="button" class="btn btn--primary btn--sm" data-sheet="yt-follow-sheet">Follow a channel</button>`)
	page.Actions = follow
	band := ui.Band{Title: "Channels", Figures: figs}
	if len(figs) == 0 {
		band.Aside = ui.Empty("video", "No channel followed yet",
			"Paste a channel, and every video it has posted, and every one it posts from now on, becomes a post on your site.",
			ui.HTML(`<button type="button" class="btn btn--sm" data-sheet="yt-follow-sheet">Follow a channel</button>`))
	}
	sections := []ui.HTML{ui.Sheet("yt-follow-sheet", "Follow a channel", ui.HTML(youtubeFollowForm(sites)))}
	for i, f := range chans {
		host, _ := a.youtubeSiteHost(sites, f.DomainID)
		held, _ := a.youtube.Videos(ctx, f.ID, youtube.StateHeld, 50)
		sections = append(sections, youtubeChannelSection(f, host, counts[i], held, keyed))
	}
	keyLine := `<p><strong>Without a YouTube API key</strong>, a channel is read from its public feeds: its newest videos and Shorts when it is first followed, and every new one after. <a href="/os/apikeys">Add a key</a> to bring the whole back catalogue too, and to post premieres and streams before they start.</p>`
	if keyed {
		keyLine = `<p><strong>A YouTube API key is set.</strong> Each channel's back catalogue is read a few pages at a time, and premieres and streams are posted before they start, then rewritten as the video once they have aired.</p>`
	}
	sections = append(sections, ui.Explain(ui.HTML(keyLine+
		`<p>Each channel is checked every 15 minutes. A post holds the video's title, its description with its links and timestamps, its hashtags as tags, its picture copied into your media library, and the player, which loads from YouTube only when a reader presses play. A Short is drawn upright. A video is posted once: a post you delete is not made again.</p>`+
		`<p>Follow a channel you own or may republish. The posts show the video through YouTube's own player and link back to it.</p>`)))
	return string(ui.Overview(page, band, sections...))
}

// youtubeFollowForm is the address field and site choice; its answer is the
// channel to confirm, drawn under it.
func youtubeFollowForm(sites []youtubeSite) string {
	var b strings.Builder
	b.WriteString(`<form class="yt-follow" hx-post="/os/api/youtube/resolve" hx-target="#yt-preview" hx-swap="innerHTML">`)
	b.WriteString(`<div class="field"><label class="field-label" for="yt-address">Channel or video address</label>` +
		`<input type="text" class="input" id="yt-address" name="address" required autocomplete="off" spellcheck="false" placeholder="youtube.com/@channel"></div>`)
	if len(sites) > 1 {
		b.WriteString(`<div class="field"><label class="field-label" for="yt-site">Post on</label><select class="input" id="yt-site" name="site">`)
		for _, s := range sites {
			b.WriteString(`<option value="` + html.EscapeString(s.ID) + `">` + html.EscapeString(s.Host) + `</option>`)
		}
		b.WriteString(`</select></div>`)
	}
	b.WriteString(`<button class="btn btn--primary btn--sm" type="submit">Find the channel</button></form><div id="yt-preview" aria-live="polite"></div>`)
	return b.String()
}

// youtubeChannelSection is one followed channel: what it has done, its
// switches and its actions, and the videos waiting for Post.
func youtubeChannelSection(f youtube.Followed, host string, counts map[string]int, held []youtube.Row, keyed bool) ui.HTML {
	id := html.EscapeString(f.ID)
	title := f.Title
	if title == "" {
		title = f.ChannelID
	}
	last := "Not checked yet"
	switch {
	case f.Paused:
		last = "Paused"
	case !f.LastCheck.IsZero() && f.LastError != "":
		last = "Last check failed " + config.FormatSite(f.LastCheck, "2 Jan 15:04")
	case !f.LastCheck.IsZero():
		last = "Checked " + config.FormatSite(f.LastCheck, "2 Jan 15:04")
		if f.LastFound > 0 {
			last += ", " + countOf(f.LastFound, "new video")
		}
	}
	facts := []ui.Fact{
		{Key: "Posts", Value: ui.Text(strconv.Itoa(counts[youtube.StatePosted] + counts[youtube.StateUpcoming]))},
		{Key: "Last check", Value: ui.Text(last)},
	}
	if !f.Paused {
		facts = append(facts, ui.Fact{Key: "Next check", Value: ui.Text(youtubeNext(f.NextCheck))})
	}
	if f.LastError != "" && !f.Paused {
		facts = append(facts, ui.Fact{Key: "Why it failed", Value: ui.HTML(`<span class="badge badge--danger">` + html.EscapeString(f.LastError) + `</span>`)})
	}
	if keyed {
		bf := "Read whole"
		if !f.BackfillDone {
			bf = strconv.Itoa(f.BackfillSeen) + " of " + strconv.Itoa(f.BackfillTotal) + " read so far"
			if f.BackfillTotal == 0 {
				bf = "Starts at the next check"
			}
		}
		facts = append(facts, ui.Fact{Key: "Back catalogue", Value: ui.Text(bf)})
	}
	if n := counts[youtube.StateSkipped]; n > 0 {
		facts = append(facts, ui.Fact{Key: "Shorts left out", Value: ui.Text(strconv.Itoa(n))})
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
		toggle("auto", "Auto-post", "New videos become posts by themselves. Off, they wait below for Post.", f.AutoPost),
		toggle("publish", "Publish at once", "Off, each post is kept as a draft for you to publish.", !f.AsDraft),
		toggle("shorts", "Shorts", "Make posts of the channel's Shorts too.", f.Shorts),
	)
	pause := `<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/youtube/` + id + `/set?f=paused" hx-vals='{"on":"1"}' hx-swap="none">Pause</button>`
	if f.Paused {
		pause = `<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/youtube/` + id + `/set?f=paused" hx-swap="none">Resume</button>`
	}
	actions := `<div class="yt-actions">` +
		`<button type="button" class="btn btn--sm" hx-post="/os/api/youtube/` + id + `/check" hx-swap="none">Fetch now</button>` + pause +
		`<a class="btn btn--ghost btn--sm" href="` + html.EscapeString(youtube.ChannelURL(f.ChannelID)) + `" target="_blank" rel="noopener noreferrer">Open on YouTube</a>` +
		`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/youtube/` + id + `/unfollow" hx-swap="none" hx-confirm="Stop following ` + html.EscapeString(title) + `? Its posts stay.">Stop following</button></div>`
	body := string(ui.Facts(facts...)) + string(rows) + actions
	if len(held) > 0 {
		trs := make([][]ui.HTML, 0, len(held))
		for _, v := range held {
			kind := "Video"
			if v.Kind == youtube.KindShort {
				kind = "Short"
			}
			trs = append(trs, []ui.HTML{
				ui.Text(v.Title), ui.Text(kind), ui.Text(config.FormatSite(v.Published, "2 Jan 2006")),
				ui.HTML(`<button type="button" class="btn btn--sm" hx-post="/os/api/youtube/` + id + `/post" hx-vals='{"video":"` + html.EscapeString(v.VideoID) + `"}' hx-swap="none">Post</button>`),
			})
		}
		body += string(ui.Section("Waiting for Post", countOf(len(held), "video"), ui.Join(
			ui.Table([]string{"Video", "Kind", "On YouTube", ""}, trs, ""),
			ui.HTML(`<div class="yt-actions"><button type="button" class="btn btn--sm" hx-post="/os/api/youtube/`+id+`/post-all" hx-swap="none">Post all</button></div>`))))
	}
	return ui.Section(title, "On "+host, ui.HTML(body))
}

// youtubeNext says when a channel is next checked.
func youtubeNext(t time.Time) string {
	d := time.Until(t)
	switch {
	case d <= time.Minute:
		return "Within a minute"
	case d < time.Hour:
		return "In " + strconv.Itoa(int(d.Minutes())) + " min"
	case d < 48*time.Hour:
		return "In " + strconv.Itoa(int(d.Hours())) + " h"
	}
	return config.FormatSite(t, "2 Jan 15:04")
}

// youtubeError answers an action that could not be done, in words.
func youtubeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeAPIError(w, r, status, "youtube", msg, "")
}

// youtubeWords says a resolve failure the way an operator can act on.
func youtubeWords(err error) string {
	var ae *youtube.APIError
	switch {
	case errors.Is(err, youtube.ErrNotYouTube):
		return "That address is not on YouTube."
	case errors.Is(err, youtube.ErrUnrecognised):
		return "That address names no YouTube channel or video. Paste the channel's page, @handle, or any of its videos."
	case errors.Is(err, youtube.ErrConsent):
		return err.Error() + "."
	case errors.As(err, &ae) && ae.QuotaSpent():
		return "The YouTube API key has spent today's quota; it is renewed at midnight Pacific time."
	case errors.As(err, &ae):
		return ae.Error() + ". Check the key on API keys."
	}
	return "YouTube could not be reached: " + err.Error()
}

// handleOSYouTubeResolve finds the channel an address names and draws it for
// the operator to confirm, with the switches it will be followed with.
func (a *App) handleOSYouTubeResolve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !a.youtubeAvailable() {
		_, _ = w.Write([]byte(ui.Callout("warn", ui.Text("YouTube cannot be reached from here."))))
		return
	}
	sites := a.youtubeSites(r.Context())
	site := r.PostFormValue("site")
	host, ok := a.youtubeSiteHost(sites, site)
	if !ok {
		_, _ = w.Write([]byte(ui.Callout("warn", ui.Text("Choose a site to post on."))))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	ch, vids, err := a.youtubeFollower().Resolve(ctx, r.PostFormValue("address"))
	if err != nil {
		_, _ = w.Write([]byte(ui.Callout("warn", ui.Text(youtubeWords(err)))))
		return
	}
	n := len(vids)
	what := countOf(n, "video") + " in its feed now"
	if ch.Videos > 0 {
		what = countOf(ch.Videos, "video") + " on the channel"
	}
	var newest []string
	for i, v := range vids {
		if i == 3 {
			break
		}
		newest = append(newest, `<li>`+html.EscapeString(v.Title)+`</li>`)
	}
	var b strings.Builder
	b.WriteString(`<div class="yt-confirm"><p><strong>` + html.EscapeString(ch.Title) + `</strong> · ` + html.EscapeString(what) + `</p>`)
	if len(newest) > 0 {
		b.WriteString(`<ul class="yt-newest">` + strings.Join(newest, "") + `</ul>`)
	}
	b.WriteString(`<form hx-post="/os/api/youtube/follow" hx-swap="none">` +
		`<input type="hidden" name="channel" value="` + html.EscapeString(ch.ID) + `"><input type="hidden" name="site" value="` + html.EscapeString(site) + `">` +
		string(ui.Rows(
			ui.Row{Label: "Auto-post", Hint: "Every video, and every new one, becomes a post by itself.", ID: "yt-new-auto",
				Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="yt-new-auto" name="auto" value="1" checked>`)},
			ui.Row{Label: "Publish at once", Hint: "Off, each post is kept as a draft.", ID: "yt-new-publish",
				Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="yt-new-publish" name="publish" value="1" checked>`)},
			ui.Row{Label: "Shorts", Hint: "Make posts of its Shorts too.", ID: "yt-new-shorts",
				Control: ui.HTML(`<input type="checkbox" class="toggle" role="switch" id="yt-new-shorts" name="shorts" value="1" checked>`)},
		)) +
		`<button class="btn btn--primary btn--sm" type="submit">Follow on ` + html.EscapeString(host) + `</button></form></div>`)
	_, _ = w.Write([]byte(b.String()))
}

// handleOSYouTubeFollow follows a confirmed channel and checks it at once.
func (a *App) handleOSYouTubeFollow(w http.ResponseWriter, r *http.Request) {
	if !a.youtubeAvailable() {
		youtubeError(w, r, http.StatusServiceUnavailable, "YouTube cannot be reached from here.")
		return
	}
	id := r.PostFormValue("channel")
	if !youtube.ValidChannelID(id) {
		youtubeError(w, r, http.StatusBadRequest, "That is not a channel.")
		return
	}
	site := r.PostFormValue("site")
	if _, ok := a.youtubeSiteHost(a.youtubeSites(r.Context()), site); !ok {
		youtubeError(w, r, http.StatusBadRequest, "Choose a site to post on.")
		return
	}
	// The name is read again from the channel itself, never taken from the
	// form, and reading it is what proves the channel is there.
	ch, _, err := a.youtubeFollower().Resolve(r.Context(), id)
	if err != nil {
		youtubeError(w, r, http.StatusBadGateway, youtubeWords(err))
		return
	}
	on := func(k string) bool { return r.PostFormValue(k) == "1" }
	_, err = a.youtube.Follow(r.Context(), site, ch, on("auto"), !on("publish"), on("shorts"))
	if errors.Is(err, youtube.ErrFollowed) {
		youtubeError(w, r, http.StatusConflict, "That site follows this channel already.")
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
		youtubeError(w, r, http.StatusServiceUnavailable, "YouTube cannot be reached from here.")
		return youtube.Followed{}, false
	}
	f, err := a.youtube.Channel(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		youtubeError(w, r, http.StatusNotFound, "No such channel is followed.")
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
		youtubeError(w, r, http.StatusBadRequest, "Say which switch.")
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
		youtubeError(w, r, http.StatusBadRequest, "That is not a video.")
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
