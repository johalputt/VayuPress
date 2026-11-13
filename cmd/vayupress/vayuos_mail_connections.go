// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_connections.go — Settings › Connected tools: the AI tools a
// holder connected to their own mailbox over VayuMCP (mcp_mail.go), what
// each may do and last did, the record of their calls, and Disconnect,
// which ends the connection and revokes its key.

import (
	"github.com/johalputt/vayupress/internal/i18n"
	"net/http"
	"strings"

	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
)

// connectionReader is the session's own mailbox, for its connections:
// never another's, an administrator's session included.
func (a *App) connectionReader(r *http.Request) (vmail.Reader, bool) {
	if !a.mailRunning() || a.vayuMail.Accounts() == nil {
		return vmail.Reader{}, false
	}
	_, email := a.ownMailbox(r)
	if email == "" {
		return vmail.Reader{}, false
	}
	return vmail.ReadAsOwner(mailKeyOf(email, a.vayuMail.Config().Domain)), true
}

// scopeWords is what a scope lets a connected tool do, in lang.
func scopeWords(lang, scope string) string {
	switch scope {
	case vmail.ScopeRead:
		return i18n.T(lang, "read mail and calendar")
	case vmail.ScopeDraft:
		return i18n.T(lang, "write drafts and file mail")
	case vmail.ScopeCalendar:
		return i18n.T(lang, "keep the calendar")
	}
	return scope
}

// connectedToolsBlock is the holder's connections and their record.
func (a *App) connectedToolsBlock(r *http.Request, rd vmail.Reader, said string) string {
	home, lang := a.calendarHome(r), i18n.Lang(r.Context())
	var b strings.Builder
	b.WriteString(`<div id="vm-connections">` + said)
	cs, _ := a.vayuMail.Connections(rd)
	if len(cs) == 0 {
		b.WriteString(`<p class="text-sm">` + esc(i18n.T(lang, "No tool is connected. An AI tool connects to {address}; when it asks, you sign in here and choose what it may do. It never sends mail: what it writes waits in Drafts for you.", "address", a.oauthBaseURL(r)+"/mcp")) + `</p></div>`)
		return b.String()
	}
	b.WriteString(`<ul class="drive-versions">`)
	for _, c := range cs {
		may := make([]string, 0, len(c.Scopes))
		for _, s := range c.Scopes {
			may = append(may, scopeWords(lang, s))
		}
		used := i18n.T(lang, "not used yet")
		if !c.LastUsed.IsZero() {
			used = i18n.T(lang, "last used {when}", "when", i18n.Date(lang, c.LastUsed.In(home), i18n.DayMonthAt))
		}
		b.WriteString(`<li><span>` + esc(c.Client) + ` · ` + esc(i18n.T(lang, "may {what}", "what", strings.Join(may, ", "))) + ` · ` +
			esc(i18n.T(lang, "connected {date}", "date", i18n.Date(lang, c.Created.In(home), i18n.DayMonthYearShort))) + ` · ` + esc(used) + `</span>` +
			`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/connections/disconnect" hx-target="#vm-connections" hx-swap="outerHTML"` + hxVals("key", c.KeyID) +
			` hx-confirm="` + esc(i18n.T(lang, "Disconnect this tool? It loses its way in at once.")) + `">` + esc(i18n.T(lang, "Disconnect")) + `</button></li>`)
	}
	b.WriteString(`</ul>`)
	if calls, _ := a.vayuMail.ToolCalls(rd, 20); len(calls) > 0 {
		b.WriteString(`<h3 class="mx-set__name">` + esc(i18n.T(lang, "What they did")) + `</h3><ul class="drive-versions">`)
		for _, t := range calls {
			b.WriteString(`<li><span>` + esc(i18n.Date(lang, t.At.In(home), i18n.WeekdayDayMonthAt)) + ` · ` + esc(t.Client) + ` · ` + esc(t.What) + `</span></li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// connectedToolsHere is the session's own Connected tools.
func (a *App) connectedToolsHere(r *http.Request) string {
	rd, ok := a.connectionReader(r)
	if !ok {
		return ""
	}
	return a.connectedToolsBlock(r, rd, "")
}

// handleVayuOSMailDisconnect ends one of the session's own connections and
// revokes its key.
func (a *App) handleVayuOSMailDisconnect(w http.ResponseWriter, r *http.Request) {
	rd, ok := a.connectionReader(r)
	if !ok {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", i18n.T(i18n.Lang(r.Context()), "Connections belong to a mailbox's holder."), "")
		return
	}
	_ = r.ParseForm()
	key := r.PostFormValue("key")
	lang := i18n.Lang(r.Context())
	said := saCallout("ok", esc(i18n.T(lang, "Disconnected. It has no way in now.")))
	if err := a.vayuMail.Disconnect(rd, key); err != nil {
		said = saCallout("danger", esc(i18n.T(lang, "That tool is not connected to your mailbox.")))
	} else if a.apiKeys != nil {
		_ = a.apiKeys.Revoke(r.Context(), key)
	}
	writeOSHTML(w, r, a.connectedToolsBlock(r, rd, said))
}
