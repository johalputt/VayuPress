// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_media.go — VayuOS media library (ADR-0068, Phase 4).
//
// The upload + storage backend already exists (handlers_media.go): content-
// addressed files under config.Cfg.MediaDir, served same-origin from /media/{file},
// with a strict type allowlist validated by magic number. This file adds the os
// browsing surface: a grid page and a JSON listing endpoint. Listing only ever
// exposes server-generated names (validated by storedMediaName), so there is no
// path-traversal or info-leak vector.

import (
	"context"
	"encoding/json"
	htmpl "html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/settings"
	"github.com/johalputt/vayupress/internal/ui"
)

// mediaItem is one stored asset as surfaced to the library UI. Name is the
// stored, content-addressed file name; Title is what a person calls it.
type mediaItem struct {
	Name    string     `json:"name"`
	Title   string     `json:"title"`
	Kind    string     `json:"kind"`
	URL     string     `json:"url"`
	Size    int64      `json:"size"`
	ModUnix int64      `json:"mod"`
	IsPDF   bool       `json:"isPdf"`
	Alt     string     `json:"alt"`
	Uses    []mediaUse `json:"uses"`
}

// mediaUse is one place a file is referenced from, with where to go to see it.
type mediaUse struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// mediaKinds names each stored extension the way a person reads it.
var mediaKinds = map[string]string{
	".jpg": "JPEG image", ".png": "PNG image", ".gif": "GIF image",
	".webp": "WebP image", ".svg": "SVG drawing", ".pdf": "PDF document",
}

// mediaNameMap returns the persisted filename→display-name map (best-effort).
func (a *App) mediaNameMap(ctx context.Context) map[string]string {
	out := map[string]string{}
	if a.siteSettings == nil {
		return out
	}
	if raw := a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyMediaNames); strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

func (a *App) saveMediaNames(ctx context.Context, names map[string]string) error {
	b, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return a.siteSettings.SetMany(ctx, settings.ForPrimary(), map[string]string{settings.KeyMediaNames: string(b)})
}

// cleanMediaTitle is a display name as it may be stored: the last path
// element of whatever the browser sent, trimmed, and bounded.
func cleanMediaTitle(s string) string {
	s = strings.TrimSpace(filepath.Base(strings.ReplaceAll(s, "\\", "/")))
	if s == "." || s == "/" {
		return ""
	}
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120])
	}
	return s
}

// nameMediaOnce records the name a file arrived with, unless it already has
// one: the same bytes uploaded again are the same file, and keep the name
// they were first given or renamed to.
func (a *App) nameMediaOnce(ctx context.Context, stored, original string) {
	title := cleanMediaTitle(original)
	if a.siteSettings == nil || title == "" || !storedMediaName.MatchString(stored) {
		return
	}
	names := a.mediaNameMap(ctx)
	if _, ok := names[stored]; ok {
		return
	}
	names[stored] = title
	_ = a.saveMediaNames(ctx, names)
}

// mediaRefRe finds a stored media file referenced from any text: a post's
// body or blocks, a website document, a setting.
var mediaRefRe = regexp.MustCompile(`/media/([a-f0-9]{32}\.(?:png|jpg|gif|webp|pdf|svg))`)

// mediaUsage maps each stored file to the places that reference it: posts and
// pages (body, blocks, feature and share images), each site's live website,
// and settings such as a logo. It is read on demand, not kept: a list that
// had to be maintained at every save would drift from the content it claims
// to describe.
func (a *App) mediaUsage(ctx context.Context) map[string][]mediaUse {
	uses := map[string][]mediaUse{}
	add := func(text string, use mediaUse) {
		seen := map[string]bool{}
		for _, m := range mediaRefRe.FindAllStringSubmatch(text, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				uses[m[1]] = append(uses[m[1]], use)
			}
		}
	}
	if dbpkg.DB == nil {
		return uses
	}
	rdb := dbpkg.Reader()
	if rows, err := rdb.QueryContext(ctx, `SELECT slug, title, is_page, COALESCE(content,'') || ' ' || COALESCE(blocks_json,'') || ' ' || COALESCE(feature_image,'') || ' ' || COALESCE(og_image,'') || ' ' || COALESCE(twitter_image,'') FROM articles ORDER BY is_page, title`); err == nil {
		for rows.Next() {
			var slug, title, text string
			var isPage bool
			if rows.Scan(&slug, &title, &isPage, &text) == nil {
				label := "Post · " + title
				if isPage {
					label = "Page · " + title
				}
				add(text, mediaUse{Label: label, Href: "/os/editor/" + slug})
			}
		}
		_ = rows.Err() // a read cut short just shows fewer uses
		rows.Close()
	}
	if rows, err := rdb.QueryContext(ctx, `SELECT r.domain_id, r.doc FROM site_revisions r JOIN (SELECT domain_id, MAX(id) AS id FROM site_revisions GROUP BY domain_id) n ON n.id = r.id ORDER BY r.domain_id`); err == nil {
		for rows.Next() {
			var domainID, doc string
			if rows.Scan(&domainID, &doc) == nil {
				use := mediaUse{Label: "Website", Href: "/os/website/editor"}
				if domainID != "" {
					use = mediaUse{Label: "Website · a hosted site", Href: "/os/d/" + domainID + "/website"}
				}
				add(doc, use)
			}
		}
		_ = rows.Err()
		rows.Close()
	}
	if rows, err := rdb.QueryContext(ctx, `SELECT value FROM site_settings WHERE value LIKE '%/media/%'`); err == nil {
		for rows.Next() {
			var value string
			if rows.Scan(&value) == nil {
				add(value, mediaUse{Label: "Site settings", Href: "/os/settings/design"})
			}
		}
		_ = rows.Err()
		rows.Close()
	}
	return uses
}

