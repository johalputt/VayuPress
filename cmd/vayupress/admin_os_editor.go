// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_editor.go — VayuOS block editor server endpoints (ADR-0068, Phase 3).
//
// The editor is a vanilla-JS, CSP-strict block editor (static/js/admin-os-editor.js).
// The canonical document is a JSON array of typed blocks. On save the server:
//   1. renders the blocks to sanitised HTML via internal/blockrender,
//   2. updates articles.content (so every reader/feed/search path is unchanged),
//   3. persists the raw blocks_json so the editor can re-hydrate losslessly.
//
// Security: block text is escaped + UGC-sanitised in blockrender (never trusted
// verbatim). Saves are session/API-key gated and CSRF-protected.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	htmlpkg "html"
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/api"
	"github.com/johalputt/vayupress/internal/blockrender"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/ui"
)

// authorSelectOptions renders <option> tags for every staff user, marking
// selectedID as selected, for the editor's Author picker. Empty when there is no
// user store.
func (a *App) authorSelectOptions(ctx context.Context, selectedID string) string {
	if a.userStore == nil {
		return ""
	}
	list, err := a.userStore.List(ctx)
	if err != nil {
		return ""
	}
	var sb strings.Builder
	for i := range list {
		u := &list[i]
		name := strings.TrimSpace(u.Name)
		if name == "" {
			name = authorFallbackName(u.Email)
		}
		sel := ""
		if u.ID == selectedID {
			sel = " selected"
		}
		sb.WriteString(`<option value="` + htmlpkg.EscapeString(u.ID) + `"` + sel + `>` + htmlpkg.EscapeString(name) + `</option>`)
	}
	return sb.String()
}

// currentUserIDOf returns the signed-in CMS user's id for the request, or "" for
// an API-key/anonymous caller. Used to attribute a new post to its author.
func currentUserIDOf(r *http.Request) string {
	if u := currentUser(r); u != nil {
		return u.ID
	}
	return ""
}

// loadBlocksJSON returns the stored block document for a slug, or "" if the
// article predates the block editor (or does not exist).
func loadBlocksJSON(ctx context.Context, slug string) string {
	if dbpkg.DB == nil {
		return ""
	}
	var bj string
	_ = dbpkg.Reader().QueryRowContext(ctx,
		`SELECT COALESCE(blocks_json,'') FROM articles WHERE slug = ?`, slug).Scan(&bj)
	return bj
}

// persistBlocksJSON writes the raw block document for a slug. It is a direct
// column update: the rendered HTML is saved through the normal article service
// so the write pipeline (cache purge, search index, feeds) stays authoritative.
func persistBlocksJSON(ctx context.Context, slug, blocksJSON string) error {
	if dbpkg.DB == nil {
		return nil
	}
	_, err := dbpkg.DB.ExecContext(ctx,
		`UPDATE articles SET blocks_json = ? WHERE slug = ?`, blocksJSON, slug)
	return err
}

