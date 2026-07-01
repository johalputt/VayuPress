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
	htmpl "html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/johalputt/vayupress/internal/blockrender"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
)

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
	_ = dbpkg.DB.QueryRowContext(ctx,
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

	contentHTML, _, err := blockrender.Render(blocksJSON)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "render-error", "Could not render blocks: "+err.Error(), "")
		return
	}

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
		if _, err := a.articles.Create(r.Context(), title, slug, seed, tags); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "create-error", err.Error(), "")
			return
		}
		if err := persistBlocksJSON(r.Context(), slug, blocksJSON); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "persist-error", err.Error(), "")
			return
		}
		a.applyPostExtras(r.Context(), slug, body.Meta, body.PublishDate, tags, currentUserIDOf(r))
		writeJSON(w, r, http.StatusOK, map[string]string{"status": "created", "slug": slug})
		return
	}

	// ── Update path (existing slug) ──────────────────────────────────────────
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
	var tagsCSV string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&tagsCSV); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No article with that slug", "")
		return
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET status=?, updated_at=? WHERE slug=?`, status, time.Now().UTC(), slug); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "update-error", err.Error(), "")
		return
	}
	// Purge public caches (article page, homepage, tag pages, sitemap, feed) so
	// an unpublish disappears — and a publish appears — without delay.
	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	// Publishing a (previously draft) post makes its URL public for the first
	// time — announce it to IndexNow so search engines crawl it promptly. The
	// status-toggle path emits no ArticleUpdated event, so without this a newly
	// published post would never be submitted. pingIndexNow re-checks that the
	// post is published, so unpublishing never pings.
	if status == "published" {
		go a.pingIndexNow(slug)
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": status, "slug": slug})
}

// handleOSPostPin pins or unpins (features) a post directly from the manager,
// flipping the same `featured` flag the editor exposes as "Feature this post".
// Pinned posts surface in the public Trending & pinned widget (homepage + under
// every post), so we drop the trending cache and purge public caches so the
// change appears immediately.
func (a *App) handleOSPostPin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug   string `json:"slug"`
		Pinned bool   `json:"pinned"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "" {
		writeAPIError(w, r, http.StatusBadRequest, "bad-input", "a slug is required", "")
		return
	}
	var tagsCSV string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&tagsCSV); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No article with that slug", "")
		return
	}
	featured := 0
	if body.Pinned {
		featured = 1
	}
	if _, err := dbpkg.WDB.Exec(`UPDATE articles SET featured=?, updated_at=? WHERE slug=?`, featured, time.Now().UTC(), slug); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "update-error", err.Error(), "")
		return
	}
	// Refresh the public surfaces and the memoised trending/pinned payload.
	invalidateTrendingCache()
	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	writeJSON(w, r, http.StatusOK, map[string]bool{"pinned": body.Pinned})
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
	var id, tagsCSV string
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT id,COALESCE(tags,'') FROM articles WHERE slug=?`, slug).Scan(&id, &tagsCSV); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No post with that slug", "")
		return
	}
	if _, err := dbpkg.WDB.Exec(`DELETE FROM articles WHERE slug=?`, slug); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "delete-error", err.Error(), "")
		return
	}
	// Best-effort cleanup of the post's comments (orphans otherwise).
	_, _ = dbpkg.WDB.Exec(`DELETE FROM comments WHERE article_id=?`, id)

	render.CachePurge(slug, splitCSVTags(tagsCSV), generateSitemap, generateRSS, generateRobots)
	dbpkg.AuditLog("article.delete", dbpkg.AuditActor(r), slug, "id="+id)
	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "editor", Severity: "info",
		Msg: "post deleted: " + slug, RequestID: getRequestID(r),
	})
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "deleted", "slug": slug})
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

// handleOSEditorConvert imports a legacy article's HTML into a block document
// (ADR-0069 Stage 1). It is deliberately non-destructive: it writes only the
// blocks_json side-car and never touches the rendered article content. The
// operator reviews the imported blocks in the editor and the original content
// stays authoritative until they explicitly Save. This keeps legacy posts
// lossless — a poor import can be abandoned by navigating away.
func (a *App) handleOSEditorConvert(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	slug := strings.TrimSpace(body.Slug)
	if slug == "" {
		writeAPIError(w, r, http.StatusBadRequest, "missing-slug", "slug is required", "")
		return
	}

	art, err := a.articles.Get(r.Context(), slug)
	if err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "No article with that slug", "")
		return
	}

	blocks := blockrender.ImportHTML(art.Content)
	raw, err := json.Marshal(blocks)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "marshal-error", err.Error(), "")
		return
	}
	if err := persistBlocksJSON(r.Context(), slug, string(raw)); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "persist-error", err.Error(), "")
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]interface{}{
		"status": "converted",
		"slug":   slug,
		"blocks": len(blocks),
	})
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
	if err := dbpkg.DB.QueryRowContext(r.Context(), `SELECT id FROM articles WHERE slug=?`, slug).Scan(&articleID); err != nil {
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
// The static remainder of the shell carries no interpolation and is appended
// as a literal.
var osEditorHeadTmpl = htmpl.Must(htmpl.New("oseditorhead").Parse(
	`<script type="application/json" id="vp-editor-data">{{.Blocks}}</script>
