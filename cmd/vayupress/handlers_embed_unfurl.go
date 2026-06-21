package main

// handlers_embed_unfurl.go — POST /api/v1/admin/embed/unfurl (ADR-0070, Phase 1 Slice 2).
//
// Fetches a URL, parses OpenGraph metadata, downloads the thumbnail image,
// stores the result in embed_cache, and returns a resolved embed block payload.
// This is called by the editor when the operator pastes a URL into an embed block.
//
// Security posture:
//   - Protected by API key + CSRF (see routes.go).
//   - Mode-gated: refused in read-only / quarantined mode.
//   - HTML fetch uses safefetch (SSRF-safe) with a 1 MB cap.
//   - Thumbnail is downloaded via remoteImageFetcher and validated by magic number
//     through storeValidatedMedia — the same path as regular media imports.
//   - All text fields are stored raw and HTML-escaped at render time (blockrender).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/mode"
	"github.com/johalputt/vayupress/internal/safefetch"
)

// maxUnfurlBytes caps the HTML body read when scraping OG tags (1 MB).
const maxUnfurlBytes = 1 * 1024 * 1024

// htmlFetcher is the SSRF-safe client used to fetch the target page HTML.
var htmlFetcher = safefetch.New(safefetch.Options{
	MaxBytes:       maxUnfurlBytes,
	Timeout:        10 * time.Second,
	AllowedSchemes: []string{"https", "http"},
})

// ogTagRe matches <meta property="og:*" content="…"> in either attribute order.
var ogTagRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:([^"']+)["'][^>]+content=["']([^"']*)["'][^>]*>|<meta[^>]+content=["']([^"']*)["'][^>]+property=["']og:([^"']+)["'][^>]*>`)

// unfurlResponse is the JSON response from the unfurl endpoint.
type unfurlResponse struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Provider    string `json:"provider"`
	ThumbURL    string `json:"thumbURL"`
}

// handleEmbedUnfurl implements POST /api/v1/admin/embed/unfurl.
func (a *App) handleEmbedUnfurl(w http.ResponseWriter, r *http.Request) {
	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
	}

	cur := mode.Global.Current()
	if cur == mode.ModeReadOnly || cur == mode.ModeQuarantined {
		fail(503, "embed unfurl is unavailable in "+string(cur)+" mode")
		return
	}

	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8*1024)).Decode(&req); err != nil {
		fail(400, "invalid JSON body")
		return
	}
	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		fail(400, "missing 'url'")
		return
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		fail(400, "url must be a valid http or https URL")
		return
	}

	// Check cache first — avoid re-fetching URLs we've already resolved.
	if cached := a.loadEmbedCache(rawURL); cached != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached) //nolint:errcheck
		return
	}

	// Fetch the page HTML via the SSRF-safe client.
	fetchRes, err := htmlFetcher.Get(r.Context(), rawURL)
	switch {
	case errors.Is(err, safefetch.ErrBlockedAddress):
		fail(400, "that URL is not allowed (private/blocked address or scheme)")
		return
	case errors.Is(err, safefetch.ErrTooLarge):
		fail(400, "remote page exceeds the 1 MB limit")
		return
	case err != nil:
		fail(502, "could not fetch the URL: "+err.Error())
		return
	}
	if fetchRes.Status < 200 || fetchRes.Status >= 300 {
		fail(502, "remote host returned an error status")
		return
	}

	resolvedURL := fetchRes.FinalURL
	if resolvedURL == "" {
		resolvedURL = rawURL
	}

	// Parse OG tags from the HTML body.
	ogMeta := parseOGTags(string(fetchRes.Body))
	title := ogMeta["title"]
	description := ogMeta["description"]
	ogImage := ogMeta["image"]
	siteName := ogMeta["site_name"]

	// Detect provider from hostname.
	provider := detectEmbedProvider(parsed.Hostname(), siteName)

	// Download and store the thumbnail image using the same validated path as
	// media imports — magic-number checked, content-addressed, SSRF-safe.
	thumbURL := ""
	if ogImage != "" {
		thumbURL = a.fetchAndStoreEmbedThumb(r, ogImage)
	}

	result := &unfurlResponse{
		URL:         resolvedURL,
		Title:       title,
		Description: description,
		Provider:    provider,
		ThumbURL:    thumbURL,
	}

	// Persist to cache so subsequent paste of the same URL is instant.
	a.saveEmbedCache(rawURL, result)

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "embed", Severity: "info",
		Msg: "unfurled: " + rawURL, RequestID: getRequestID(r),
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result) //nolint:errcheck
}

