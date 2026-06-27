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
	"html"
	htmpl "html/template"
	"net/http"
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

	type msgRow struct {
		ID, Name, Email, Message, Page string
		Read                           bool
		Created                        time.Time
	}
	var msgs []msgRow
	unread := 0
	if dbpkg.DB != nil {
		if rows, err := dbpkg.DB.QueryContext(r.Context(),
			`SELECT id,name,email,message,page,is_read,created_at FROM contact_messages ORDER BY created_at DESC LIMIT 500`); err == nil {
			defer rows.Close() //nolint:errcheck
			for rows.Next() {
				var m msgRow
				var read int
				if rows.Scan(&m.ID, &m.Name, &m.Email, &m.Message, &m.Page, &read, &m.Created) == nil {
					m.Read = read != 0
					if !m.Read {
						unread++
					}
					msgs = append(msgs, m)
				}
			}
			_ = rows.Err()
		}
	}

	var body string
	if len(msgs) == 0 {
		body = `<div class="page-header"><h1>Messages</h1>
  <p class="text-sm muted">Submissions from your contact form land here — a durable record, even if email delivery fails.</p></div>
<div class="card empty-state"><div class="empty-icon">📨</div>
  <div class="empty-title">No messages yet</div>
  <div class="empty-sub">When a visitor sends a message through a page's contact form, it appears here. Add a contact form from the Pages section.</div></div>`
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
		body = `<div class="page-header">
  <h1>Messages <span class="count-pill">` + intToStr(len(msgs)) + `</span></h1>
  <div class="page-actions"><span class="text-sm muted">` + intToStr(unread) + ` unread</span></div>
</div>
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
})();
</script>`
	}

	writeOSHTML(w, adminOSLayout(nonce, "Messages", "messages", cfg, htmpl.HTML(body)))
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