// handleOSEditorSave persists a block document for an existing article. It
// renders blocks → HTML, updates the article content+title via the service,
// then stores the raw blocks for re-hydration.
func (a *App) handleOSEditorSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug        string            `json:"slug"`
		Title       string            `json:"title"`
		Blocks      []json.RawMessage `json:"blocks"`
		Tags        []string          `json:"tags"`
		PublishDate string            `json:"publishDate"`
		Meta        *PostMeta         `json:"meta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	isNew := slug == ""

	// Normalise tags: trim, drop blanks. A nil slice leaves tags unchanged on
	// update; a non-nil (possibly empty) slice replaces them (allows clearing).
	var tags []string
	if body.Tags != nil {
		tags = splitCSVTags(strings.Join(body.Tags, ","))
		if tags == nil {
			tags = []string{}
		}
	}

	// Re-marshal the blocks array to a canonical JSON string for storage+render.
	blocksJSON := "[]"
	if len(body.Blocks) > 0 {
		if raw, err := json.Marshal(body.Blocks); err == nil {
			blocksJSON = string(raw)
		}
	}

	// Make pasted third-party image links robust: resolve any image/gallery block
	// whose URL is a *page* (e.g. a Pixabay/Unsplash photo page) to the direct
	// image it advertises, so it renders instead of showing a broken image. This
	// only rewrites URL strings — nothing is downloaded or re-hosted — and is
	// bounded by a short deadline so a slow host never blocks the save.
	imgCtx, cancelImg := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancelImg()
	blocksJSON = a.resolveBlockImages(imgCtx, blocksJSON)

	contentHTML, _, err := blockrender.Render(blocksJSON)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "render-error", "Could not render blocks: "+err.Error(), "")
		return
	}

	// Auto-hero: when the author left the feature image blank, adopt the first
	// image in the body so the post gets a hero automatically; also resolve a
	// page-link feature image to its direct image. Storage-neutral (URL only).
	a.ensureFeatureImage(imgCtx, body.Meta, contentHTML)

	title := strings.TrimSpace(body.Title)

	// ── Native create path (no slug) ─────────────────────────────────────────
	// A brand-new post is created here through the same authoritative article
	// service the API uses — so /os owns the create flow end to end and no
	// longer delegates to the legacy editor. A title is required to derive the
	// slug; article validation needs non-empty content, so an empty document is
	// seeded with a single space that renders to nothing.
	if isNew {
		if title == "" {
			writeAPIError(w, r, http.StatusBadRequest, "missing-title", "A title is required to create a post", "")
			return
		}
		slug = a.uniqueArticleSlug(r.Context(), title)
		seed := contentHTML
		if strings.TrimSpace(seed) == "" {
			seed = " "
		}
		// Draft-first authoring (dashboard-upgrade Wave 1): a brand-new post is
		// born a DRAFT — the first ⌘S must never publish a half-thought to the
		// live site, the disk cache or RSS. Publishing happens via the explicit
		// topbar control (or the Posts manager), never as a side effect of saving.
		if _, err := a.articles.CreateDraft(r.Context(), title, slug, seed, tags); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
			return
		}
		if err := persistBlocksJSON(r.Context(), slug, blocksJSON); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "persist-error", err.Error(), "")
			return
		}
		a.applyPostExtras(r.Context(), slug, body.Meta, body.PublishDate, tags, currentUserIDOf(r))
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "created", "slug": slug, "postStatus": "draft"})
		return
	}

	// ── Update path (existing slug) ──────────────────────────────────────────
	// Ownership, before anything is written. Everything above this point creates;
	// everything below it overwrites somebody's page.
	if a.refuseArticleWrite(w, r, slug) {
		return
	}
	var titlePtr *string
	if title != "" {
		titlePtr = &title
	}
	if _, err := a.articles.Update(r.Context(), slug, titlePtr, &contentHTML, tags); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "update-error", err.Error(), "")
		return
	}

	// Persist the raw block document for lossless re-hydration.
	if err := persistBlocksJSON(r.Context(), slug, blocksJSON); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "persist-error", err.Error(), "")
		return
	}

	a.applyPostExtras(r.Context(), slug, body.Meta, body.PublishDate, tags, currentUserIDOf(r))
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "saved", "slug": slug})
}

// applyPostExtras persists the publishing-options side-car (PostMeta), an
// optional publish-date override, and purges the public caches so the article's
// head metadata / share cards refresh immediately. Each step is best-effort and
// independent of the queued content write (they touch disjoint columns).
func (a *App) applyPostExtras(ctx context.Context, slug string, meta *PostMeta, publishDate string, tags []string, editorID string) {
	if meta != nil {
		// Multi-author attribution: keep any author already assigned; otherwise
		// attribute the post to whoever is editing it (never re-attribute an
		// already-owned post, and never blank it out when the client omits it).
		if strings.TrimSpace(meta.AuthorID) == "" {
			if existing := loadPostMeta(ctx, slug).AuthorID; existing != "" {
				meta.AuthorID = existing
			} else {
				meta.AuthorID = editorID
			}
		}
		if err := savePostMeta(ctx, slug, *meta); err != nil {
			logging.LogError("os-editor", "save post meta failed", err.Error())
		}
	}
	if t, ok := parsePublishDate(publishDate); ok {
		if err := setPublishDate(ctx, slug, t); err != nil {
			logging.LogError("os-editor", "set publish date failed", err.Error())
		}
	}
	// Refresh the public surfaces (article page head meta, home cards, feeds).
	render.CachePurge(slug, tags, generateSitemap, generateRSS, generateRobots)
}

// parsePublishDate accepts the editor's datetime-local value (and a few common
// variants), interpreting a bare wall-clock time as UTC. A blank or unparseable
// value returns ok=false so the existing created_at is left untouched.
func parsePublishDate(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

// handleOSPostStatus publishes or unpublishes (drafts) an article from the post
// manager. Unpublishing hides it from every public surface; both directions
// purge the public caches so the change is immediately visible.
// errPostNotFound marks a slug with no article row, so the single-post and bulk
// endpoints can both map it to a 404 / per-item failure without duplicating the
// lookup.
var errPostNotFound = errors.New("no article with that slug")

// applyPostStatus flips one post's published/draft state and performs the
// shared side effects: cache purge, IndexNow ping on publish, status-toggle
// metric. Shared by the JSON endpoint, the HTMX fragment endpoint and the bulk
// endpoint, so the three can never disagree about what a status flip does.
func (a *App) applyPostStatus(ctx context.Context, slug, status string) error {
	var tagsCSV string
	if err := dbpkg.Reader().QueryRowContext(ctx, `SELECT COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&tagsCSV); err != nil {
		return errPostNotFound
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET status=?, updated_at=? WHERE slug=?`, status, time.Now().UTC(), slug); err != nil {
		return err
	}
	// Purge public caches (article page, homepage, tag pages, sitemap, feed) so
	// an unpublish disappears — and a publish appears — without delay.
	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	// Publishing a (previously draft) post makes its URL public for the first
	// time — announce it to IndexNow so search engines crawl it promptly. The
	// status-toggle path emits no ArticleUpdated event, so without this a newly
	// published post would never be submitted. pingIndexNow re-checks that the
	// post is published, so unpublishing never pings. The goroutine is tracked
	// on a.bgWG so tests can drain it instead of leaking it across harnesses.
	if status == "published" {
		a.goPingIndexNow(slug)
	}
	atomic.AddInt64(&metrics.MetricPostStatusToggles, 1)
	return nil
}

func (a *App) handleOSPostStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug   string `json:"slug"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	status := strings.TrimSpace(body.Status)
	if slug == "" || (status != "published" && status != "draft") {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", "slug and a valid status (published|draft) are required", "")
		return
	}
	if err := a.applyPostStatus(r.Context(), slug, status); err != nil {
		if err == errPostNotFound {
			writeAPIError(w, r, http.StatusNotFound, "not-found", "No article with that slug", "")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "update-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": status, "slug": slug})
}

// handleOSPostToggleFragment is the HTMX counterpart to handleOSPostStatus: it
// flips a single post's published/draft status and returns an HTML fragment —
// the flipped toggle button plus out-of-band updates of the post's state in its
// row and in the inspector — so the Posts list updates in place. The
// JSON handler above stays in use for the bulk actions. CSRF is enforced by the
// route's CSRFTokenMiddleware (the admin layout mirrors the vp_csrf cookie into
// the X-CSRF-Token header for every hx-* request).
func (a *App) handleOSPostToggleFragment(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	status := strings.TrimSpace(r.FormValue("status"))
	// Strict slug allowlist before the slug is used or reflected into the fragment.
	if !api.IsValidSlug(slug) || (status != "published" && status != "draft") {
		http.Error(w, "a valid slug and status (published|draft) are required", http.StatusBadRequest)
		return
	}
	if err := a.applyPostStatus(r.Context(), slug, status); err != nil {
		if err == errPostNotFound {
			http.Error(w, "no article with that slug", http.StatusNotFound)
			return
		}
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	esc := htmlpkg.EscapeString(slug)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, osPostStatusButton(esc, status)+osPostStatusOOB(esc, status))
}