// parseOGTags extracts og: meta properties from an HTML string.
func parseOGTags(htmlBody string) map[string]string {
	out := make(map[string]string)
	matches := ogTagRe.FindAllStringSubmatch(htmlBody, -1)
	for _, m := range matches {
		if m[1] != "" && m[2] != "" {
			out[strings.ToLower(m[1])] = m[2]
		} else if m[4] != "" && m[3] != "" {
			out[strings.ToLower(m[4])] = m[3]
		}
	}
	return out
}

// detectEmbedProvider returns a human-readable provider name.
func detectEmbedProvider(hostname, siteName string) string {
	h := strings.ToLower(strings.TrimPrefix(hostname, "www."))
	switch h {
	case "youtube.com", "youtu.be":
		return "YouTube"
	case "vimeo.com":
		return "Vimeo"
	case "twitter.com", "x.com":
		return "X / Twitter"
	}
	if siteName != "" {
		return siteName
	}
	return hostname
}

// fetchAndStoreEmbedThumb downloads the OG image URL via remoteImageFetcher
// and stores it using storeValidatedMedia (magic-number validated, same path
// as all other media). Returns the /media/<name> URL or "" on any failure.
func (a *App) fetchAndStoreEmbedThumb(r *http.Request, imgURL string) string {
	parsed, err := url.ParseRequestURI(imgURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}

	res, err := remoteImageFetcher.Get(r.Context(), imgURL)
	if err != nil {
		return ""
	}
	if res.Status < 200 || res.Status >= 300 {
		return ""
	}

	stored, err := storeValidatedMedia(res.Body, false /* rasters only */)
	if err != nil {
		return ""
	}

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "embed", Severity: "info",
		Msg: "embed thumb stored: " + stored.Name, RequestID: getRequestID(r),
	})

	return stored.URL
}

// loadEmbedCache checks embed_cache for a previously unfurled URL.
func (a *App) loadEmbedCache(rawURL string) *unfurlResponse {
	if dbpkg.DB == nil {
		return nil
	}
	row := dbpkg.DB.QueryRow(
		`SELECT resolved_url, title, description, provider, thumb_name FROM embed_cache WHERE url = ?`,
		rawURL,
	)
	var resolvedURL, title, description, provider, thumbName string
	if err := row.Scan(&resolvedURL, &title, &description, &provider, &thumbName); err != nil {
		return nil
	}
	thumbURL := ""
	if thumbName != "" {
		thumbURL = "/media/" + thumbName
	}
	return &unfurlResponse{
		URL:         resolvedURL,
		Title:       title,
		Description: description,
		Provider:    provider,
		ThumbURL:    thumbURL,
	}
}

// saveEmbedCache inserts or replaces a row in embed_cache.
func (a *App) saveEmbedCache(rawURL string, res *unfurlResponse) {
	if dbpkg.DB == nil {
		return
	}
	thumbName := strings.TrimPrefix(res.ThumbURL, "/media/")
	_, _ = dbpkg.DB.Exec(
		`INSERT INTO embed_cache (url, resolved_url, title, description, provider, thumb_name, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		 ON CONFLICT(url) DO UPDATE SET
		   resolved_url = excluded.resolved_url,
		   title        = excluded.title,
		   description  = excluded.description,
		   provider     = excluded.provider,
		   thumb_name   = excluded.thumb_name,
		   updated_at   = excluded.updated_at`,
		rawURL, res.URL, res.Title, res.Description, res.Provider, thumbName,
	)
}
