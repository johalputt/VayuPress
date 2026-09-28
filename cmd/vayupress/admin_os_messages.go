// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_messages.go — VayuOS "Messages" surface: the contact-form inbox.
//
// Every public contact submission is persisted to contact_messages (see
// migration 046) so operators always have a durable record, even if SMTP
// delivery is unconfigured or fails. This page lists them newest-first as the
// List kind, with the selected message read in the inspector beside it.
//
// CSP posture matches the rest of VayuOS: no inline styles, the only inline
// <script> carries the per-request nonce, every dynamic string is escaped.

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/ui"
)

// contactMessage is one message left through a contact form.
type contactMessage struct {
	ID, Name, Email, Message, Page  string
	Country, Region, City, DomainID string
	Read                            bool
	Created                         time.Time
}

const contactMessageCols = `id,name,email,message,page,country,region,city,is_read,created_at,domain_id`

func scanContactMessage(sc interface{ Scan(...any) error }) (contactMessage, error) {
	var m contactMessage
	var read int
	err := sc.Scan(&m.ID, &m.Name, &m.Email, &m.Message, &m.Page, &m.Country, &m.Region, &m.City, &read, &m.Created, &m.DomainID)
	m.Read = read != 0
	return m, err
}

// markMessageRead records that a message was opened. Opening one, by
// selecting it or by its own address, is what reading it means; a message the
// page merely shows first is not marked.
func markMessageRead(ctx context.Context, id string) {
	_, _ = dbpkg.WDB.ExecContext(ctx, `UPDATE contact_messages SET is_read=1 WHERE id=?`, id)
}

func unreadMessages(ctx context.Context) int {
	n := 0
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(ctx, `SELECT COUNT(1) FROM contact_messages WHERE is_read=0`).Scan(&n)
	}
	return n
}

// osMessageState is a message's state as a dot and a word, keyed by a
// per-message id, "m" for its row and "i" for the inspector, so opening it
// can update both.
func osMessageState(idEsc string, read bool, where string, oob bool) string {
	st := ui.State("accent", "New")
	if read {
		st = ui.State("neutral", "Read")
	}
	oobAttr := ""
	if oob {
		oobAttr = ` hx-swap-oob="true"`
	}
	return `<span id="` + where + `state-` + idEsc + `"` + oobAttr + `>` + string(st) + `</span>`
}

// osMessageInspector renders one message in the inspector: who wrote it,
// what they wrote, where from, and what can be done with it.
func osMessageInspector(m contactMessage, hosts map[string]string) string {
	idEsc := html.EscapeString(m.ID)
	var b strings.Builder
	b.WriteString(`<div class="sa-insp__title">` + html.EscapeString(m.Name) + `</div><div class="sa-insp__meta"><a href="mailto:` + html.EscapeString(m.Email) + `">` + html.EscapeString(m.Email) + `</a></div>`)
	b.WriteString(`<p class="comment-body">` + html.EscapeString(m.Message) + `</p>`)
	reply := "mailto:" + html.EscapeString(m.Email) + "?subject=" + url.QueryEscape("Re: your message") +
		"&body=" + url.QueryEscape("\n\n— On "+config.FormatSite(m.Created, "2 Jan 2006")+", "+m.Name+" wrote:\n> "+m.Message)
	b.WriteString(`<div class="sa-insp__actions"><a class="btn btn--sm" href="` + reply + `">` + saIcon("mail") + ` Reply</a>`)
	if !m.Read {
		b.WriteString(`<button type="button" class="btn btn--ghost btn--sm" data-msg-read data-id="` + idEsc + `">Mark read</button>`)
	}
	b.WriteString(`<button type="button" class="btn btn--sm btn--danger" data-msg-delete data-id="` + idEsc + `">Delete</button></div>`)
	b.WriteString(`<dl class="sa-insp__facts"><dt>State</dt><dd>` + osMessageState(idEsc, m.Read, "i", false) + `</dd>`)
	host := hosts[m.DomainID]
	if m.Page != "" {
		href := m.Page
		// A message from a hosted site links to the page on that site, not on the
		// console's own host.
		if host != "" && strings.HasPrefix(href, "/") {
			href = seo.Origin(host) + href
		}
		b.WriteString(`<dt>Page</dt><dd><a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener">` + html.EscapeString(m.Page) + `</a></dd>`)
	}
	switch {
	case host != "":
		b.WriteString(`<dt>Site</dt><dd>` + html.EscapeString(host) + `</dd>`)
	case m.DomainID != "":
		b.WriteString(`<dt>Site</dt><dd>A removed site</dd>`)
	}
	// GDPR-safe location instead of a raw IP: coarse country, city and region,
	// captured at submit time (no IP is ever stored or shown).
	loc := geoDisplayHTML(m.Country, m.City)
	if strings.TrimSpace(m.Region) != "" && strings.TrimSpace(m.City) == "" && strings.TrimSpace(m.Country) != "" {
		loc = countryDisplayHTML(m.Country) + ` <span class="muted">· ` + html.EscapeString(m.Region) + `</span>`
	}
	if loc != "" {
		b.WriteString(`<dt>From</dt><dd>` + loc + `</dd>`)
	}
	b.WriteString(`<dt>Received</dt><dd>` + config.FormatSite(m.Created, "2 Jan 2006, 15:04") + `</dd></dl>`)
	return b.String()
}