// handleOSPostShare issues a signed preview link for a DRAFT through the same
// signer the API uses, so a draft can be shared with a reviewer without making
// it public (Wave 4.4). Published posts are refused with a clear message —
// they are already reachable, and a "share" that shares nothing but a token is
// a lie. The link expires (48h default) and stops working the moment the
// signer's tokens do.
func (a *App) handleOSPostShare(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if !api.IsValidSlug(slug) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-slug", "A valid slug is required", "")
		return
	}
	var status string
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT COALESCE(status,'published') FROM articles WHERE slug=?`, slug).Scan(&status); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No article with that slug", "")
		return
	}
	if status != "draft" {
		writeAPIError(w, r, http.StatusConflict, "not-a-draft", "Only drafts can be shared — this post is already public at /"+slug, "")
		return
	}
	if a.previewSigner == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "preview-disabled", "Preview links are not available on this install", "")
		return
	}
	token := a.previewSigner.Issue(slug, 48*time.Hour)
	writeJSON(w, r, http.StatusOK, map[string]string{
		"token": token,
		"url":   "https://" + r.Host + "/" + slug + "?preview=" + token,
		"ttl":   "48h",
	})
}

// handleOSPostIndexNowFragment lets an operator manually (re-)submit a single
// post's URL to IndexNow from the Posts manager — for when the automatic
// on-publish ping did not go through (no key at the time, a transient failure,
// or a proxy challenge). It submits synchronously so the result is immediate,
// then returns the flipped button plus an out-of-band update of the post's
// IndexNow state. CSRF is enforced by the route's CSRFTokenMiddleware.
func (a *App) handleOSPostIndexNowFragment(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if !api.IsValidSlug(slug) {
		http.Error(w, "a valid slug is required", http.StatusBadRequest)
		return
	}
	var status string
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT COALESCE(status,'published') FROM articles WHERE slug=?`, slug).Scan(&status); err != nil {
		http.Error(w, "no article with that slug", http.StatusNotFound)
		return
	}
	isDraft := status != "published"
	// Submit now; pingIndexNow records the outcome to indexnow_submissions.
	state, detail := a.pingIndexNow(slug)
	st, ok := dbpkg.IndexNowStatusOf(slug)
	if !ok && state == "skipped" {
		// Nothing was recorded (e.g. no IndexNow key configured, or a read-only
		// mode). Surface the reason inline so the operator isn't left guessing.
		st, ok = dbpkg.IndexNowStatus{State: dbpkg.IndexNowFailed, Detail: detail}, true
	}
	esc := htmlpkg.EscapeString(slug)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, osIndexNowButton(esc, st, ok, isDraft)+osIndexNowStateOOB(esc, st, ok, isDraft))
}

// handleOSPostPinFragment is the HTMX counterpart to handleOSPostPin: it flips a
// post's featured (pinned) flag and returns an HTML fragment — the flipped pin
// button plus an out-of-band update of the row's pin mark — so the Posts list
// updates in place. The JSON handler
// below stays in use for the bulk/editor paths. CSRF is enforced by the route's
// CSRFTokenMiddleware.
func (a *App) handleOSPostPinFragment(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	pinned := strings.TrimSpace(r.FormValue("pinned"))
	// Strict slug allowlist before the slug is used or reflected into the fragment.
	if !api.IsValidSlug(slug) || (pinned != "0" && pinned != "1") {
		http.Error(w, "a valid slug and pinned (0|1) are required", http.StatusBadRequest)
		return
	}
	var tagsCSV string
	if err := dbpkg.Reader().QueryRowContext(r.Context(), `SELECT COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&tagsCSV); err != nil {
		http.Error(w, "no article with that slug", http.StatusNotFound)
		return
	}
	featured := pinned == "1"
	f := 0
	if featured {
		f = 1
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET featured=?, updated_at=? WHERE slug=?`, f, time.Now().UTC(), slug); err != nil {
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	// Same public-surface refresh as the JSON path so the trending/pinned widget
	// reflects the change immediately.
	invalidateTrendingCache()
	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	atomic.AddInt64(&metrics.MetricPostPinToggles, 1)
	esc := htmlpkg.EscapeString(slug)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, osPostPinButton(esc, featured)+osPostPinMark(esc, featured, true))
}

