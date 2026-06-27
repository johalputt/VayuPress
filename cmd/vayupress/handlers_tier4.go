package main

// handlers_tier4.go — enterprise feature handlers (Tier 4):
//   - Read-only GraphQL API (already handled in handlers_graphql.go)
//   - Real-time collaboration / live event stream over SSE (GET /api/v1/stream)
//   - Operator-customisable email templates (GET/PUT /api/v1/admin/email-templates)
//   - Internationalisation catalog (GET/PUT /api/v1/admin/i18n, GET /api/v1/i18n/{lang})
//   - i18n message loading from database on startup

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/emailtmpl"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/ws"
)

// =============================================================================
// Real-time collaboration stream (SSE)
// =============================================================================

// GET /api/v1/stream — Server-Sent Events feed of article mutations and admin
// events. Requires a valid API key (same auth as the write API). Clients receive
// a JSON-encoded ws.Message on each event:
//
//	{"type":"article.created","payload":{"slug":"..."}}
//
// The stream is read-only from the client's perspective: it cannot send data.
// The hub is wired to the event bus in registerEventHandlers().
func (a *App) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if a.collab == nil {
		http.Error(w, "stream not available", http.StatusServiceUnavailable)
		return
	}
	a.collab.ServeHTTP(w, r)
}

// broadcastEvent sends a typed event to all connected SSE clients.
// Called from registerEventHandlers after each article mutation.
func (a *App) broadcastEvent(eventType string, payload interface{}) {
	if a.collab == nil {
		return
	}
	a.collab.Broadcast(ws.Message{Type: eventType, Payload: payload})
}

// =============================================================================
// Email template admin API
// =============================================================================

// GET /api/v1/admin/email-templates — list current template overrides + defaults.
func (a *App) handleEmailTemplateList(w http.ResponseWriter, r *http.Request) {
	kinds := []emailtmpl.Kind{
		emailtmpl.MagicLink,
		emailtmpl.CommentApproved,
		emailtmpl.NewsletterConfirm,
	}
	type entry struct {
		Kind           string `json:"kind"`
		DefaultSubject string `json:"default_subject"`
		DefaultText    string `json:"default_text"`
		DefaultHTML    string `json:"default_html"`
		Subject        string `json:"subject,omitempty"`
		Text           string `json:"text,omitempty"`
		HTML           string `json:"html,omitempty"`
	}
	var out []entry
	for _, k := range kinds {
		row := entry{
			Kind:           string(k),
			DefaultSubject: emailtmpl.DefaultSubject(k),
			DefaultText:    emailtmpl.DefaultText(k),
			DefaultHTML:    emailtmpl.DefaultHTML(k),
		}
		// Pull stored override from DB. ErrNoRows is the benign "no override" case;
		// any other error is surfaced rather than masked as an empty override.
		if err := dbpkg.DB.QueryRowContext(r.Context(),
			`SELECT subject, text_body, html_body FROM email_templates WHERE kind=?`, string(k)).
			Scan(&row.Subject, &row.Text, &row.HTML); err != nil && err != sql.ErrNoRows {
			writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
			return
		}
		out = append(out, row)
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"templates": out})
}

// PUT /api/v1/admin/email-templates/{kind}  {subject, text, html}
// Saves an operator override and hot-reloads the in-memory template store.
func (a *App) handleEmailTemplateSet(w http.ResponseWriter, r *http.Request) {
	kind := emailtmpl.Kind(chi.URLParam(r, "kind"))
	validKinds := map[emailtmpl.Kind]bool{
		emailtmpl.MagicLink:         true,
		emailtmpl.CommentApproved:   true,
		emailtmpl.NewsletterConfirm: true,
	}
	if !validKinds[kind] {
		writeAPIError(w, r, http.StatusBadRequest, "invalid-kind", "Unknown template kind", "")
		return
	}
	var body struct {
		Subject string `json:"subject"`
		Text    string `json:"text"`
		HTML    string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if _, err := dbpkg.DB.ExecContext(r.Context(),
		`INSERT INTO email_templates(kind,subject,text_body,html_body,updated_at)
		 VALUES(?,?,?,?,?) ON CONFLICT(kind) DO UPDATE SET subject=excluded.subject,
		 text_body=excluded.text_body,html_body=excluded.html_body,updated_at=excluded.updated_at`,
		string(kind), body.Subject, body.Text, body.HTML, time.Now().UTC()); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// Hot-reload: update the in-memory store immediately.
	if a.emailTmpl != nil {
		a.emailTmpl.Set(kind, body.Subject, body.Text, body.HTML)
	}
	logging.LogInfo("email-templates", "updated template: "+string(kind))
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok", "kind": string(kind)})
}

// loadEmailTemplateOverrides reads any stored overrides from the DB into the
// in-memory template store. Called once at startup after DB is ready.
func (a *App) loadEmailTemplateOverrides() {
	if a.emailTmpl == nil || dbpkg.DB == nil {
		return
	}
	rows, err := dbpkg.DB.QueryContext(context.Background(),
		`SELECT kind, subject, text_body, html_body FROM email_templates`)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var kind, subject, text, html string
		if err := rows.Scan(&kind, &subject, &text, &html); err != nil {
			continue
		}
		a.emailTmpl.Set(emailtmpl.Kind(kind), subject, text, html)
	}
	_ = rows.Err()
}

