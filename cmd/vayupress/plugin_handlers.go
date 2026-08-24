// SPDX-License-Identifier: Apache-2.0

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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/comments"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/emailtmpl"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/newsletter"
	"github.com/johalputt/vayupress/internal/settings"
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

// publicComment is the reader-facing projection of a comment: it deliberately
// omits the commenter's email and the finer region/city so the public list and
// submit responses expose only what the widget renders — the author, body, a
// coarse country (for a flag), the moderation status and the timestamp.
type publicComment struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"`
	Author   string `json:"author"`
	Body     string `json:"body"`
	Country  string `json:"country,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Status   string `json:"status,omitempty"`
	// CanEdit/CanDelete tell the widget which controls to show for the current
	// viewer without ever leaking who the author is: CanEdit means "you wrote
	// this" (author-only), CanDelete means "you wrote this OR you moderate the
	// site" (owner or operator). Both default false for an anonymous reader.
	CanEdit   bool      `json:"can_edit,omitempty"`
	CanDelete bool      `json:"can_delete,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func toPublicComment(c *comments.Comment) publicComment {
	return publicComment{
		ID: c.ID, ParentID: c.ParentID, Author: c.Author, Body: c.Body,
		Country: c.Country, Status: c.Status, CreatedAt: c.CreatedAt,
	}
}

// commenterAvatar resolves a public avatar URL for a comment's author by email —
// the real profile photo when the commenter is also a CMS user (typically the
// owner/staff), so their comment shows their actual picture. Readers with no
// account resolve to "" and the widget draws a deterministic initials chip. The
// email itself is never exposed; only the resolved (already-public) URL is.
func (a *App) commenterAvatar(ctx context.Context, email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	// 1) A CMS user / owner with a real profile photo.
	if a.userStore != nil {
		if u, err := a.userStore.GetByEmail(ctx, email); err == nil && u != nil && u.AvatarURL != "" {
			return u.AvatarURL
		}
	}
	// 2) A reader member — their uploaded photo, chosen cartoon, or deterministic
	// auto avatar. Every member has one, so member comments always get a picture.
	// The ?v token busts the short cache the instant they change their avatar.
	if a.members != nil {
		if m, err := a.members.Get(ctx, email); err == nil && m != nil {
			return "/api/v1/members/avatar/" + m.ID + "?v=" + avatarVer(m.AvatarChoice, m.Gender)
		}
	}
	return ""
}

// avatarVer is a short cache-busting token that changes whenever a member's
// avatar selection (choice or gender) changes.
func avatarVer(choice, gender string) string {
	sum := sha256.Sum256([]byte(choice + "|" + gender))
	return hex.EncodeToString(sum[:3])
}

// POST /api/v1/articles/{slug}/comments
func (a *App) handleCommentSubmit(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	if a.siteSettings != nil && !a.siteSettings.FeatureEnabled(r.Context(), settings.ForPrimary(), settings.KeyFeatureComments) {
		writeAPIError(w, r, http.StatusForbidden, "comments-off", "Comments are disabled by the operator", "")
		return
	}
	// Commenting is restricted to authenticated principals. Readers sign in via
	// the membership portal (magic link) or a VayuMail mailbox; the site
	// owner/staff sign in through the VayuOS console. resolveCommenter recognises
	// ALL of them — exactly the set /api/v1/members/me reports as authenticated —
	// so a signed-in operator is no longer shown the "Commenting as …" form and
	// then refused with "please sign in as a member". Anonymous posts are refused.
	who := a.resolveCommenter(r)
	if who == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in as a member to comment", "")
		return
	}
	slug := chi.URLParam(r, "slug")

	var body struct {
		Author   string `json:"author"`
		Email    string `json:"email"`
		Body     string `json:"body"`
		ParentID string `json:"parent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// Identity comes from the authenticated member, not the client payload, so a
	// commenter cannot impersonate someone else. An optional display name is
	// honoured but the email is always the session's.
	author := strings.TrimSpace(body.Author)
	if author == "" {
		author = who.Name
	}

	// Resolve article ID from slug. Drafts are not public, so commenting on one
	// returns the same not-found as a non-existent slug (no existence leak).
	var articleID string
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=? AND COALESCE(status,'published')='published'`, slug).Scan(&articleID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "article-not-found", "No article with that slug", "")
		return
	}

	// GDPR: never store the commenter's IP. Resolve a coarse, privacy-safe
	// location (country offline; region/city from trusted proxy headers) instead.
	geo := geoFromHeaders(r)

	// A reply must point at an existing comment on the same article, so a thread
	// can't be forged across articles. An unknown parent is treated as not-found.
	parentID := strings.TrimSpace(body.ParentID)
	if parentID != "" {
		parent, perr := a.commentStore.Get(r.Context(), parentID)
		if perr != nil || parent.ArticleID != articleID {
			writeAPIError(w, r, http.StatusBadRequest, "bad-parent", "The comment you replied to does not exist", "")
			return
		}
	}

	c, err := a.commentStore.SubmitReply(r.Context(), articleID, parentID, author, who.Email, body.Body, geo.Country, geo.Region, geo.City)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "comment-error", err.Error(), "")
		return
	}
	// Operators/staff already hold moderation power over every comment, so their
	// own comments skip the queue: approve immediately and reflect the approved
	// status in the response so the widget shows the comment live instead of
	// "awaiting moderation". A member's comment still enters moderation as before.
	if who.Operator {
		if merr := a.commentStore.Moderate(r.Context(), c.ID, comments.StatusApproved); merr == nil {
			c.Status = comments.StatusApproved
			// The comment is now live without passing through the console moderation
			// path, so fire the same "you got a reply" notification here — otherwise an
			// operator replying to a member would silently skip it. A no-op for a
			// top-level comment (no parent author to notify).
			go a.notifyCommentReply(context.WithoutCancel(r.Context()), c.ID)
		}
	}
	pc := toPublicComment(c)
	pc.Avatar = a.commenterAvatar(r.Context(), c.Email)
	// The author just posted this, so they can always edit or delete it.
	pc.CanEdit = true
	pc.CanDelete = true
	writeJSON(w, r, http.StatusCreated, pc)
}