// handleOSPostDelete permanently removes a post (or page) from the VayuOS
// manager. It is synchronous so the list reflects the deletion immediately:
// the article row carries its own blocks_json + publishing-options columns, so
// the row delete cleans those up; its comments are removed too. Public caches
// (article page, home, tags, sitemap, feed) are purged so the post disappears
// from the live site at once. Refused in read-only / quarantined mode.
func (a *App) handleOSPostDelete(w http.ResponseWriter, r *http.Request) {
	if cur := mode.Global.Current(); cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeAPIError(w, r, http.StatusServiceUnavailable, "read-only", "posts cannot be deleted in "+string(cur)+" mode", "")
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if slug == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", "a slug is required", "")
		return
	}
	if err := a.deletePostBySlug(r.Context(), r, slug); err != nil {
		if err == errPostNotFound {
			writeAPIError(w, r, http.StatusNotFound, "not-found", "No post with that slug", "")
			return
		}
		var refused *refusedWriteError
		if errors.As(err, &refused) {
			writeAPIError(w, r, http.StatusForbidden, "not-your-post", refused.Error(), "")
			return
		}
		writeAPIError(w, r, http.StatusInternalServerError, "delete-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "deleted", "slug": slug})
}

// refusedWriteError marks a per-slug write refusal (ownership or mode), so the
// single-post and bulk endpoints can map it to 403 without re-running the
// predicate or writing to the response from inside the helper.
type refusedWriteError struct{ reason string }

func (e *refusedWriteError) Error() string { return e.reason }

// deletePostBySlug removes one post and its comments, purges the public caches
// and writes the audit trail. Shared by the single DELETE endpoint and the bulk
// endpoint, so bulk deletion can never drift from single deletion. Deletion is
// the irreversible half: there is no snapshot to restore from, and the comments
// go with it.
func (a *App) deletePostBySlug(ctx context.Context, r *http.Request, slug string) error {
	if cur := mode.Global.Current(); cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		return errors.New("posts cannot be deleted in " + string(cur) + " mode")
	}
	var id, tagsCSV string
	if err := dbpkg.Reader().QueryRowContext(ctx, `SELECT id,COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&id, &tagsCSV); err != nil {
		return errPostNotFound
	}
	if reason := a.articleWriteRefusal(r, slug); reason != "" {
		dbpkg.AuditLog("article.write.refused", dbpkg.AuditActor(r), slug, reason)
		return &refusedWriteError{reason: reason}
	}
	if _, err := dbpkg.WDB.Exec(`DELETE FROM articles WHERE slug=?`, slug); err != nil {
		return err
	}
	// Best-effort cleanup of the post's comments (orphans otherwise).
	_, _ = dbpkg.WDB.Exec(`DELETE FROM comments WHERE article_id=?`, id)

	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	dbpkg.AuditLog("article.delete", dbpkg.AuditActor(r), slug, "id="+id)
	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "editor", Severity: "info",
		Msg: "post deleted: " + slug, RequestID: getRequestID(r),
	})
	return nil
}

// osBulkMaxSlugs caps one bulk request: the batch is N single-post operations
// (each with a cache purge), and an unbounded list would let one request pin
// the server for minutes. 200 covers any page of the posts manager (100/page)
// with headroom for a multi-page selection.
const osBulkMaxSlugs = 200

// bulkPostResult is the per-slug outcome of a bulk request.
type bulkPostResult struct {
	Slug   string `json:"slug"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Status string `json:"status,omitempty"`
	Pill   string `json:"pill,omitempty"`
	Button string `json:"button,omitempty"`
}

// handleOSPostsBulk is the ONE-request bulk endpoint for the Posts manager:
// POST /os/api/posts/bulk {"action":"published"|"draft"|"delete","slugs":[…]}.
// It replaces the client-side loop of N parallel fetches, which raced the
// server, gave no per-slug outcomes, and could only recover by reloading the
// page away. Each slug is applied independently through the SAME helpers as the
// single-post endpoints (applyPostStatus / deletePostBySlug), so a failure is
// reported per slug and the successes stand. Status actions additionally return
// each slug's flipped pill + toggle button (the same markup the HTMX fragment
// endpoint returns) so the rows update in place without a reload, plus the
// recomputed All/Published/Drafts counts so the tabs stay honest.
func (a *App) handleOSPostsBulk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string   `json:"action"`
		Slugs  []string `json:"slugs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	action := strings.TrimSpace(body.Action)
	if action != "published" && action != "draft" && action != "delete" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", "action must be published, draft or delete", "")
		return
	}
	if len(body.Slugs) == 0 || len(body.Slugs) > osBulkMaxSlugs {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input",
			"slugs must contain between 1 and "+strconv.Itoa(osBulkMaxSlugs)+" slugs", "")
		return
	}
	seen := make(map[string]struct{}, len(body.Slugs))
	slugs := make([]string, 0, len(body.Slugs))
	for _, s := range body.Slugs {
		s = strings.TrimSpace(s)
		if !api.IsValidSlug(s) {
			writeAPIError(w, r, http.StatusBadRequest, "bad-input", "every slug must be a valid post slug", "")
			return
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		slugs = append(slugs, s)
	}

	results := make([]bulkPostResult, 0, len(slugs))
	okN := 0
	for _, slug := range slugs {
		res := bulkPostResult{Slug: slug}
		var err error
		if action == "delete" {
			err = a.deletePostBySlug(r.Context(), r, slug)
		} else {
			err = a.applyPostStatus(r.Context(), slug, action)
		}
		if err != nil {
			res.Error = err.Error()
		} else {
			res.OK = true
			okN++
			if action != "delete" {
				res.Status = action
				res.Pill = osPostState(action)
				res.Button = osPostStatusButton(htmlpkg.EscapeString(slug), action)
			}
		}
		results = append(results, res)
	}

	resp := struct {
		OK      int              `json:"ok"`
		Failed  int              `json:"failed"`
		Results []bulkPostResult `json:"results"`
		Counts  map[string]int   `json:"counts,omitempty"`
	}{OK: okN, Failed: len(slugs) - okN, Results: results}
	if action != "delete" {
		// The tabs must describe the catalogue AS IT NOW IS, not as it was when
		// the page loaded — otherwise the numbers lie the moment the batch lands.
		if counts, err := postStatusCounts(r.Context()); err == nil {
			resp.Counts = counts
		}
	}
	writeJSON(w, r, http.StatusOK, resp)
}

// postStatusCounts recomputes the All/Published/Drafts tab counts. status is
// NOT NULL DEFAULT 'published' (migration 030), so the query groups by the bare
// column — COALESCE would defeat idx_articles_status and force a full scan (the
// same reasoning as the posts page's count query).
func postStatusCounts(ctx context.Context) (map[string]int, error) {
	counts := map[string]int{"all": 0, "published": 0, "draft": 0}
	rows, err := dbpkg.Reader().QueryContext(ctx, `SELECT status, COUNT(1) FROM articles GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		var c int
		if err := rows.Scan(&s, &c); err != nil {
			return nil, err
		}
		counts["all"] += c
		if s == "draft" {
			counts["draft"] += c
		} else {
			counts["published"] += c
		}
	}
	return counts, rows.Err()
}

// splitCSVTags splits a stored comma-separated tag string into a slice.
func splitCSVTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// uniqueArticleSlug derives a URL slug from title and ensures it does not collide
// with an existing article, appending -2, -3, … as needed. Shared by the native
// editor create path and quick-create.
func (a *App) uniqueArticleSlug(ctx context.Context, title string) string {
	slug := migrateSlugify(title)
	if slug == "" {
		slug = "untitled-" + strconv.FormatInt(time.Now().Unix(), 36)
	}
	base := slug
	for i := 2; i <= 99; i++ {
		if _, err := a.articles.Get(ctx, slug); err != nil {
			break // available
		}
		slug = base + "-" + strconv.Itoa(i)
	}
	return slug
}

