package main

// admin_os_messages.go — VayuOS "Messages" surface: the contact-form inbox.
//
// Every public contact submission is persisted to contact_messages (see
// migration 046) so operators always have a durable record, even if SMTP
// delivery is unconfigured or fails. This page lists them newest-first with
// mark-read and delete controls. Unread messages are highlighted and counted.
//
// CSP posture matches the rest of VayuOS: no inline styles, the only inline
// <script> carries the per-request nonce, every dynamic string is escaped.

import (
	"encoding/csv"
	"html"
	htmpl "html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/render"
)

func (a *App) handleOSMessages(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	// Filters: free-text search across name/email/message, and an unread-only
	// toggle. Both are applied in SQL so the list scales past the render cap.
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 120 {
		q = q[:120]
	}
	unreadOnly := r.URL.Query().Get("unread") == "1"
	filtersActive := q != "" || unreadOnly

	type msgRow struct {
		ID, Name, Email, Message, Page string
		Read                           bool
		Created                        time.Time
	}
	var msgs []msgRow
	unread := 0 // total unread, independent of the active filter (header + badge)
	if dbpkg.DB != nil {
		_ = dbpkg.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM contact_messages WHERE is_read=0`).Scan(&unread)

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
		clause := ""
		if len(where) > 0 {
			clause = " WHERE " + strings.Join(where, " AND ")
		}
		if rows, err := dbpkg.DB.QueryContext(r.Context(),
			`SELECT id,name,email,message,page,is_read,created_at FROM contact_messages`+clause+` ORDER BY created_at DESC LIMIT 500`, args...); err == nil {
			defer rows.Close() //nolint:errcheck
			for rows.Next() {
				var m msgRow
				var read int
				if rows.Scan(&m.ID, &m.Name, &m.Email, &m.Message, &m.Page, &read, &m.Created) == nil {
					m.Read = read != 0
					msgs = append(msgs, m)
				}
			}
			_ = rows.Err()
		}
	}

	// Filter toolbar: a GET search form + an unread-only toggle link that
	// preserves the current query. Plain links/forms → CSP-safe, JS-free.
	unreadHref := "/os/messages"
	if !unreadOnly {
		uv := url.Values{}
		uv.Set("unread", "1")
		if q != "" {
			uv.Set("q", q)
		}
		unreadHref = "/os/messages?" + uv.Encode()
	} else if q != "" {
		unreadHref = "/os/messages?q=" + url.QueryEscape(q)
	}
	unreadCls := "btn btn--ghost btn--sm"
	if unreadOnly {
		unreadCls = "btn btn--primary btn--sm"
	}
	filterBar := `<div class="card"><div class="toolbar-row">
  <form method="GET" action="/os/messages" class="vm-row" style="flex:1;gap:.5rem">
    <input type="search" name="q" class="input" style="flex:1" value="` + html.EscapeString(q) + `" placeholder="Search name, email or message…" aria-label="Search messages">
    <button type="submit" class="btn btn--sm">Search</button>
  </form>
  <a class="` + unreadCls + `" href="` + unreadHref + `">Unread only</a>
  ` + filterClearLink(filtersActive) + `
</div></div>`

	var body string
	if len(msgs) == 0 && !filtersActive {
		body = `<div class="page-header"><h1>Messages</h1>
  <p class="text-sm muted">Submissions from your contact form land here — a durable record, even if email delivery fails.</p></div>
<div class="card empty-state"><div class="empty-icon">📨</div>
  <div class="empty-title">No messages yet</div>
  <div class="empty-sub">When a visitor sends a message through a page's contact form, it appears here. Add a contact form from the Pages section.</div></div>`
	} else if len(msgs) == 0 {
		body = messagesHeader(0, unread) + filterBar +
			`<div class="card empty-state"><div class="empty-icon">🔍</div>
  <div class="empty-title">No matching messages</div>
  <div class="empty-sub">No messages match your search or filter. <a href="/os/messages">Clear filters</a>.</div></div>`
	} else {
		rows := ""
		for _, m := range msgs {
			rowCls := "row-title"
			pill := ""
			if !m.Read {
				pill = `<span class="status-pill status-pill--draft">● New</span> `
			}
			pageCell := ""
			if m.Page != "" {
				pageCell = `<a href="` + html.EscapeString(m.Page) + `" target="_blank" rel="noopener">` + html.EscapeString(m.Page) + `</a>`
			}
			readBtn := ""
			if !m.Read {
				readBtn = `<button type="button" class="btn btn--ghost btn--sm" data-msg-read data-id="` + html.EscapeString(m.ID) + `">Mark read</button>`
			}
			rows += `<tr data-msg-row>
  <td class="` + rowCls + `">` + pill + `<strong>` + html.EscapeString(m.Name) + `</strong>
    <div class="row-meta"><a href="mailto:` + html.EscapeString(m.Email) + `">` + html.EscapeString(m.Email) + `</a></div></td>
  <td style="white-space:pre-wrap;max-width:40ch">` + html.EscapeString(m.Message) + `</td>
  <td>` + pageCell + `</td>
  <td class="muted text-sm">` + m.Created.UTC().Format("2 Jan 2006 15:04") + `</td>
  <td class="row-actions">
    <a class="btn btn--ghost btn--sm" href="mailto:` + html.EscapeString(m.Email) + `?subject=Re:%20your%20message">Reply</a>
    ` + readBtn + `
    <button type="button" class="btn btn--ghost btn--sm" data-msg-delete data-id="` + html.EscapeString(m.ID) + `">Delete</button>
  </td>
</tr>`
		}
		body = messagesHeader(len(msgs), unread) + filterBar + `
<div class="card"><div class="table-wrap"><table class="table">
  <thead><tr><th>From</th><th>Message</th><th>Page</th><th>When</th><th></th></tr></thead>
  <tbody>` + rows + `</tbody>
</table></div></div>
<div id="msg-status" class="text-sm muted" role="status" aria-live="polite"></div>
<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var st=document.getElementById('msg-status');
function show(t){if(st)st.textContent=t;}
document.querySelectorAll('[data-msg-read]').forEach(function(b){
  b.addEventListener('click',function(){
    b.disabled=true;show('Saving…');
    fetch('/os/api/messages/'+encodeURIComponent(b.getAttribute('data-id'))+'/read',{method:'PUT',headers:{'X-CSRF-Token':csrf()}})
      .then(function(r){if(r.ok){location.reload();}else{b.disabled=false;show('Could not update');}})
      .catch(function(e){b.disabled=false;show('Error: '+e);});
  });
});
document.querySelectorAll('[data-msg-delete]').forEach(function(b){
  b.addEventListener('click',function(){
    if(!window.confirm('Delete this message? This cannot be undone.'))return;
    b.disabled=true;show('Deleting…');
    fetch('/os/api/messages/'+encodeURIComponent(b.getAttribute('data-id')),{method:'DELETE',headers:{'X-CSRF-Token':csrf()}})
      .then(function(r){if(r.ok){var row=b.closest('[data-msg-row]');if(row)row.remove();show('Deleted');}else{b.disabled=false;show('Could not delete');}})
      .catch(function(e){b.disabled=false;show('Error: '+e);});
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
  if(!window.confirm('Delete all messages already marked read? This cannot be undone.'))return;
  delRead.disabled=true;show('Clearing read…');
  fetch('/os/api/messages/delete-read',{method:'POST',headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){if(r.ok){location.reload();}else{delRead.disabled=false;show('Could not clear');}})
    .catch(function(e){delRead.disabled=false;show('Error: '+e);});
});
})();
</script>`
	}

	writeOSHTML(w, adminOSLayout(nonce, "Messages", "messages", cfg, htmpl.HTML(body)))
}

