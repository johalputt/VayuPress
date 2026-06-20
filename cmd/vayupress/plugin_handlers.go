package main

// plugin_handlers.go — HTTP handlers for VayuPress plugin features:
//   - Comments (submit / list / moderate)
//   - Article version history (list / restore)
//   - Collections / Series (CRUD + article membership)
//   - Newsletter subscribers (subscribe / unsubscribe / confirm / list)
//   - Webmentions (receive / list)
//   - Draft preview links (issue / verify)
//   - Redirect rules (CRUD)
//   - Table of Contents (extracted per-article)

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/emailtmpl"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/newsletter"
	"github.com/johalputt/vayupress/internal/toc"
	"github.com/johalputt/vayupress/internal/update"
)

// =============================================================================
// Self-update — READ-ONLY check endpoint (ADR-0064)
//
// This is the ONLY update-related HTTP route. There is deliberately no web path
// that downloads, replaces, or restarts the binary. Applying an update is a
// gated, signature-verified, CLI-only action: `vayupress update apply`.
// =============================================================================

// GET /admin/api/updates/check
func (a *App) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	rel, err := update.CheckLatest(r.Context(), a.outboundClient, "johalputt", "vayupress")
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "update-check-failed", err.Error(), "")
		return
	}
	available := update.UpdateAvailable(Version, rel.Version)

	// Audit the check (best-effort; never blocks the response).
	if a.updateStore != nil {
		_, _ = a.updateStore.Log(r.Context(), update.Record{
			FromVersion: Version,
			ToVersion:   rel.Version,
			Status:      "checked",
		})
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"current":         Version,
		"latest":          rel.Version,
		"updateAvailable": available,
		"changelog":       rel.Notes,
		"url":             rel.URL,
		"published_at":    rel.Published,
		// Applying is CLI-only and signature-verified — see ADR-0064.
		"apply_via": "vayupress update apply",
	})
}

// GET /admin/api/updates/history
func (a *App) handleUpdateHistory(w http.ResponseWriter, r *http.Request) {
	if a.updateStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "update-disabled", "Update store not initialised", "")
		return
	}
	recs, err := a.updateStore.List(r.Context(), 20)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"history": recs})
}

// =============================================================================
// Comments
// =============================================================================

// POST /api/v1/articles/{slug}/comments
func (a *App) handleCommentSubmit(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	slug := chi.URLParam(r, "slug")

	var body struct {
		Author string `json:"author"`
		Email  string `json:"email"`
		Body   string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}

	// Resolve article ID from slug.
	var articleID string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=?`, slug).Scan(&articleID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "article-not-found", "No article with that slug", "")
		return
	}

	ip := r.RemoteAddr
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		ip = strings.Split(fwd, ",")[0]
	}

	c, err := a.commentStore.Submit(r.Context(), articleID, body.Author, body.Email, body.Body, ip)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "comment-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusCreated, c)
}

// GET /api/v1/articles/{slug}/comments
func (a *App) handleCommentList(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	slug := chi.URLParam(r, "slug")
	var articleID string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=?`, slug).Scan(&articleID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "article-not-found", "No article with that slug", "")
		return
	}
	cs, err := a.commentStore.ListApproved(r.Context(), articleID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"comments": cs})
}

// PUT /api/v1/admin/comments/{id}/status
func (a *App) handleCommentModerate(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.commentStore.Moderate(r.Context(), id, body.Status); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "moderate-error", err.Error(), "")
		return
	}
	// Email the commenter when their comment is approved.
	if body.Status == "approved" {
		go a.notifyCommentApproved(r.Context(), id)
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": body.Status})
}

// GET /api/v1/admin/comments?status=pending
func (a *App) handleCommentListAdmin(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	cs, err := a.commentStore.ListAll(r.Context(), status, 200)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"comments": cs})
}

// =============================================================================
// Article Version History
// =============================================================================