// handleOSEditorImport converts an editor-supplied HTML string into a block
// document and returns it, without persisting anything. It backs the editor's
// one-click HTML source mode: the operator edits raw HTML and, on switching back
// to the visual canvas, that HTML is parsed into blocks here. The conversion is
// the same conservative importer used for legacy posts and now preserves inline
// formatting (bold / italic / code / strike / links) as Markdown, so a
// visual → HTML → visual round-trip is lossless for common formatting.
func (a *App) handleOSEditorImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HTML string `json:"html"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	blocks := blockrender.ImportHTML(body.HTML)
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"blocks": blocks})
}

// handleOSEditorPreview renders a block document to sanitised HTML without
// persisting anything — used by the editor's live preview pane.
func (a *App) handleOSEditorPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	blocksJSON := "[]"
	if len(body.Blocks) > 0 {
		if raw, err := json.Marshal(body.Blocks); err == nil {
			blocksJSON = string(raw)
		}
	}
	contentHTML, excerpt, err := blockrender.Render(blocksJSON)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "render-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"html": contentHTML, "excerpt": excerpt})
}

// handleOSEditorAI proxies an AI writing-assist request for os session-cookie
// operators. The backing model is opt-in (VAYU_AI_URL); when absent the handler
// returns 503 so the editor UI can degrade gracefully.
func (a *App) handleOSEditorAI(w http.ResponseWriter, r *http.Request) {
	if a.aiAssist == nil || !a.aiAssist.Enabled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "ai-disabled", "AI assistant not configured (set VAYU_AI_URL)", "")
		return
	}
	var body struct {
		Op   string `json:"op"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	result, err := a.aiAssist.Assist(r.Context(), body.Op, body.Text)
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "ai-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"op": body.Op, "result": result})
}

// handleOSEditorVersionList returns the version list for a slug, session-gated.
func (a *App) handleOSEditorVersionList(w http.ResponseWriter, r *http.Request) {
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
	vs, err := a.versionStore.List(r.Context(), articleID, 30)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "db-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"versions": vs})
}

// handleOSEditorVersionGet returns a single version by ID, session-gated.
func (a *App) handleOSEditorVersionGet(w http.ResponseWriter, r *http.Request) {
	if a.versionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "versions-disabled", "Version store not initialised", "")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
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

// handleOSEditorVersionRestore rewinds an article to a stored snapshot
// (dashboard-upgrade Wave 1): history that can only be read is documentation,
// not a safety net. The article's title/content/tags return to the snapshot and
// the block document is rebuilt from the restored HTML so the editor rehydrates
// what was actually restored (ImportHTML is conservative; anything it cannot
// express becomes markdown/raw-HTML blocks rather than being dropped).
func (a *App) handleOSEditorVersionRestore(w http.ResponseWriter, r *http.Request) {
	if a.versionStore == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "versions-disabled", "Version store not initialised", "")
		return
	}
	if a.articles == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "article service not initialised", "")
		return
	}
	slug := strings.TrimSpace(chi.URLParam(r, "slug"))
	if !api.IsValidSlug(slug) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", "A valid slug is required", "")
		return
	}
	// Same ownership gate as every other destructive write on this surface.
	if a.refuseArticleWrite(w, r, slug) {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-id", "Version id must be an integer", "")
		return
	}
	v, err := a.versionStore.Get(r.Context(), id)
	if err != nil || v == nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "Version not found", "")
		return
	}
	if v.Slug != slug {
		writeAPIError(w, r, http.StatusBadRequest, "version-mismatch", "That version belongs to a different post", "")
		return
	}
	restoredTags := v.Tags
	if restoredTags == nil {
		restoredTags = []string{}
	}
	if _, err := a.articles.Update(r.Context(), slug, &v.Title, &v.Content, restoredTags); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "restore-error", err.Error(), "")
		return
	}
	// Best-effort hydration document: the canonical article content above is the
	// source of truth for readers; this keeps the editor's canvas faithful.
	blocks := blockrender.ImportHTML(v.Content)
	blocksJSON := "[]"
	if len(blocks) > 0 {
		if raw, mErr := json.Marshal(blocks); mErr == nil {
			blocksJSON = string(raw)
		}
	}
	if err := persistBlocksJSON(r.Context(), slug, blocksJSON); err != nil {
		logging.LogError("os-editor", "restore blocks_json persist failed", err.Error())
	}
	render.CachePurge(slug, restoredTags, generateSitemap, generateRSS, generateRobots)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "restored", "slug": slug})
}