// messagesHeader renders the Messages page header with the count, unread tally
// and the bulk/export actions. Shared by the list and filtered-empty views.
func messagesHeader(count, unread int) string {
	return `<div class="page-header">
  <h1>Messages <span class="count-pill">` + intToStr(count) + `</span></h1>
  <div class="page-actions">
    <span class="text-sm muted">` + intToStr(unread) + ` unread</span>
    <a class="btn btn--ghost btn--sm" href="/os/api/messages/export.csv" download>Export CSV</a>
    <button type="button" class="btn btn--ghost btn--sm" data-msg-readall>Mark all read</button>
    <button type="button" class="btn btn--ghost btn--sm" data-msg-deleteread>Clear read</button>
  </div>
</div>`
}

// filterClearLink renders a "Clear" link when any filter is active.
func filterClearLink(active bool) string {
	if !active {
		return ""
	}
	return `<a class="btn btn--ghost btn--sm" href="/os/messages">Clear</a>`
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
	_ = cw.Write([]string{"created_at", "name", "email", "page", "ip", "read", "message"})
	if dbpkg.DB == nil {
		return
	}
	rows, err := dbpkg.DB.QueryContext(r.Context(),
		`SELECT created_at,name,email,page,ip,is_read,message FROM contact_messages ORDER BY created_at DESC`)
	if err != nil {
		return
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var created time.Time
		var name, eml, page, ip, msg string
		var read int
		if rows.Scan(&created, &name, &eml, &page, &ip, &read, &msg) != nil {
			continue
		}
		readStr := "no"
		if read != 0 {
			readStr = "yes"
		}
		_ = cw.Write([]string{created.UTC().Format(time.RFC3339), name, eml, page, ip, readStr, msg})
	}
	_ = rows.Err()
}