// GET /api/v1/admin/articles/{slug}/versions
func (a *App) handleVersionList(w http.ResponseWriter, r *http.Request) {
	if a.versionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "versions-disabled", "Version store not initialised", "")
		return
	}
	slug := chi.URLParam(r, "slug")
	var articleID string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=?`, slug).Scan(&articleID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "article-not-found", "No article with that slug", "")
		return
	}
	limit := 20
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	vs, err := a.versionStore.List(r.Context(), articleID, limit)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"versions": vs})
}

// GET /api/v1/admin/articles/{slug}/versions/{id}
func (a *App) handleVersionGet(w http.ResponseWriter, r *http.Request) {
	if a.versionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "versions-disabled", "Version store not initialised", "")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-id", "Version id must be an integer", "")
		return
	}
	v, err := a.versionStore.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Version not found", "")
		return
	}
	writeJSON(w, r, http.StatusOK, v)
}

// =============================================================================
// Collections / Series
// =============================================================================

// POST /api/v1/collections
func (a *App) handleCollectionCreate(w http.ResponseWriter, r *http.Request) {
	if a.collectionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "collections-disabled", "Collections store not initialised", "")
		return
	}
	var body struct {
		Title       string `json:"title"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	c, err := a.collectionStore.Create(r.Context(), body.Title, body.Slug, body.Description)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusCreated, c)
}

// GET /api/v1/collections
func (a *App) handleCollectionList(w http.ResponseWriter, r *http.Request) {
	if a.collectionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "collections-disabled", "Collections store not initialised", "")
		return
	}
	cs, err := a.collectionStore.List(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"collections": cs})
}

// GET /api/v1/collections/{id}
func (a *App) handleCollectionGet(w http.ResponseWriter, r *http.Request) {
	if a.collectionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "collections-disabled", "Collections store not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	c, err := a.collectionStore.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Collection not found", "")
		return
	}
	writeJSON(w, r, http.StatusOK, c)
}

