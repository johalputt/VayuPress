// SPDX-License-Identifier: Apache-2.0

package main

// sessions_mine.go — a principal's own console sessions: where each was
// opened from, signing one out, and signing out everywhere else.

import (
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// startSession opens a console session for userID and records the device
// the request came from, so its owner's list can say which is which.
func (a *App) startSession(r *http.Request, userID string) (string, error) {
	token, err := a.sessions.Create(r.Context(), userID)
	if err == nil {
		_ = a.sessions.Describe(r.Context(), token, deviceLabel(r.UserAgent()))
	}
	return token, err
}

// deviceLabel names a browser and its system from a User-Agent, in the
// words a person would use ("Firefox on Linux"); "" when it says neither.
// The order matters: Edge and Opera say Chrome too, Chrome says Safari, and
// an iPhone says Mac OS X.
func deviceLabel(ua string) string {
	browser := ""
	for _, b := range [][2]string{{"Edg/", "Edge"}, {"OPR/", "Opera"}, {"Firefox/", "Firefox"}, {"Chrome/", "Chrome"}, {"Safari/", "Safari"}} {
		if strings.Contains(ua, b[0]) {
			browser = b[1]
			break
		}
	}
	system := ""
	for _, s := range [][2]string{{"iPhone", "iPhone"}, {"iPad", "iPad"}, {"Android", "Android"}, {"CrOS", "ChromeOS"}, {"Windows", "Windows"}, {"Mac OS X", "macOS"}, {"Linux", "Linux"}} {
		if strings.Contains(ua, s[0]) {
			system = s[1]
			break
		}
	}
	switch {
	case browser != "" && system != "":
		return browser + " on " + system
	case browser != "":
		return browser
	}
	return system
}

// sessionsBlock is the signed-in principal's sessions, newest first, this
// one marked and each other one with its own Sign out.
func (a *App) sessionsBlock(r *http.Request) string {
	u := currentUser(r)
	c, err := r.Cookie(auth.SessionCookie)
	if u == nil || err != nil || a.sessions == nil {
		return ""
	}
	list, err := a.sessions.List(r.Context(), u.ID)
	if err != nil {
		return ""
	}
	this := auth.SessionID(c.Value)
	var b strings.Builder
	b.WriteString(`<div id="vm-sessions"><ul class="mx-sessions">`)
	for _, s := range list {
		device := s.Device
		if device == "" {
			device = "A browser"
		}
		b.WriteString(`<li><span class="mx-sessions__what"><span class="mx-sessions__device">` + esc(device) + `</span><span class="mx-sessions__when">Signed in ` +
			esc(config.FormatSite(s.Created, "2 January 2006, 15:04")) + `</span></span>`)
		if s.ID == this {
			b.WriteString(`<span class="mx-sessions__this">This one</span>`)
		} else {
			b.WriteString(`<button type="button" class="btn btn--sm" hx-post="/os/api/sessions/end"` + hxVals("id", s.ID) + ` hx-target="#vm-sessions" hx-swap="outerHTML">Sign out</button>`)
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
	if len(list) > 1 {
		b.WriteString(`<button type="button" class="btn btn--sm" hx-post="/os/api/sessions/end-others" hx-target="#vm-sessions" hx-swap="outerHTML" hx-confirm="Sign out of every other session?">Sign out everywhere else</button>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// handleSessionEnd signs out one of the principal's own other sessions.
func (a *App) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	a.endSessions(w, r, false)
}

// handleSessionEndOthers signs out every session of the principal but this.
func (a *App) handleSessionEndOthers(w http.ResponseWriter, r *http.Request) {
	a.endSessions(w, r, true)
}

func (a *App) endSessions(w http.ResponseWriter, r *http.Request, others bool) {
	u := currentUser(r)
	c, err := r.Cookie(auth.SessionCookie)
	if u == nil || err != nil {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Only a signed-in session has sessions to end.", "")
		return
	}
	if others {
		_, err = a.sessions.DestroyOthers(r.Context(), u.ID, c.Value)
	} else if id := strings.TrimSpace(r.PostFormValue("id")); id != auth.SessionID(c.Value) {
		// This session is ended by signing out, not from its own list.
		_, err = a.sessions.DestroyByID(r.Context(), u.ID, id)
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "sessions", "The sessions could not be ended.", "")
		return
	}
	writeOSFragment(w, a.sessionsBlock(r))
}

// securityChecklist is the holder's security at a glance: what is done,
// what is not and where to do it, and how many of the four are done.
func (a *App) securityChecklist(r *http.Request, ac vmail.Account) string {
	ctx := r.Context()
	passkeys := 0
	if principal, _, ok := passkeyPrincipal(r); ok && a.passkeys != nil {
		list, _ := a.passkeys.List(ctx, principal)
		passkeys = len(list)
	}
	recovery := a.vayuMail.Accounts().RecoveryStatusFor(ctx, ac.Email).Ready
	stale := 0
	for _, p := range a.vayuMail.Accounts().ListAppPasswords(ctx, ac.Email) {
		if appPasswordForgotten(p, time.Now()) {
			stale++
		}
	}
	items := []struct {
		done      bool
		said, fix string
	}{
		{ac.TOTPEnabled, "Two-factor is on", `Turn on two-factor on <a href="/members/account">your account on the site</a>`},
		{passkeys > 0, "You have a passkey", `Add a passkey under Passkeys below`},
		{recovery, "You have a way back in", `Add a recovery address or codes on <a href="/os/vayumail/connect">Connect a device</a>`},
		{stale == 0, "No forgotten app passwords", `Remove the app passwords nothing uses on <a href="/os/vayumail/connect">Connect a device</a>`},
	}
	done := 0
	var rows strings.Builder
	for _, it := range items {
		if it.done {
			done++
			rows.WriteString(`<li class="is-done">` + saIcon("check-c") + `<span>` + it.said + `</span></li>`)
		} else {
			rows.WriteString(`<li>` + saIcon("warn") + `<span>` + it.fix + `</span></li>`)
		}
	}
	return `<p class="mx-score"><strong>` + itoaSafe(done) + ` of ` + itoaSafe(len(items)) + `</strong> done</p><ul class="mx-checklist">` + rows.String() + `</ul>`
}

// appPasswordForgotten is an app password nothing seems to use: never used
// a month after it was made, or not used for three months.
func appPasswordForgotten(p vmail.AppPassword, now time.Time) bool {
	if p.Status != vmail.DeviceStatusApproved {
		return false // a blocked or pending device signs nothing in
	}
	if p.LastUsedAt.IsZero() {
		return now.Sub(p.CreatedAt) > 30*24*time.Hour
	}
	return now.Sub(p.LastUsedAt) > 90*24*time.Hour
}
