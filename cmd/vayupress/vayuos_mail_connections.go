// SPDX-License-Identifier: Apache-2.0

package main

// vayuos_mail_connections.go — Settings › Connected tools: the AI tools a
// holder connected to their own mailbox over VayuMCP (mcp_mail.go), what
// each may do and last did, the record of their calls, and Disconnect,
// which ends the connection and revokes its key.

import (
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

var scopeWords = map[string]string{vmail.ScopeRead: "read mail and calendar", vmail.ScopeDraft: "write drafts and file mail", vmail.ScopeCalendar: "keep the calendar"}

// connectedToolsBlock is the holder's connections and their record.
func (a *App) connectedToolsBlock(r *http.Request, rd vmail.Reader, said string) string {
	home := a.calendarHome(r)
	var b strings.Builder
	b.WriteString(`<div id="vm-connections">` + said)
	cs, _ := a.vayuMail.Connections(rd)
	if len(cs) == 0 {
		b.WriteString(`<p class="text-sm">No tool is connected. An AI tool connects to ` + esc(a.oauthBaseURL(r)) + `/mcp; when it asks, you sign in here and choose what it may do. It never sends mail: what it writes waits in Drafts for you.</p></div>`)
		return b.String()
	}
	b.WriteString(`<ul class="drive-versions">`)
	for _, c := range cs {
		may := make([]string, 0, len(c.Scopes))
		for _, s := range c.Scopes {
			may = append(may, scopeWords[s])
		}
		used := "not used yet"
		if !c.LastUsed.IsZero() {
			used = "last used " + c.LastUsed.In(home).Format("2 January, 15:04")
		}
		b.WriteString(`<li><span>` + esc(c.Client) + ` · may ` + esc(strings.Join(may, ", ")) + ` · connected ` + esc(c.Created.In(home).Format("2 January 2006")) + ` · ` + esc(used) + `</span>` +
			`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/connections/disconnect" hx-target="#vm-connections" hx-swap="outerHTML"` + hxVals("key", c.KeyID) +
			` hx-confirm="Disconnect this tool? It loses its way in at once.">Disconnect</button></li>`)
	}
	b.WriteString(`</ul>`)
	if calls, _ := a.vayuMail.ToolCalls(rd, 20); len(calls) > 0 {
		b.WriteString(`<h3 class="mx-set__name">What they did</h3><ul class="drive-versions">`)
		for _, t := range calls {
			b.WriteString(`<li><span>` + esc(t.At.In(home).Format("2 Jan, 15:04")) + ` · ` + esc(t.Client) + ` · ` + esc(t.What) + `</span></li>`)
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
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "Connections belong to a mailbox's holder.", "")
		return
	}
	_ = r.ParseForm()
	key := r.PostFormValue("key")
	said := saCallout("ok", "Disconnected. It has no way in now.")
	if err := a.vayuMail.Disconnect(rd, key); err != nil {
		said = saCallout("danger", "That tool is not connected to your mailbox.")
	} else if a.apiKeys != nil {
		_ = a.apiKeys.Revoke(r.Context(), key)
	}
	writeOSHTML(w, r, a.connectedToolsBlock(r, rd, said))
}