// mediaAltMap returns the persisted filename→alt-text map (best-effort).
func (a *App) mediaAltMap(ctx context.Context) map[string]string {
	out := map[string]string{}
	if a.siteSettings == nil {
		return out
	}
	raw := a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyMediaAlt)
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// countMediaItems returns just how many media files there are, without stat-ing
// each one or building and sorting a slice. The dashboard and the Media page
// header only need the count, and listMediaItems costs a stat syscall per file
// plus an allocation per file plus a sort — work that grows with the library and
// is paid on every render of the most-visited page in the console.
func countMediaItems() int {
	entries, err := os.ReadDir(config.Cfg.MediaDir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		// IsDir and Name come from the directory entry itself (no stat), and the
		// name check is the same allowlist listMediaItems applies.
		if e.IsDir() || !storedMediaName.MatchString(e.Name()) {
			continue
		}
		n++
	}
	return n
}

// listMediaItems reads MediaDir and returns the stored assets newest-first. Only
// names matching storedMediaName (the content-addressed pattern this server
// itself produces) are included, so stray or hostile filenames are ignored.
//
// storedMediaName, not safeMediaName: the narrower serve allowlist left every
// uploaded SVG invisible here while the quota went on charging for it, so a
// library could fill with files this page would not show and the delete
// endpoint would not touch. The page has to account for everything the ceiling
// counts, or a full library has no remedy the operator can reach.
func listMediaItems() []mediaItem {
	items := []mediaItem{}
	entries, err := os.ReadDir(config.Cfg.MediaDir)
	if err != nil {
		return items
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !storedMediaName.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, mediaItem{
			Name:    name,
			Kind:    mediaKinds[filepath.Ext(name)],
			URL:     "/media/" + name,
			Size:    info.Size(),
			ModUnix: info.ModTime().Unix(),
			IsPDF:   strings.HasSuffix(name, ".pdf"),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModUnix > items[j].ModUnix })
	return items
}

// handleOSMediaList returns the media library contents as JSON, merging each
// asset's alt text, its name and where it is used.
func (a *App) handleOSMediaList(w http.ResponseWriter, r *http.Request) {
	items := listMediaItems()
	alts, names, uses := a.mediaAltMap(r.Context()), a.mediaNameMap(r.Context()), a.mediaUsage(r.Context())
	for i := range items {
		items[i].Alt = alts[items[i].Name]
		items[i].Title = names[items[i].Name]
		if items[i].Title == "" {
			items[i].Title = items[i].Name
		}
		items[i].Uses = uses[items[i].Name]
		if items[i].Uses == nil {
			items[i].Uses = []mediaUse{}
		}
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"items": items})
}

// handleOSMediaDelete removes one or more content-addressed media files. Names
// are validated against storedMediaName (so only server-generated assets can be
// targeted — no path traversal), their alt entries are pruned, and the count of
// successful deletions is returned.
//
// It has to accept every name the store path can create, not just the ones
// /media will serve. Deleting is the only way an operator gets back under the
// quota, and a file that is charged but undeletable turns a full library into a
// state the panel cannot leave.
func (a *App) handleOSMediaDelete(w http.ResponseWriter, r *http.Request) {
	if cur := mode.Global.Current(); cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		writeAPIError(w, r, http.StatusServiceUnavailable, "read-only", "media cannot be deleted in "+string(cur)+" mode", "")
		return
	}
	var body struct {
		Names []string `json:"names"`
	}
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	alts, names := a.mediaAltMap(r.Context()), a.mediaNameMap(r.Context())
	deleted := 0
	for _, n := range body.Names {
		if !storedMediaName.MatchString(n) {
			continue // ignore anything not a server-generated asset name
		}
		if err := os.Remove(filepath.Join(config.Cfg.MediaDir, n)); err == nil {
			deleted++
			delete(alts, n)
			delete(names, n)
		}
	}
	if deleted > 0 {
		// The quota's usage figure is cached, so without this the freed space stays
		// invisible for up to a TTL — an operator who deletes to make room and is
		// refused the very next upload has been told the delete did not work.
		invalidateMediaUsage()
	}
	if a.siteSettings != nil {
		if b, err := json.Marshal(alts); err == nil {
			_ = a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyMediaAlt: string(b)})
		}
		_ = a.saveMediaNames(r.Context(), names)
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"deleted": deleted})
}