<div class="editor-shell" data-editor data-slug="{{.Slug}}">
  <div class="editor-topbar">
    <span class="editor-topbar-status" data-editor-topbar-status></span>
    <div class="editor-topbar-actions">
      <span class="editor-wordcount" data-editor-wordcount aria-live="polite"></span>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-focus-btn title="Focus mode (Ctrl/Cmd+.)">Focus</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-split-btn title="Toggle live preview">Split</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-html-btn title="Edit HTML source (Ctrl/Cmd+Shift+H)" aria-pressed="false">HTML</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-preview-btn>Preview</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-settings-btn title="Post settings (Ctrl/Cmd+Shift+P)" aria-pressed="false">⚙ Settings</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-newpage title="Create a new standalone page">＋ Page</button>
      <button type="button" class="btn btn--primary btn--sm" data-editor-save>Save</button>
    </div>
  </div>
  <div class="editor-main">
    <input class="editor-title" data-editor-title type="text" placeholder="Post title…" value="{{.Title}}" aria-label="Post title">
    <div class="editor-workspace">
      <div class="editor-canvas" data-editor-canvas aria-label="Editor canvas"></div>
      <aside class="editor-live" data-editor-live hidden aria-label="Live preview">
        <div class="editor-live-head">Live preview</div>
        <article class="editor-live-body article" data-editor-live-body></article>
      </aside>
      <section class="editor-html" data-editor-html-panel hidden aria-label="HTML source editor">
        <div class="editor-html-head">
          <span class="editor-html-title">HTML source</span>
          <span class="editor-html-hint text-xs muted">Edit raw HTML — switch back to apply it to your blocks.</span>
        </div>
        <textarea class="editor-html-area" data-editor-html-area spellcheck="false" autocomplete="off" autocapitalize="off" wrap="soft" aria-label="HTML source"></textarea>
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
		pub = createdAt.UTC().Format("2006-01-02T15:04")
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