// osEditorBody builds the block-editor shell. The editor hydrates from the
// <script type="application/json" id="vp-editor-data"> document on first paint;
// an empty value starts a fresh document.
// osEditorHeadTmpl renders the interpolated head of the editor shell through
// html/template so every value passes a recognised escaping barrier:
//   - .Blocks is emitted in the <script type="application/json"> context, where
//     html/template turns HTML-significant bytes (<, >, &, U+2028/9) into \uXXXX
//     escapes that JSON.parse reverses — so </script> can never break out, yet
//     the document round-trips losslessly.
//   - .Slug and .Title are attribute-escaped (double quotes become &#34;).
//
// The shell is the page grammar's document kind (render 02): a quiet bar with
// the way back, the save state, the length, Preview and Publish; the post at
// reading measure; and the inspector beside it. Everything the old bar's eleven
// buttons did is still one keystroke or one menu away: formatting is the
// selection bar and the slash menu, and the modes, AI, image, history and
// sharing are in the bar's More menu, so the bar carries only what is used on
// every visit.
var osEditorHeadTmpl = htmpl.Must(htmpl.New("oseditorhead").Parse(
	`<script type="application/json" id="vp-editor-data">{{.Blocks}}</script>
<div class="editor-shell sa-doc" data-page-kind="document" data-editor data-slug="{{.Slug}}">
  <h1 class="vp-sr-only">{{if .Title}}{{.Title}}{{else}}New post{{end}}</h1>
  <div class="sa-doc__bar">
    <a class="sa-doc__back" href="{{.BackHref}}">{{.ChevIcon}}{{.BackLabel}}</a>
    <span class="sa-doc__state" data-editor-topbar-status role="status" aria-live="polite"></span>
    <span class="sa-doc__fill"></span>
    <span class="editor-wordcount" data-editor-wordcount></span>
    <button type="button" class="btn btn--ghost btn--sm" data-editor-preview-btn>Preview</button>
    <details class="sa-pop editor-more">
      <summary class="btn btn--ghost btn--sm btn--icon" aria-label="More">{{.MoreIcon}}</summary>
      <div class="sa-pop__panel sa-menu" role="menu">
        <button type="button" class="sa-menu__item" role="menuitemcheckbox" aria-checked="false" data-editor-focus-btn>Focus<span class="sa-menu__meta">⌘.</span></button>
        <button type="button" class="sa-menu__item" role="menuitemcheckbox" aria-checked="false" data-editor-split-btn>Preview beside the text</button>
        <button type="button" class="sa-menu__item" role="menuitemcheckbox" aria-checked="false" data-editor-md-btn>Edit as Markdown<span class="sa-menu__meta">⌘⇧M</span></button>
        <button type="button" class="sa-menu__item" role="menuitemcheckbox" aria-checked="false" data-editor-html-btn>Edit as HTML<span class="sa-menu__meta">⌘⇧H</span></button>
        <div class="sa-menu__sep"></div>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-image-btn>Insert an image</button>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-ai-btn>Write a draft with AI</button>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-share-btn>Copy a preview link</button>
        <div class="sa-menu__sep"></div>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-undo disabled>Undo<span class="sa-menu__meta">⌘Z</span></button>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-redo disabled>Redo<span class="sa-menu__meta">⌘⇧Z</span></button>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-history-btn>Version history</button>
        <div class="sa-menu__sep"></div>
        <button type="button" class="sa-menu__item" role="menuitem" data-editor-newpage>New page</button>
      </div>
    </details>
    <button type="button" class="btn btn--ghost btn--sm btn--icon" data-editor-settings-btn aria-pressed="true" aria-label="Post settings" title="Post settings (⌘⇧P)">{{.PanelIcon}}</button>
    <button type="button" class="btn btn--ghost btn--sm" data-editor-save title="Save (⌘S)">Save</button>
    <button type="button" class="btn btn--primary btn--sm" data-editor-publish-btn hidden></button>
  </div>
  <div class="sa-doc__body">
  <div class="sa-doc__main sa-doc__main--text" data-editor-scroller>
    <input class="editor-title" data-editor-title type="text" placeholder="Title" value="{{.Title}}" aria-label="Post title">
    <p class="editor-byline" data-editor-byline></p>
    <div class="editor-workspace">
      <div class="editor-canvas" data-editor-canvas aria-label="Editor canvas"></div>
      <aside class="editor-live" data-editor-live hidden aria-label="Live preview">
        <div class="editor-live-head">Live preview</div>
        <article class="editor-live-body article" data-editor-live-body></article>
      </aside>
      <section class="editor-html" data-editor-html-panel hidden aria-label="HTML source editor">
        <div class="editor-html-head">
          <span class="editor-html-title">HTML source</span>
          <span class="text-xs muted">Switch back to apply it to your blocks.</span>
        </div>
        <textarea class="editor-html-area" data-editor-html-area spellcheck="false" autocomplete="off" autocapitalize="off" wrap="soft" aria-label="HTML source"></textarea>
      </section>
      <section class="editor-html" data-editor-md-panel hidden aria-label="Markdown source editor">
        <div class="editor-html-head">
          <span class="editor-html-title">Markdown</span>
          <span class="text-xs muted">Switch back to apply it to your blocks.</span>
        </div>
        <textarea class="editor-html-area" data-editor-md-area spellcheck="false" autocomplete="off" autocapitalize="off" wrap="soft" aria-label="Markdown source"></textarea>
      </section>
    </div>
  </div>`))

// osEditorMetaTmpl emits the publishing-options hydration document in the same
// JSON-in-script context the block document uses: html/template escapes the
// HTML-significant bytes so the values cannot break out of <script>, while
// JSON.parse reverses the escaping client-side.
var osEditorMetaTmpl = htmpl.Must(htmpl.New("oseditormeta").Parse(
	`<script type="application/json" id="vp-editor-meta">{{.Meta}}</script>`))

// osEditorMetaScript serialises a post's settings (tags, publish date, status,
// and the PostMeta side-car) for the editor's Post-settings panel to hydrate.
func osEditorMetaScript(slug, status string, createdAt time.Time, tags []string, m PostMeta) string {
	if tags == nil {
		tags = []string{}
	}
	pub := ""
	if !createdAt.IsZero() {
		pub = config.FormatSite(createdAt, "2006-01-02T15:04")
	}
	payload := struct {
		Slug        string   `json:"slug"`
		Status      string   `json:"status"`
		Tags        []string `json:"tags"`
		PublishDate string   `json:"publishDate"`
		PostMeta
	}{Slug: slug, Status: status, Tags: tags, PublishDate: pub, PostMeta: m}
	raw, err := json.Marshal(payload)
	if err != nil {
		raw = []byte("{}")
	}
	var sb strings.Builder
	_ = osEditorMetaTmpl.Execute(&sb, struct{ Meta json.RawMessage }{json.RawMessage(raw)})
	return sb.String()
}