// handleOSMediaName renames a file as the library shows it. The stored file,
// and every link to it, is unchanged: the name is how a person finds it.
func (a *App) handleOSMediaName(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	var body struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	if !storedMediaName.MatchString(body.Name) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-name", "Unknown media asset", "")
		return
	}
	title := cleanMediaTitle(body.Title)
	if title == "" {
		writeAPIError(w, r, http.StatusBadRequest, "empty-name", "A file needs a name", "")
		return
	}
	names := a.mediaNameMap(r.Context())
	names[body.Name] = title
	if err := a.saveMediaNames(r.Context(), names); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"title": title})
}

// handleOSMediaAlt sets (or clears) the alt text for one media asset.
func (a *App) handleOSMediaAlt(w http.ResponseWriter, r *http.Request) {
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "settings-error", "settings not initialised", "")
		return
	}
	var body struct {
		Name string `json:"name"`
		Alt  string `json:"alt"`
	}
	if err := readJSONDirect(r, &body); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-json", "Invalid request body", "")
		return
	}
	// Same allowlist the listing uses: an asset the page shows has to be one the
	// page can annotate, or the operator gets "Unknown media asset" for a file
	// sitting in front of them.
	if !storedMediaName.MatchString(body.Name) {
		writeAPIError(w, r, http.StatusBadRequest, "bad-name", "Unknown media asset", "")
		return
	}
	alt := strings.TrimSpace(body.Alt)
	if len(alt) > 300 {
		alt = alt[:300]
	}
	alts := a.mediaAltMap(r.Context())
	if alt == "" {
		delete(alts, body.Name)
	} else {
		alts[body.Name] = alt
	}
	b, err := json.Marshal(alts)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "encode-error", err.Error(), "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), settings.ForPrimary(), map[string]string{settings.KeyMediaAlt: string(b)}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "save-error", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handleOSMedia renders the media library (render 07): a table or grid of
// every file, an inspector for the selected one, a context menu on the
// selection, and uploads with their progress and time left. The list itself
// is drawn by admin-os.js from /os/api/media.
func (a *App) handleOSMedia(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())

	count := countMediaItems()
	seg := func(view, icon, label string, on bool) string {
		return `<button type="button" class="sa-seg__opt" data-media-view="` + view + `" aria-pressed="` + strconv.FormatBool(on) + `" aria-label="` + label + `">` + saIcon(icon) + `</button>`
	}
	kind := func(k, label string, on bool) string {
		cls := "seg-btn"
		if on {
			cls += " is-active"
		}
		return `<button type="button" class="` + cls + `" data-media-filter="` + k + `">` + label + `</button>`
	}
	body := `<div class="page-header">
  <h1>Media</h1>
  <div class="page-actions">
    <label class="media-find">` + saIcon("search") + `<input type="search" class="input" data-media-search placeholder="Filter media" aria-label="Filter media" autocomplete="off"></label>
    <span class="sa-seg" role="group" aria-label="Show as">` + seg("list", "list", "Show as a list", true) + seg("grid", "grid", "Show as a grid", false) + `</span>
    <button type="button" class="btn btn--primary" data-media-upload>` + saIcon("upload") + ` Upload</button>
    <input type="file" data-media-input multiple accept="image/png,image/jpeg,image/gif,image/webp,image/svg+xml,application/pdf" hidden>
  </div>
</div>
<p class="page-sub">` + strconv.Itoa(count) + ` file` + plural(count) + `, served from your own address. Drop files anywhere on this page to upload them.` +
		string(ui.Tip("PNG, JPEG, GIF, WebP, SVG and PDF, up to 32 MB each. An SVG is cleaned on upload: scripts, styles and references to other sites are removed before it is stored.")) + `</p>
<div class="seg-filter media-kinds" role="group" aria-label="Kind">` + kind("all", "All", true) + kind("image", "Images", false) + kind("pdf", "Documents", false) + `</div>
<div class="media-shell">
  <div class="media-main" data-media-drop>
    <div data-media-list aria-live="polite"><div class="skeleton skeleton--media"></div></div>
    <p class="table-empty" data-media-empty hidden>No file matches that.</p>
  </div>
  <aside class="media-inspector" data-media-inspector aria-label="Details"><p class="table-empty">Select a file to see its details.</p></aside>
</div>
<div class="sa-pop__panel sa-menu media-menu" data-media-menu role="menu" hidden></div>
<div class="media-uploads" data-media-uploads role="status" hidden></div>`

	writeOSHTML(w, r, adminOSLayout(nonce, "Media", "media", cfg, htmpl.HTML(body)))
}