// handleOSMessages renders the inbox as the List kind: the messages as a
// table, new and all as views, a search and a date range, and the selected
// message in the inspector.
func (a *App) handleOSMessages(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	csrfTokenFor(w, r)

	// Filters: free-text search across name/email/message, the unread view and
	// a date range. All are applied in SQL so the list scales past the render cap.
	qv := r.URL.Query()
	q := strings.TrimSpace(qv.Get("q"))
	if len(q) > 120 {
		q = q[:120]
	}
	unreadOnly := qv.Get("unread") == "1"
	from := normalizeDateParam(qv.Get("from"))
	to := normalizeDateParam(qv.Get("to"))
	open := qv.Get("open")

	var msgs []contactMessage
	total := 0
	unread := unreadMessages(r.Context()) // independent of the filter: it is the view's count
	if dbpkg.DB != nil {
		_ = dbpkg.Reader().QueryRowContext(r.Context(), `SELECT COUNT(1) FROM contact_messages`).Scan(&total)
		where := []string{}
		args := []any{}
		if q != "" {
			where = append(where, "(name LIKE ? OR email LIKE ? OR message LIKE ?)")
			like := "%" + q + "%"
			args = append(args, like, like, like)
		}
		if unreadOnly {
			where = append(where, "is_read=0")
		}
		if from != "" {
			where = append(where, "date(created_at) >= ?")
			args = append(args, from)
		}
		if to != "" {
			where = append(where, "date(created_at) <= ?")
			args = append(args, to)
		}
		clause := ""
		if len(where) > 0 {
			clause = " WHERE " + strings.Join(where, " AND ")
		}
		if rows, err := dbpkg.Reader().QueryContext(r.Context(),
			`SELECT `+contactMessageCols+` FROM contact_messages`+clause+` ORDER BY created_at DESC LIMIT 500`, args...); err == nil {
			defer rows.Close() //nolint:errcheck
			for rows.Next() {
				if m, err := scanContactMessage(rows); err == nil {
					msgs = append(msgs, m)
				}
			}
			_ = rows.Err()
		}
	}

	if total == 0 {
		writeOSHTML(w, r, adminOSLayout(nonce, "Messages", "messages", cfg, htmpl.HTML(ui.List(ui.ListPage{Title: "Messages"},
			ui.Empty("mail", "No messages yet", "When a visitor writes through a page's contact form, the message is kept here, even when email delivery fails. The form goes on a page made from the Contact template.",
				ui.HTML(`<a class="btn" href="/os/pages">Pages</a>`)), ""))))
		return
	}

	// The message named by ?open= (a link to one message) is the one shown and,
	// being opened, is marked read; otherwise the newest is shown and left as
	// it is.
	sel := 0
	for i, m := range msgs {
		if open != "" && m.ID == open {
			sel = i
			if !m.Read {
				markMessageRead(r.Context(), m.ID)
				msgs[i].Read = true
				unread--
			}
		}
	}
	hosts := a.domainHosts(r.Context())
	var rows strings.Builder
	for i, m := range msgs {
		idEsc := html.EscapeString(m.ID)
		selected := "false"
		if i == sel {
			selected = "true"
		}
		// One inbox holds every site's messages, so a hosted site's row names it.
		where := m.Page
		if host := hosts[m.DomainID]; host != "" {
			where = host + m.Page
		}
		rows.WriteString(`<tr class="post-row" data-list-row data-msg-row data-id="` + idEsc + `" data-list-src="/os/messages/inspector/` + idEsc + `" tabindex="0" aria-selected="` + selected + `">` +
			`<td class="post-row__name">` + html.EscapeString(m.Name) + `</td>` +
			`<td class="comment-row__body">` + html.EscapeString(m.Message) + `</td>` +
			`<td class="post-row__date">` + html.EscapeString(where) + `</td>` +
			`<td>` + osMessageState(idEsc, m.Read, "m", false) + `</td>` +
			`<td class="post-row__date">` + config.FormatSite(m.Created, "2 Jan") + `</td></tr>`)
	}
	list := `<p class="table-empty">No message matches that. <a href="/os/messages">Show every message</a>.</p>`
	inspector := ""
	if len(msgs) > 0 {
		list = `<div class="table-wrap"><table class="table post-table comment-table"><thead><tr><th>From</th><th>Message</th><th>Page</th><th>State</th><th>Received</th></tr></thead><tbody>` +
			rows.String() + `</tbody></table></div>`
		inspector = osMessageInspector(msgs[sel], hosts)
	}

	href := func(unread bool, from, to string) string {
		v := url.Values{}
		if q != "" {
			v.Set("q", q)
		}
		if unread {
			v.Set("unread", "1")
		}
		if from != "" {
			v.Set("from", from)
		}
		if to != "" {
			v.Set("to", to)
		}
		if enc := v.Encode(); enc != "" {
			return "/os/messages?" + enc
		}
		return "/os/messages"
	}
	keep := [][2]string{}
	for _, kv := range [][2]string{{"from", from}, {"to", to}} {
		if kv[1] != "" {
			keep = append(keep, kv)
		}
	}
	if unreadOnly {
		keep = append(keep, [2]string{"unread", "1"})
	}
	unreadHidden := ""
	if unreadOnly {
		unreadHidden = `<input type="hidden" name="unread" value="1">`
	}
	count := strconv.Itoa(total)
	if unread > 0 {
		count += " · " + strconv.Itoa(unread) + " new"
	}
	body := string(ui.List(ui.ListPage{
		Title: "Messages",
		Count: count,
		Views: ui.Segments("Show",
			ui.Segment{Label: "All", Href: href(false, from, to), Count: total, On: !unreadOnly},
			ui.Segment{Label: "New", Href: href(true, from, to), Count: unread, On: unreadOnly, CountID: "msg-unread"}),
		Search: ui.Search(ui.SearchBox{Action: "/os/messages", Name: "q", Value: q, Placeholder: "Search messages", Keep: keep}),
		Actions: ui.HTML(`<button type="button" class="btn" data-sheet="msg-when">` + saIcon("calendar") + ` ` + html.EscapeString(osWhenLabel("", from, to)) + `</button>` +
			`<a class="btn btn--ghost" href="/os/api/messages/export.csv" download>Export</a>` +
			`<button type="button" class="btn btn--ghost" data-msg-readall>Mark all read</button>` +
			`<button type="button" class="btn btn--ghost" data-msg-deleteread>Clear read</button>`),
	}, ui.HTML(list), ui.HTML(inspector))) +
		string(ui.Sheet("msg-when", "Show messages from", ui.HTML(`<form method="GET" action="/os/messages">
  <input type="hidden" name="q" value="`+html.EscapeString(q)+`">`+unreadHidden+`
  <div class="field"><label class="field-label" for="msg-from">From</label><input id="msg-from" class="input" type="date" name="from" value="`+html.EscapeString(from)+`"></div>
  <div class="field"><label class="field-label" for="msg-to">To</label><input id="msg-to" class="input" type="date" name="to" value="`+html.EscapeString(to)+`"></div>
  <div class="mt-3 sa-list__sheet-actions"><button class="btn btn--primary btn--sm" type="submit">Show</button><a class="btn btn--ghost btn--sm" href="`+html.EscapeString(href(unreadOnly, "", ""))+`">Any time</a></div>
</form>`))) + `
<div id="msg-status" class="text-sm muted" role="status" aria-live="polite"></div>
<script nonce="` + nonce + `">` + osMessagesScript + `</script>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Messages", "messages", cfg, htmpl.HTML(body)))
}

// osMessagesScript runs the inbox's actions. Mark read and delete are
// delegated from the document because the inspector that holds them is
// replaced each time a row is selected.
const osMessagesScript = `
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var st=document.getElementById('msg-status');
function show(t){if(st)st.textContent=t;}
function row(id){var r=null;document.querySelectorAll('[data-msg-row]').forEach(function(x){if(x.getAttribute('data-id')===id)r=x;});return r;}
document.addEventListener('click',function(ev){
  var b=ev.target.closest('[data-msg-read]');
  if(b){
    var id=b.getAttribute('data-id');b.disabled=true;show('Saving…');
    fetch('/os/api/messages/'+encodeURIComponent(id)+'/read',{method:'PUT',headers:{'X-CSRF-Token':csrf()}})
      .then(function(r){if(!r.ok){b.disabled=false;show('Could not update');return;}
        ['mstate-','istate-'].forEach(function(p){var el=document.getElementById(p+id);if(el)el.innerHTML='<span class="sa-indicator"><span class="sa-dot sa-dot--neutral" aria-hidden="true"></span>Read</span>';});
        var n=document.getElementById('msg-unread');if(n)n.textContent=String(Math.max(0,parseInt(n.textContent,10)-1));
        b.remove();show('Marked read');})
      .catch(function(e){b.disabled=false;show('Error: '+e);});
    return;
  }
  var d=ev.target.closest('[data-msg-delete]');
  if(!d)return;
  vpConfirm({title:'Delete message',message:'Delete this message? This cannot be undone.',confirm:'Delete'},function(){
    var id=d.getAttribute('data-id');d.disabled=true;show('Deleting…');
    fetch('/os/api/messages/'+encodeURIComponent(id),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}})
      .then(function(r){if(!r.ok){d.disabled=false;show('Could not delete');return;}
        var x=row(id);if(x)x.remove();
        var insp=document.querySelector('[data-list-inspector]');if(insp)insp.innerHTML='<p class="table-empty">Select a message to read it here.</p>';
        show('Deleted');})
      .catch(function(e){d.disabled=false;show('Error: '+e);});
  });
});
var readAll=document.querySelector('[data-msg-readall]');
if(readAll)readAll.addEventListener('click',function(){
  readAll.disabled=true;show('Marking all read…');
  fetch('/os/api/messages/read-all',{method:'POST',headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){if(r.ok){location.reload();}else{readAll.disabled=false;show('Could not update');}})
    .catch(function(e){readAll.disabled=false;show('Error: '+e);});
});
var delRead=document.querySelector('[data-msg-deleteread]');
if(delRead)delRead.addEventListener('click',function(){
  vpConfirm({title:'Clear read messages',message:'Delete all messages already marked read? This cannot be undone.',confirm:'Delete read'},function(){
  delRead.disabled=true;show('Clearing read…');
  fetch('/os/api/messages/delete-read',{method:'POST',headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){if(r.ok){location.reload();}else{delRead.disabled=false;show('Could not clear');}})
    .catch(function(e){delRead.disabled=false;show('Error: '+e);});
  });
});
})();
`

// handleOSMessageInspector returns one message's inspector, for the row a
// person selects. Selecting a message is opening it, so an unread one is
// marked read, and its row and the New count follow out of band.
func (a *App) handleOSMessageInspector(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if dbpkg.DB == nil {
		http.Error(w, "no database", http.StatusServiceUnavailable)
		return
	}
	m, err := scanContactMessage(dbpkg.Reader().QueryRowContext(r.Context(), `SELECT `+contactMessageCols+` FROM contact_messages WHERE id=?`, id))
	if err == sql.ErrNoRows {
		http.Error(w, "no such message", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "the message could not be read", http.StatusInternalServerError)
		return
	}
	extra := ""
	if !m.Read {
		markMessageRead(r.Context(), m.ID)
		m.Read = true
		extra = osMessageState(html.EscapeString(m.ID), true, "m", true) +
			`<span class="muted" id="msg-unread" hx-swap-oob="true">` + strconv.Itoa(unreadMessages(r.Context())) + `</span>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, osMessageInspector(m, a.domainHosts(r.Context()))+extra)
}