// GET /api/v1/articles/{slug}/comments
func (a *App) handleCommentList(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	slug := chi.URLParam(r, "slug")
	// Drafts are not public — listing comments for one returns not-found.
	var articleID string
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=? AND COALESCE(status,'published')='published'`, slug).Scan(&articleID); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "article-not-found", "No article with that slug", "")
		return
	}
	cs, err := a.commentStore.ListApproved(r.Context(), articleID)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	// Public projection: never expose the commenter's email (privacy/GDPR) or the
	// finer region/city — the widget shows only the coarse country (for a flag) and
	// the timestamp. The raw Comment carries email/region/city, so map to a safe DTO
	// before it leaves the server.
	// Who is viewing? Anonymous readers see no edit/delete controls; a signed-in
	// commenter can edit/delete their own comments; an operator can delete any.
	// Resolved once here, then matched per comment by session email (never exposed).
	viewer := a.resolveCommenter(r)
	out := make([]publicComment, 0, len(cs))
	for i := range cs {
		pc := toPublicComment(&cs[i])
		pc.Avatar = a.commenterAvatar(r.Context(), cs[i].Email)
		if viewer != nil {
			mine := cs[i].Email != "" && strings.EqualFold(strings.TrimSpace(viewer.Email), strings.TrimSpace(cs[i].Email))
			pc.CanEdit = mine
			pc.CanDelete = mine || viewer.Operator
		}
		out = append(out, pc)
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"comments": out})
}

// PATCH /api/v1/articles/{slug}/comments/{id}
// Edit the body of your OWN comment. Identity is taken from the session
// (resolveCommenter), never the client payload, so no one can rewrite another
// person's words — an operator's blanket moderation power extends to *deleting*
// any comment, not silently editing what someone said. Status and thread
// position are left unchanged so an edited comment stays in place.
func (a *App) handleCommentEdit(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	who := a.resolveCommenter(r)
	if who == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in to edit your comment", "")
		return
	}
	id := chi.URLParam(r, "id")
	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	c, err := a.commentStore.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Comment not found", "")
		return
	}
	if c.Email == "" || !strings.EqualFold(strings.TrimSpace(who.Email), strings.TrimSpace(c.Email)) {
		writeAPIError(w, r, http.StatusForbidden, "not-your-comment", "You can only edit your own comment", "")
		return
	}
	if err := a.commentStore.UpdateBody(r.Context(), id, body.Body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "edit-error", err.Error(), "")
		return
	}
	c.Body = strings.TrimSpace(body.Body)
	pc := toPublicComment(c)
	pc.Avatar = a.commenterAvatar(r.Context(), c.Email)
	pc.CanEdit = true
	pc.CanDelete = true
	writeJSON(w, r, http.StatusOK, pc)
}

// DELETE /api/v1/articles/{slug}/comments/{id}
// Remove a comment. A member may delete their OWN comment; an operator/staff
// user may delete ANY comment (moderating conflicting or abusive content).
// Deleting a top-level comment also removes its replies so none are orphaned.
func (a *App) handleCommentDelete(w http.ResponseWriter, r *http.Request) {
	if a.commentStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "comments-disabled", "Comments not initialised", "")
		return
	}
	who := a.resolveCommenter(r)
	if who == nil {
		writeAPIError(w, r, http.StatusUnauthorized, "members-only", "Please sign in to delete your comment", "")
		return
	}
	id := chi.URLParam(r, "id")
	c, err := a.commentStore.Get(r.Context(), id)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Comment not found", "")
		return
	}
	owns := c.Email != "" && strings.EqualFold(strings.TrimSpace(who.Email), strings.TrimSpace(c.Email))
	if !owns && !who.Operator {
		writeAPIError(w, r, http.StatusForbidden, "not-allowed", "You can only delete your own comment", "")
		return
	}
	if err := a.commentStore.DeleteThread(r.Context(), id); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "delete-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]bool{"deleted": true})
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
	// Email the commenter when their comment is approved, and — if it is a reply —
	// notify the parent comment's author that they got a response.
	if body.Status == "approved" {
		go a.notifyCommentApproved(context.WithoutCancel(r.Context()), id)
		go a.notifyCommentReply(context.WithoutCancel(r.Context()), id)
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
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=?`, slug).Scan(&articleID); err != nil {
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
	if a.siteSettings != nil && !a.siteSettings.FeatureEnabled(r.Context(), settings.ForPrimary(), settings.KeyFeatureNewsletter) {
		writeAPIError(w, r, http.StatusForbidden, "newsletter-off", "Newsletter signup is disabled by the operator", "")
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

// Webmention ingest limits (audit H4): the receiver is public and anonymous,
// and every accepted submission is a persistent INSERT — so, like every other
// ingest path here, it gets a per-address budget, a body cap, and field caps.
var webmentionIngest = newIngestLimiter(30, time.Minute)

const (
	webmentionMaxBodyLen  = 16 * 1024 // whole submitted form
	webmentionMaxFieldLen = 2048      // per source/target URL
)

// POST /webmention  (W3C endpoint — public, no auth)
func (a *App) handleWebmentionReceive(w http.ResponseWriter, r *http.Request) {
	if a.webmentionStore == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if a.siteSettings != nil && !a.siteSettings.FeatureEnabled(r.Context(), settings.ForPrimary(), settings.KeyFeatureWebmentions) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if !webmentionIngest.allow(auth.ClientIP(r)) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	// Cap the parsed form: r.FormValue spools the entire body while parsing,
	// so without this an anonymous POST could make the server buffer an
	// arbitrarily large request before any validation ran.
	r.Body = http.MaxBytesReader(w, r.Body, webmentionMaxBodyLen)
	source := r.FormValue("source")
	target := r.FormValue("target")
	invalid := func() {
		// One generic rejection for every invalid shape — store errors used to
		// be echoed verbatim to anonymous callers, leaking driver detail.
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid webmention"))
	}
	if source == "" || target == "" || len(source) > webmentionMaxFieldLen || len(target) > webmentionMaxFieldLen {
		invalid()
		return
	}
	// The target must be a page on this site: a webmention notifies the site
	// that something linked to it, so an off-site or hostless target has no
	// business reaching the store.
	tgt, err := url.Parse(target)
	if err != nil || (tgt.Scheme != "http" && tgt.Scheme != "https") {
		invalid()
		return
	}
	host := strings.ToLower(tgt.Hostname())
	primary := strings.ToLower(strings.TrimSpace(config.Cfg.Domain))
	if host == "" || (host != primary && host != strings.ToLower(r.Host)) {
		invalid()
		return
	}
	if _, err := a.webmentionStore.Receive(r.Context(), source, target); err != nil {
		invalid()
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
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT content FROM articles WHERE slug=?`, slug).Scan(&content); err != nil {
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
	err := dbpkg.Reader().QueryRowContext(ctx,
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

// notifyCommentReply emails the author of a parent comment when a reply to it is
// approved, provided that author is a member who has reply notifications on. It
// is a no-op for top-level comments, self-replies, or members who opted out.
func (a *App) notifyCommentReply(ctx context.Context, replyID string) {
	if a.mailer == nil || a.commentStore == nil || a.members == nil {
		return
	}
	reply, err := a.commentStore.Get(ctx, replyID)
	if err != nil || reply.ParentID == "" {
		return
	}
	parent, err := a.commentStore.Get(ctx, reply.ParentID)
	if err != nil || parent.Email == "" {
		return
	}
	// Don't email someone for replying to their own comment.
	if strings.EqualFold(parent.Email, reply.Email) {
		return
	}
	// Respect the parent author's notification preference (members only).
	pm, err := a.members.Get(ctx, parent.Email)
	if err != nil || !pm.ReplyNotify {
		return
	}

	var slug, title string
	_ = dbpkg.Reader().QueryRowContext(ctx,
		`SELECT slug,COALESCE(title,'') FROM articles WHERE id=?`, reply.ArticleID).Scan(&slug, &title)
	if title == "" {
		title = slug
	}
	link := "https://" + config.Cfg.Domain + "/" + slug + "#comments"
	msg := a.renderEmail(emailtmpl.CommentReply, map[string]interface{}{
		"Author":  parent.Author,
		"Replier": reply.Author,
		"Title":   title,
		"Reply":   reply.Body,
		"Link":    link,
	})
	if err := a.mailer.Send(email.Message{
		To:      parent.Email,
		Subject: msg.Subject,
		Text:    msg.Text,
		HTML:    msg.HTML,
	}); err != nil {
		logging.LogError("comments", "reply notification email failed", err.Error())
	}
}