// POST /api/v1/admin/collections/{id}/articles
func (a *App) handleCollectionAddArticle(w http.ResponseWriter, r *http.Request) {
	if a.collectionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "collections-disabled", "Collections store not initialised", "")
		return
	}
	collID := chi.URLParam(r, "id")
	var body struct {
		ArticleID string `json:"article_id"`
		Position  int    `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if err := a.collectionStore.AddArticle(r.Context(), collID, body.ArticleID, body.Position); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "add-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "added"})
}

// =============================================================================
// Newsletter
// =============================================================================

// POST /api/v1/newsletter/subscribe
func (a *App) handleNewsletterSubscribe(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	sub, isNew, err := a.newsletterStore.Subscribe(r.Context(), body.Email)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "subscribe-error", err.Error(), "")
		return
	}
	// Send the double opt-in confirmation email out-of-band (no-op when SMTP is
	// unconfigured). Only new, unconfirmed subscribers receive a fresh link.
	if isNew && sub != nil && sub.Token != "" {
		go a.sendNewsletterConfirmation(sub.Email, sub.Token)
	}
	code := http.StatusCreated
	if !isNew {
		code = http.StatusOK
	}
	writeJSON(w, r, code, map[string]interface{}{"subscriber": sub, "new": isNew})
}

// sendNewsletterConfirmation emails the double opt-in confirmation link,
// honouring any operator-customised template (Tier 4).
func (a *App) sendNewsletterConfirmation(addr, token string) {
	if a.mailer == nil {
		return
	}
	confirm := "https://" + config.Cfg.Domain + "/api/v1/newsletter/confirm?token=" + token
	msg := a.renderEmail(emailtmpl.NewsletterConfirm, map[string]interface{}{
		"Domain": config.Cfg.Domain,
		"Link":   confirm,
	})
	if err := a.mailer.Send(email.Message{
		To: addr, Subject: msg.Subject, Text: msg.Text, HTML: msg.HTML,
	}); err != nil {
		logging.LogError("newsletter", "confirmation email failed", err.Error())
	}
}

// POST /api/v1/admin/newsletter/broadcast  {subject, text, html}
// Sends a one-off broadcast to every active, confirmed subscriber. Delivery is
// sequential and best-effort; per-recipient failures are counted, not fatal.
func (a *App) handleNewsletterBroadcast(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	if a.mailer == nil || !a.mailer.Enabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "email-disabled", "SMTP not configured — set SMTP_HOST to send broadcasts", "")
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
	if strings.TrimSpace(body.Subject) == "" || strings.TrimSpace(body.Text) == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-fields", "subject and text are required", "")
		return
	}
	subs, err := a.newsletterStore.ListActive(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// Run delivery in the background so the request returns promptly; report the
	// recipient count synchronously.
	go a.deliverBroadcast(subs, body.Subject, body.Text, body.HTML)
	writeJSON(w, r, http.StatusAccepted, map[string]interface{}{
		"queued": len(subs), "status": "sending",
	})
}

func (a *App) deliverBroadcast(subs []newsletter.Subscriber, subject, text, htmlBody string) {
	var sent, failed int
	for _, s := range subs {
		unsub := "https://" + config.Cfg.Domain + "/api/v1/newsletter/unsubscribe?token=" + s.Token
		ftext := text + "\r\n\r\n---\r\nUnsubscribe: " + unsub
		fhtml := htmlBody
		if fhtml != "" {
			fhtml += `<hr><p style="color:#888;font-size:12px"><a href="` + html.EscapeString(unsub) + `">Unsubscribe</a></p>`
		}
		if err := a.mailer.Send(email.Message{To: s.Email, Subject: subject, Text: ftext, HTML: fhtml}); err != nil {
			failed++
		} else {
			sent++
		}
	}
	logging.LogInfo("newsletter", fmt.Sprintf("broadcast complete — sent=%d failed=%d", sent, failed))
}

// GET /api/v1/newsletter/confirm?token=...
func (a *App) handleNewsletterConfirm(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		http.Error(w, "Newsletter not available", http.StatusServiceUnavailable)
		return
	}
	token := r.URL.Query().Get("token")
	if err := a.newsletterStore.Confirm(r.Context(), token); err != nil {
		http.Error(w, "Invalid or expired confirmation link", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?confirmed=1", http.StatusSeeOther)
}

// GET /api/v1/newsletter/unsubscribe?token=...
func (a *App) handleNewsletterUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		http.Error(w, "Newsletter not available", http.StatusServiceUnavailable)
		return
	}
	token := r.URL.Query().Get("token")
	if err := a.newsletterStore.Unsubscribe(r.Context(), token); err != nil {
		http.Error(w, "Invalid unsubscribe link", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/?unsubscribed=1", http.StatusSeeOther)
}

// GET /api/v1/admin/newsletter/subscribers
func (a *App) handleNewsletterList(w http.ResponseWriter, r *http.Request) {
	if a.newsletterStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "newsletter-disabled", "Newsletter not initialised", "")
		return
	}
	subs, err := a.newsletterStore.ListActive(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"subscribers": subs, "count": len(subs)})
}

// =============================================================================
// Webmentions
// =============================================================================

// POST /webmention  (W3C endpoint — public, no auth)
func (a *App) handleWebmentionReceive(w http.ResponseWriter, r *http.Request) {
	if a.webmentionStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	source := r.FormValue("source")
	target := r.FormValue("target")
	if source == "" || target == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("source and target parameters required"))
		return
	}
	if _, err := a.webmentionStore.Receive(r.Context(), source, target); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(err.Error()))
		return
	}
	w.WriteHeader(http.StatusAccepted) // 202 per W3C spec
}

// GET /api/v1/admin/webmentions
func (a *App) handleWebmentionList(w http.ResponseWriter, r *http.Request) {
	if a.webmentionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "webmention-disabled", "Webmention store not initialised", "")
		return
	}
	target := r.URL.Query().Get("target")
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	var ms interface{}
	var err error
	if target != "" {
		ms, err = a.webmentionStore.ListForTarget(r.Context(), target)
	} else {
		ms, err = a.webmentionStore.ListAll(r.Context(), status, 100)
	}
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"webmentions": ms})
}

// PUT /api/v1/admin/webmentions/{id}/status
func (a *App) handleWebmentionModerate(w http.ResponseWriter, r *http.Request) {
	if a.webmentionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "webmention-disabled", "Webmention store not initialised", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid body", "")
		return
	}
	if err := a.webmentionStore.Moderate(r.Context(), id, body.Status); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "moderate-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": body.Status})
}

// =============================================================================
// Draft Preview Links
// =============================================================================

// POST /api/v1/admin/preview
func (a *App) handlePreviewIssue(w http.ResponseWriter, r *http.Request) {
	if a.previewSigner == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "preview-disabled", "Preview signer not initialised", "")
		return
	}
	var body struct {
		Slug string        `json:"slug"`
		TTL  time.Duration `json:"ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid body", "")
		return
	}
	if body.Slug == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-slug", "slug is required", "")
		return
	}
	ttl := body.TTL * time.Hour
	if ttl == 0 {
		ttl = 48 * time.Hour
	}
	token := a.previewSigner.Issue(body.Slug, ttl)
	writeJSON(w, r, http.StatusOK, map[string]string{
		"token": token,
		"url":   "https://" + r.Host + "/" + body.Slug + "?preview=" + token,
	})
}