func osEditorBody(slug, title, blocksJSON string) string {
	if strings.TrimSpace(blocksJSON) == "" {
		blocksJSON = "[]"
	}
	var head strings.Builder
	// Execute cannot fail for these scalar fields and a constant template.
	_ = osEditorHeadTmpl.Execute(&head, struct {
		Blocks json.RawMessage
		Slug   string
		Title  string
	}{json.RawMessage(blocksJSON), slug, title})
	return head.String() + `
  <aside class="editor-sidebar" aria-label="Editor tools">
    <div class="editor-status" data-editor-status>Ready</div>
    <div class="editor-stats" aria-label="Document statistics">
      <div class="editor-stat"><span class="editor-stat__num" data-editor-stats-words>0</span><span class="editor-stat__label">words</span></div>
      <div class="editor-stat"><span class="editor-stat__num" data-editor-stats-chars>0</span><span class="editor-stat__label">characters</span></div>
      <div class="editor-stat"><span class="editor-stat__num" data-editor-stats-read>—</span><span class="editor-stat__label">reading</span></div>
    </div>
    <div class="editor-actions">
      <button type="button" class="btn btn--ghost btn--sm" data-editor-undo title="Undo (Ctrl/Cmd+Z outside a field)" disabled>Undo</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-redo title="Redo (Ctrl/Cmd+Shift+Z)" disabled>Redo</button>
      <button type="button" class="btn btn--ghost btn--sm" data-editor-history-btn>History</button>
    </div>
    <div class="editor-hint text-xs muted">Press <kbd>/</kbd> on an empty block for commands, or <kbd>⌘K</kbd>/<kbd>Ctrl+K</kbd> anywhere. <kbd>/ai</kbd> for AI assist.</div>
    <div class="editor-hint text-xs muted mt-2">Type Markdown to format: <kbd>## </kbd> heading, <kbd>- </kbd> list, <kbd>- [ ] </kbd> task, <kbd>1. </kbd> numbered, <kbd>&gt; </kbd> quote, <kbd>&#96;&#96;&#96;</kbd> code, <kbd>---</kbd> divider.</div>
    <div class="editor-hint text-xs muted mt-2">Select text for <strong>bold</strong>/<em>italic</em>/link, or use <kbd>**bold**</kbd>, <kbd>*italic*</kbd>, <kbd>[text](url)</kbd>. Drag or paste an image to upload.</div>
    <div class="editor-hint text-xs muted mt-2">Reorder blocks by dragging <kbd>⋮⋮</kbd> or with the <kbd>↑</kbd>/<kbd>↓</kbd> buttons. <kbd>⌘.</kbd> toggles focus mode.</div>
    <div class="editor-hint text-xs muted mt-2"><kbd>Enter</kbd> new block · <kbd>Shift+Enter</kbd> line break · <kbd>⌘S</kbd> / <kbd>Ctrl+S</kbd> to save.</div>
    <div class="editor-hint text-xs muted mt-2"><kbd>HTML</kbd> in the toolbar (<kbd>⌘⇧H</kbd>) switches to a raw HTML source editor and back — formatting is preserved both ways.</div>
    <div class="editor-hint text-xs muted mt-2"><kbd>⚙ Settings</kbd> (<kbd>⌘⇧P</kbd>) opens post settings: feature image, URL, publish date, excerpt, tags, SEO &amp; social cards.</div>
  </aside>
  <div class="editor-preview-modal" data-editor-preview hidden role="dialog" aria-modal="true" aria-label="Preview">
    <div class="editor-preview-panel">
      <div class="editor-preview-head">
        <span>Preview</span>
        <button type="button" class="btn--icon" data-editor-preview-close aria-label="Close preview">✕</button>
      </div>
      <article class="editor-preview-body article" data-editor-preview-body></article>
    </div>
  </div>
  <div class="editor-history-modal" data-editor-history hidden role="dialog" aria-modal="true" aria-label="Version history">
    <div class="editor-history-panel">
      <div class="editor-history-head">
        <span>Version history</span>
        <button type="button" class="btn--icon" data-editor-history-close aria-label="Close history">✕</button>
      </div>
      <div class="editor-history-body">
        <div class="editor-history-list" data-editor-history-list></div>
        <div class="editor-history-diff" data-editor-history-diff></div>
      </div>
    </div>
  </div>
  <div class="editor-settings-backdrop" data-editor-settings-backdrop hidden></div>
  <aside class="editor-settings" data-editor-settings hidden role="dialog" aria-modal="true" aria-label="Post settings">
    <div class="editor-settings-head">
      <span class="editor-settings-title">Post settings</span>
      <button type="button" class="btn--icon" data-editor-settings-close aria-label="Close post settings">✕</button>
    </div>
    <div class="editor-settings-body">
      <div class="pm-field">
        <label class="pm-label">Feature image</label>
        <div class="pm-feature" data-pm-feature>
          <img class="pm-feature-preview" data-pm-feature-preview alt="" hidden>
          <div class="pm-feature-empty" data-pm-feature-empty>No feature image</div>
        </div>
        <div class="pm-row">
          <input class="pm-input" type="text" data-pm-feature-image placeholder="Image URL or upload…">
          <button type="button" class="btn btn--ghost btn--xs" data-pm-feature-upload>Upload</button>
          <button type="button" class="btn btn--ghost btn--xs" data-pm-feature-remove>Remove</button>
        </div>
        <input type="file" accept="image/*" data-pm-feature-file hidden>
      </div>

      <div class="pm-field">
        <label class="pm-label" for="pm-slug">Post URL</label>
        <div class="pm-row">
          <span class="pm-prefix" data-pm-slug-prefix>/</span>
          <input class="pm-input" id="pm-slug" type="text" data-pm-slug placeholder="post-url-slug">
          <button type="button" class="btn btn--ghost btn--xs" data-pm-slug-apply>Update</button>
        </div>
        <div class="pm-hint text-xs muted" data-pm-slug-status>The slug is set automatically from the title on first save.</div>
      </div>

      <div class="pm-field">
        <label class="pm-label" for="pm-publish-date">Publish date</label>
        <input class="pm-input" id="pm-publish-date" type="datetime-local" data-pm-publish-date>
      </div>

      <div class="pm-field">
        <label class="pm-label" for="pm-excerpt">Excerpt</label>
        <textarea class="pm-input pm-textarea" id="pm-excerpt" rows="3" maxlength="300" data-pm-excerpt placeholder="A short summary used on cards, feeds, and search results…"></textarea>
        <div class="pm-hint text-xs muted"><span data-pm-excerpt-count>0</span>/300 · falls back to the first lines of the post when left blank.</div>
      </div>

      <div class="pm-field">
        <label class="pm-label">Tags</label>
        <div class="pm-tags" data-pm-tags-list></div>
        <input class="pm-input" type="text" data-pm-tags-input placeholder="Type a tag and press Enter…">
      </div>

      <div class="pm-field pm-toggles">
        <label class="pm-check"><input type="checkbox" data-pm-featured> <span>Feature this post</span></label>
        <label class="pm-check"><input type="checkbox" data-pm-is-page> <span>Turn this post into a page</span></label>
        <div class="pm-hint text-xs muted">Pages are standalone (no date, tags, or author) and are kept out of the home feed, RSS, and sitemap.</div>
      </div>

      <details class="pm-group" open>
        <summary class="pm-group-title">Meta data &amp; SEO</summary>
        <div class="pm-field">
          <label class="pm-label" for="pm-meta-title">SEO title</label>
          <input class="pm-input" id="pm-meta-title" type="text" maxlength="120" data-pm-meta-title placeholder="Custom title for search engines…">
          <div class="pm-hint text-xs muted"><span data-pm-meta-title-count>0</span>/120 · defaults to the post title.</div>
        </div>
        <div class="pm-field">
          <label class="pm-label" for="pm-meta-description">SEO description</label>
          <textarea class="pm-input pm-textarea" id="pm-meta-description" rows="3" maxlength="300" data-pm-meta-description placeholder="Custom description for search engines…"></textarea>
          <div class="pm-hint text-xs muted"><span data-pm-meta-description-count>0</span>/300 · defaults to the excerpt.</div>
        </div>
        <div class="pm-field">
          <label class="pm-label">Search preview</label>
          <div class="seo-snippet" data-seo-snippet aria-label="Google search result preview">
            <div class="seo-snippet__url" data-seo-snippet-url></div>
            <div class="seo-snippet__title" data-seo-snippet-title></div>
            <div class="seo-snippet__desc" data-seo-snippet-desc></div>
          </div>
          <div class="pm-hint text-xs muted">Approximate Google result. Titles over ~60 and descriptions over ~160 characters may be truncated.</div>
        </div>
        <div class="pm-field">
          <label class="pm-label" for="pm-canonical">Canonical URL</label>
          <input class="pm-input" id="pm-canonical" type="url" data-pm-canonical placeholder="https://example.com/original-post">
          <div class="pm-hint text-xs muted">Set when this content was first published elsewhere.</div>
        </div>
      </details>

      <details class="pm-group">
        <summary class="pm-group-title">Social sharing cards</summary>
        <div class="pm-subhead">Facebook / Open Graph</div>
        <div class="pm-field"><label class="pm-label" for="pm-og-title">Title</label><input class="pm-input" id="pm-og-title" type="text" data-pm-og-title placeholder="Defaults to the SEO title…"></div>
        <div class="pm-field"><label class="pm-label" for="pm-og-description">Description</label><textarea class="pm-input pm-textarea" id="pm-og-description" rows="2" data-pm-og-description placeholder="Defaults to the SEO description…"></textarea></div>
        <div class="pm-field"><label class="pm-label" for="pm-og-image">Image URL</label><input class="pm-input" id="pm-og-image" type="text" data-pm-og-image placeholder="Defaults to the feature image…"></div>
        <div class="pm-subhead">Twitter / X</div>
        <div class="pm-field"><label class="pm-label" for="pm-twitter-title">Title</label><input class="pm-input" id="pm-twitter-title" type="text" data-pm-twitter-title placeholder="Defaults to the Open Graph title…"></div>
        <div class="pm-field"><label class="pm-label" for="pm-twitter-description">Description</label><textarea class="pm-input pm-textarea" id="pm-twitter-description" rows="2" data-pm-twitter-description placeholder="Defaults to the Open Graph description…"></textarea></div>
        <div class="pm-field"><label class="pm-label" for="pm-twitter-image">Image URL</label><input class="pm-input" id="pm-twitter-image" type="text" data-pm-twitter-image placeholder="Defaults to the Open Graph image…"></div>
      </details>
    </div>
  </aside>
</div>`
}