func osEditorBody(slug, title, blocksJSON, authorOptions string, isPage bool) string {
	if strings.TrimSpace(blocksJSON) == "" {
		blocksJSON = "[]"
	}
	back, backLabel := "/os/posts", "Posts"
	if isPage {
		back, backLabel = "/os/pages", "Pages"
	}
	var head strings.Builder
	// Execute cannot fail for these scalar fields and a constant template.
	_ = osEditorHeadTmpl.Execute(&head, struct {
		Blocks                        json.RawMessage
		Slug, Title                   string
		BackHref, BackLabel           string
		ChevIcon, MoreIcon, PanelIcon htmpl.HTML
	}{json.RawMessage(blocksJSON), slug, title, back, backLabel,
		htmpl.HTML(saIcon("chev-r")), htmpl.HTML(saIcon("more")), htmpl.HTML(saIcon("settings"))})
	return head.String() + osEditorInspector(authorOptions) + `
  <div class="editor-preview-modal" data-editor-preview hidden role="dialog" aria-modal="true" aria-label="Preview">
    <div class="editor-preview-panel">
      <div class="editor-preview-head">
        <span>Preview</span>
        <button type="button" class="btn--icon" data-editor-preview-close aria-label="Close preview">` + saIcon("x") + `</button>
      </div>
      <article class="editor-preview-body article" data-editor-preview-body></article>
    </div>
  </div>
  <div class="editor-history-modal" data-editor-history hidden role="dialog" aria-modal="true" aria-label="Version history">
    <div class="editor-history-panel">
      <div class="editor-history-head">
        <span>Version history</span>
        <button type="button" class="btn--icon" data-editor-history-close aria-label="Close history">` + saIcon("x") + `</button>
      </div>
      <div class="editor-history-body">
        <div class="editor-history-list" data-editor-history-list></div>
        <div class="editor-history-diff" data-editor-history-diff></div>
      </div>
    </div>
  </div>
  <div class="editor-history-modal" data-editor-ai-modal hidden role="dialog" aria-modal="true" aria-label="Write with AI">
    <div class="editor-history-panel">
      <div class="editor-history-head">
        <span>` + saIcon("sparkle") + ` Write with AI</span>
        <button type="button" class="btn--icon" data-ai-close aria-label="Close">` + saIcon("x") + `</button>
      </div>
      <div class="editor-settings-body ai-panel">
        <div class="pm-field">
          <label class="pm-label" for="ai-prompt">What should this post be about?</label>
          <textarea class="pm-input" id="ai-prompt" data-ai-prompt rows="4" placeholder="e.g. A beginner's guide to self-hosting email — friendly tone, ~800 words, with a short FAQ."></textarea>
        </div>

        <!-- Shape: the controls most authors change per draft, open by default. -->
        <details class="mon-acc" open>
          <summary class="mon-acc__sum">
            <span class="mon-acc__ic" aria-hidden="true">` + saIcon("pencil") + `</span>
            <span class="mon-acc__head"><span class="mon-acc__title">Shape the draft</span><span class="mon-acc__sub">Format, tone, length and who it is for</span></span>
            <span class="mon-chip mon-chip--off" data-ai-shape-chip>○ defaults</span>
            <svg class="mon-acc__chev" viewBox="0 0 20 20" width="16" height="16" fill="none" aria-hidden="true"><path d="M6 8l4 4 4-4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
          </summary>
          <div class="mon-acc__body">
            <div class="ai-grid">
              <div class="pm-field">
                <label class="pm-label" for="ai-shape">Format</label>
                <select class="pm-input" id="ai-shape" data-ai-shape>
                  <option value="post">Full blog post</option>
                  <option value="outline">Outline only</option>
                  <option value="howto">Step-by-step how-to</option>
                  <option value="listicle">Numbered list article</option>
                  <option value="faq">Question &amp; answer</option>
                </select>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-tone">Tone</label>
                <select class="pm-input" id="ai-tone" data-ai-tone>
                  <option value="">Model default</option>
                  <option value="neutral">Neutral</option>
                  <option value="friendly">Friendly</option>
                  <option value="conversational">Conversational</option>
                  <option value="professional">Professional</option>
                  <option value="technical">Technical</option>
                  <option value="persuasive">Persuasive</option>
                </select>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-length">Length</label>
                <select class="pm-input" id="ai-length" data-ai-length>
                  <option value="">Model default</option>
                  <option value="short">Short — 300–500 words</option>
                  <option value="medium">Medium — 700–900 words</option>
                  <option value="long">Long — 1200–1600 words</option>
                  <option value="exact">Exact word count…</option>
                </select>
              </div>
              <div class="pm-field" data-ai-words-field hidden>
                <label class="pm-label" for="ai-words">Target words</label>
                <input class="pm-input" id="ai-words" type="number" min="100" max="4000" step="50" value="800" data-ai-words>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-audience">Written for <span class="muted">(optional)</span></label>
                <input class="pm-input" id="ai-audience" type="text" maxlength="80" placeholder="e.g. small-business owners, new self-hosters" data-ai-audience>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-language">Language <span class="muted">(optional)</span></label>
                <input class="pm-input" id="ai-language" type="text" maxlength="80" placeholder="Model default" data-ai-language>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-keyword">Search phrase to rank for <span class="muted">(optional)</span></label>
                <input class="pm-input" id="ai-keyword" type="text" maxlength="80" placeholder="e.g. self-hosted email server" data-ai-keyword>
                <p class="pm-help">Used naturally in the title, the opening answer and one section heading — not stuffed.</p>
              </div>
            </div>
          </div>
        </details>

        <!-- Engine: set once and rarely touched, so it starts collapsed. -->
        <details class="mon-acc">
          <summary class="mon-acc__sum">
            <span class="mon-acc__ic" aria-hidden="true">` + saIcon("settings") + `</span>
            <span class="mon-acc__head"><span class="mon-acc__title">Model &amp; provider</span><span class="mon-acc__sub" data-ai-engine-sub>Which model writes it</span></span>
            <span class="mon-chip mon-chip--off" data-ai-engine-chip>○ checking</span>
            <svg class="mon-acc__chev" viewBox="0 0 20 20" width="16" height="16" fill="none" aria-hidden="true"><path d="M6 8l4 4 4-4" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>
          </summary>
          <div class="mon-acc__body">
            <div class="ai-grid">
              <div class="pm-field">
                <label class="pm-label" for="ai-provider">Provider</label>
                <select class="pm-input" id="ai-provider" data-ai-provider></select>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-model-select">Model</label>
                <select class="pm-input" id="ai-model-select" data-ai-model-select></select>
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-model">Or type a model name</label>
                <input class="pm-input" id="ai-model" type="text" data-ai-model placeholder="Provider default">
              </div>
              <div class="pm-field">
                <label class="pm-label" for="ai-temp">Creativity <span class="muted" data-ai-temp-out>model default</span></label>
                <input class="pm-input" id="ai-temp" type="range" min="0" max="20" step="1" value="0" data-ai-temp>
                <p class="pm-help">Left is predictable and factual, right is more inventive. Leave at the far left to use the model's own setting.</p>
              </div>
            </div>
            <p class="pm-help">Providers and keys are configured in VayuOS &rarr; API Keys. Only providers you have set up appear here.</p>
          </div>
        </details>

        <p class="pm-help">Every draft is written as structured HTML — one H1, scannable sections, key
          takeaways and an FAQ — so it reads well and can be quoted by search and answer engines.</p>
        <div class="ai-status" data-ai-msg role="status" aria-live="polite">The draft is inserted as editable blocks — always review before you publish.</div>
        <div class="pm-row">
          <button type="button" class="btn btn--primary btn--sm" data-ai-run>Generate draft</button>
          <button type="button" class="btn btn--ghost btn--sm" data-ai-cancel>Cancel</button>
        </div>
      </div>
    </div>
  </div>
</div>`
}