// GET /{slug}?preview=<token>  — verified in the article handler, this endpoint
// just validates a token for API callers.
// GET /api/v1/preview/verify?token=...&slug=...
func (a *App) handlePreviewVerify(w http.ResponseWriter, r *http.Request) {
	if a.previewSigner == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "preview-disabled", "Preview signer not initialised", "")
		return
	}
	token := r.URL.Query().Get("token")
	parsed, err := a.previewSigner.Verify(token)
	if err != nil {
		writeAPIError(w, r, http.StatusUnauthorized, "invalid-token", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"valid":      true,
		"slug":       parsed.Slug,
		"expires_at": parsed.ExpiresAt,
	})
}

// =============================================================================
// Redirects
// =============================================================================

// GET /api/v1/admin/redirects
func (a *App) handleRedirectList(w http.ResponseWriter, r *http.Request) {
	if a.redirectMgr == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "redirects-disabled", "Redirect manager not initialised", "")
		return
	}
	rules, err := a.redirectMgr.List(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"redirects": rules})
}

// POST /api/v1/admin/redirects
func (a *App) handleRedirectCreate(w http.ResponseWriter, r *http.Request) {
	if a.redirectMgr == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "redirects-disabled", "Redirect manager not initialised", "")
		return
	}
	var body struct {
		FromPath string `json:"from_path"`
		ToPath   string `json:"to_path"`
		Code     int    `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid body", "")
		return
	}
	if body.Code == 0 {
		body.Code = 301
	}
	rule, err := a.redirectMgr.Create(r.Context(), body.FromPath, body.ToPath, body.Code)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "create-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusCreated, rule)
}

// DELETE /api/v1/admin/redirects/{id}
func (a *App) handleRedirectDelete(w http.ResponseWriter, r *http.Request) {
	if a.redirectMgr == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "redirects-disabled", "Redirect manager not initialised", "")
		return
	}
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-id", "id must be an integer", "")
		return
	}
	if err := a.redirectMgr.Delete(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Redirect rule not found", "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// =============================================================================
// Table of Contents
// =============================================================================

// GET /api/v1/articles/{slug}/toc
func (a *App) handleArticleTOC(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var content string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT content FROM articles WHERE slug=?`, slug).Scan(&content); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Article not found", "")
		return
	}
	entries := toc.Extract(content)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"toc": entries, "slug": slug})
}

// notifyCommentApproved emails the commenter (if they provided an address) to
// let them know their comment is live. Runs in a goroutine; all errors are
// logged and discarded so a mail failure never affects the HTTP response.
func (a *App) notifyCommentApproved(ctx context.Context, commentID string) {
	if a.mailer == nil {
		return
	}
	var author, addr, articleSlug string
	err := dbpkg.DB.QueryRowContext(ctx,
		`SELECT c.author, c.email, a.slug
		   FROM comments c
		   JOIN articles a ON a.id = c.article_id
		  WHERE c.id = ?`, commentID).Scan(&author, &addr, &articleSlug)
	if err != nil || addr == "" {
		return
	}
	link := "https://" + config.Cfg.Domain + "/" + articleSlug
	msg := a.renderEmail(emailtmpl.CommentApproved, map[string]interface{}{
		"Author": author,
		"Link":   link,
		"Slug":   articleSlug,
	})
	if err := a.mailer.Send(email.Message{
		To:      addr,
		Subject: msg.Subject,
		Text:    msg.Text,
		HTML:    msg.HTML,
	}); err != nil {
		logging.LogError("comments", "approval email failed", err.Error())
	}
}