// renderEmail renders a transactional email via the in-memory template store.
// Falls back gracefully to built-in defaults when emailTmpl is nil.
func (a *App) renderEmail(kind emailtmpl.Kind, data map[string]interface{}) emailtmpl.Rendered {
	if a.emailTmpl == nil {
		store := emailtmpl.New()
		return store.Render(kind, data)
	}
	return a.emailTmpl.Render(kind, data)
}

// =============================================================================
// Internationalisation (i18n) API
// =============================================================================

// GET /api/v1/i18n/{lang} — returns the merged message map for a language.
// Used by single-page clients or themes that want server-side i18n.
func (a *App) handleI18nMessages(w http.ResponseWriter, r *http.Request) {
	if a.i18n == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "i18n-disabled", "i18n not initialised", "")
		return
	}
	lang := chi.URLParam(r, "lang")
	msgs := a.i18n.Messages(lang)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"lang": lang, "messages": msgs})
}

// GET /api/v1/admin/i18n — list available languages.
func (a *App) handleI18nLanguageList(w http.ResponseWriter, r *http.Request) {
	if a.i18n == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "i18n-disabled", "i18n not initialised", "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"languages": a.i18n.Languages()})
}

// PUT /api/v1/admin/i18n/{lang}  {messages: {"key":"value",...}}
// Upserts messages for a language both in DB and in the in-memory catalog.
func (a *App) handleI18nLanguageSet(w http.ResponseWriter, r *http.Request) {
	if a.i18n == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "i18n-disabled", "i18n not initialised", "")
		return
	}
	lang := chi.URLParam(r, "lang")
	if lang == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-lang", "Language tag required", "")
		return
	}
	var body struct {
		Messages map[string]string `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// Persist each key/value pair.
	tx, err := dbpkg.DB.BeginTx(r.Context(), nil)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	defer tx.Rollback() //nolint:errcheck
	for k, v := range body.Messages {
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO i18n_messages(lang,key,value) VALUES(?,?,?)
			 ON CONFLICT(lang,key) DO UPDATE SET value=excluded.value`,
			lang, k, v); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// Hot-reload in-memory catalog.
	a.i18n.SetLanguage(lang, body.Messages)
	logging.LogInfo("i18n", "updated "+lang+" ("+strconv.Itoa(len(body.Messages))+" keys)")
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status": "ok", "lang": lang, "keys": len(body.Messages),
	})
}

// loadI18nFromDB loads any persisted language messages into the in-memory
// catalog. Called once at startup after DB is ready.
func (a *App) loadI18nFromDB() {
	if a.i18n == nil || dbpkg.DB == nil {
		return
	}
	rows, err := dbpkg.DB.QueryContext(context.Background(),
		`SELECT lang, key, value FROM i18n_messages`)
	if err != nil {
		return
	}
	defer rows.Close()
	// Group into per-language maps, then bulk-install each.
	byLang := map[string]map[string]string{}
	for rows.Next() {
		var lang, key, value string
		if err := rows.Scan(&lang, &key, &value); err != nil {
			continue
		}
		if byLang[lang] == nil {
			byLang[lang] = map[string]string{}
		}
		byLang[lang][key] = value
	}
	_ = rows.Err()
	for lang, msgs := range byLang {
		a.i18n.SetLanguage(lang, msgs)
	}
}