// handleOSMessageDetail keeps a link to one message working: it opens the
// inbox with that message in the inspector, which is where a message is read.
func (a *App) handleOSMessageDetail(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/os/messages?open="+url.QueryEscape(chi.URLParam(r, "id")), http.StatusSeeOther)
}

// handleOSMessageRead marks a contact message read.
func (a *App) handleOSMessageRead(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := dbpkg.WDB.ExecContext(r.Context(), `UPDATE contact_messages SET is_read=1 WHERE id=?`, id); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSMessageDelete removes a contact message.
func (a *App) handleOSMessageDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := dbpkg.WDB.ExecContext(r.Context(), `DELETE FROM contact_messages WHERE id=?`, id); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSMessagesReadAll marks every message read in one go.
func (a *App) handleOSMessagesReadAll(w http.ResponseWriter, r *http.Request) {
	if _, err := dbpkg.WDB.ExecContext(r.Context(), `UPDATE contact_messages SET is_read=1 WHERE is_read=0`); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSMessagesDeleteRead clears out every already-read message (an
// "empty trash" for processed submissions). Unread messages are kept.
func (a *App) handleOSMessagesDeleteRead(w http.ResponseWriter, r *http.Request) {
	if _, err := dbpkg.WDB.ExecContext(r.Context(), `DELETE FROM contact_messages WHERE is_read=1`); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSMessagesExportCSV streams every contact message as a downloadable CSV
// (RFC 4180 via encoding/csv, which quotes/escapes commas, quotes and newlines).
func (a *App) handleOSMessagesExportCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="contact-messages.csv"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"created_at", "name", "email", "page", "country", "region", "city", "read", "message"})
	if dbpkg.DB == nil {
		return
	}
	rows, err := dbpkg.Reader().QueryContext(r.Context(),
		`SELECT created_at,name,email,page,country,region,city,is_read,message FROM contact_messages ORDER BY created_at DESC`)
	if err != nil {
		return
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var created time.Time
		var name, eml, page, country, region, city, msg string
		var read int
		if rows.Scan(&created, &name, &eml, &page, &country, &region, &city, &read, &msg) != nil {
			continue
		}
		readStr := "no"
		if read != 0 {
			readStr = "yes"
		}
		_ = cw.Write([]string{created.UTC().Format(time.RFC3339), name, eml, page, country, region, city, readStr, msg})
	}
	_ = rows.Err()
}

// domainHosts maps each hosted domain's id to its host, for labelling
// messages by the site they were sent from.
func (a *App) domainHosts(ctx context.Context) map[string]string {
	hosts := map[string]string{}
	if a.domains == nil {
		return hosts
	}
	if list, err := a.domains.List(ctx); err == nil {
		for _, d := range list {
			if !d.IsPrimary {
				hosts[d.ID] = d.Host
			}
		}
	}
	return hosts
}