// osEditorInspector is the post's inspector (render 02): how it is published,
// its tags, how it reads in search results and its cover, then the rest of its
// settings. It is the old settings drawer laid open beside the writing, with
// the same fields and hooks, so the editor script drives it unchanged; it folds
// away in focus mode and becomes a drawer where there is no room beside the
// text.
func osEditorInspector(authorOptions string) string {
	return `
  <div class="editor-settings-backdrop" data-editor-settings-backdrop hidden></div>
  <aside class="sa-doc__inspector" data-editor-settings aria-label="Post settings">
    <section class="sa-insp">
      <h2 class="sa-insp__head">Publish</h2>
      <div class="sa-insp__row"><span class="sa-insp__label">State</span><span class="sa-insp__value" data-editor-pubstate></span></div>
      <label class="sa-insp__row"><span class="sa-insp__label">When</span><input class="input input--quiet" id="pm-publish-date" type="datetime-local" data-pm-publish-date aria-label="Publish date"></label>
      <div class="sa-insp__row sa-insp__row--top"><label class="sa-insp__label" for="pm-slug">Address</label>
        <span class="sa-insp__value"><span class="pm-slug"><span class="pm-prefix" data-pm-slug-prefix>/</span><input class="input input--quiet" id="pm-slug" type="text" data-pm-slug placeholder="set from the title"><button type="button" class="btn btn--ghost btn--xs" data-pm-slug-apply>Change</button></span>
        <span class="sa-insp__hint" data-pm-slug-status></span></span></div>
      <label class="sa-insp__row"><span class="sa-insp__label">Author</span><select class="input input--quiet" id="pm-author" data-pm-author>` + authorOptions + `</select></label>
      <label class="sa-insp__check"><input type="checkbox" data-pm-featured> Feature this post</label>
      <label class="sa-insp__check"><input type="checkbox" data-pm-is-page> A page, not a post</label>
    </section>
    <section class="sa-insp">
      <h2 class="sa-insp__head">Tags</h2>
      <div class="pm-tags" data-pm-tags-list></div>
      <input class="input input--quiet" type="text" data-pm-tags-input placeholder="Add a tag" aria-label="Add a tag">
    </section>
    <section class="sa-insp">
      <h2 class="sa-insp__head">In search results</h2>
      <div class="seo-snippet" data-seo-snippet aria-label="How the post reads in a search result">
        <div class="seo-snippet__url" data-seo-snippet-url></div>
        <div class="seo-snippet__title" data-seo-snippet-title></div>
        <div class="seo-snippet__desc" data-seo-snippet-desc></div>
      </div>
      <label class="sa-insp__field"><span class="sa-insp__label">Title for search <span class="sa-insp__count"><span data-pm-meta-title-count>0</span>/120</span></span>
        <input class="input" id="pm-meta-title" type="text" maxlength="120" data-pm-meta-title placeholder="The post's title"></label>
      <label class="sa-insp__field"><span class="sa-insp__label">Description <span class="sa-insp__count"><span data-pm-meta-description-count>0</span>/300</span></span>
        <textarea class="textarea" id="pm-meta-description" rows="3" maxlength="300" data-pm-meta-description placeholder="The excerpt"></textarea></label>
    </section>
    <section class="sa-insp">
      <h2 class="sa-insp__head">Cover image</h2>
      <button type="button" class="pm-feature" data-pm-feature-upload aria-label="Upload a cover image">
        <img class="pm-feature-preview" data-pm-feature-preview alt="" hidden>
        <span class="pm-feature-empty" data-pm-feature-empty>` + saIcon("upload") + `</span>
      </button>
      <span class="pm-slug"><input class="input input--quiet" type="text" data-pm-feature-image placeholder="or an image address" aria-label="Cover image address"><button type="button" class="btn btn--ghost btn--xs" data-pm-feature-remove>Remove</button></span>
      <input type="file" accept="image/*" data-pm-feature-file hidden>
    </section>
    <section class="sa-insp">
      <h2 class="sa-insp__head">Excerpt <span class="sa-insp__count"><span data-pm-excerpt-count>0</span>/300</span></h2>
      <textarea class="textarea" id="pm-excerpt" rows="3" maxlength="300" data-pm-excerpt placeholder="The first lines of the post" aria-label="Excerpt"></textarea>
    </section>
    <nav class="sa-insp" data-editor-outline-wrap aria-label="Outline" hidden>
      <h2 class="sa-insp__head">Outline</h2>
      <div class="editor-outline__list" data-editor-outline></div>
    </nav>
    <details class="sa-insp sa-insp--more">
      <summary class="sa-insp__head">Original address and sharing cards</summary>
      <label class="sa-insp__field"><span class="sa-insp__label">Original address</span><input class="input" id="pm-canonical" type="url" data-pm-canonical placeholder="Where it was first published"></label>
      <label class="sa-insp__field"><span class="sa-insp__label">Card title</span><input class="input" id="pm-og-title" type="text" data-pm-og-title placeholder="The title for search"></label>
      <label class="sa-insp__field"><span class="sa-insp__label">Card description</span><textarea class="textarea" id="pm-og-description" rows="2" data-pm-og-description placeholder="The description"></textarea></label>
      <label class="sa-insp__field"><span class="sa-insp__label">Card image</span><input class="input" id="pm-og-image" type="text" data-pm-og-image placeholder="The cover image"></label>
      <label class="sa-insp__field"><span class="sa-insp__label">X title</span><input class="input" id="pm-twitter-title" type="text" data-pm-twitter-title placeholder="The card title"></label>
      <label class="sa-insp__field"><span class="sa-insp__label">X description</span><textarea class="textarea" id="pm-twitter-description" rows="2" data-pm-twitter-description placeholder="The card description"></textarea></label>
      <label class="sa-insp__field"><span class="sa-insp__label">X image</span><input class="input" id="pm-twitter-image" type="text" data-pm-twitter-image placeholder="The card image"></label>
    </details>
    ` + string(ui.Explain(ui.HTML(`
      <p>Press <kbd>/</kbd> on an empty block for blocks, or <kbd>⌘K</kbd> anywhere. Select text to format it, or type Markdown: <kbd>## </kbd> heading, <kbd>- </kbd> list, <kbd>&gt; </kbd> quote, <kbd>&#96;&#96;&#96;</kbd> code.</p>
      <p>Drag or paste an image to upload it. Reorder blocks by their grip, or with Move up and Move down. <kbd>Enter</kbd> starts a block, <kbd>Shift+Enter</kbd> breaks a line, <kbd>⌘S</kbd> saves.</p>
    `))) + `
  </aside>
  </div>`
}
