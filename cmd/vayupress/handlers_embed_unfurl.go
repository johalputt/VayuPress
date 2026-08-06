// SPDX-License-Identifier: Apache-2.0

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
//   - All text fields are HTML-escaped at render time (blockrender), and clamped
//     on the way INTO embed_cache: every one of them is copied out of a page the
//     caller nominated, and the table is bounded in row size and row count so a
//     media:write key cannot spend it as disk. See saveEmbedCache.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/embeds"
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
	Kind        string `json:"kind"`               // "link" (default) or "video"
	EmbedSrc    string `json:"embedSrc,omitempty"` // video: cookie-free iframe URL
}

// embedMeta is the JSON shape stored in embed_cache.raw_meta so video resolution
// (kind + privacy-origin embed URL) survives caching without a schema change.
type embedMeta struct {
	Kind     string `json:"kind,omitempty"`
	EmbedSrc string `json:"embedSrc,omitempty"`
}

// detectVideoEmbed returns the provider key and a validated cookie-free embed
// URL when rawURL is a recognised video link, else ("", "").
//
// The recognition itself lives in internal/embeds, which holds the one provider
// table the CSP allowlist, the sanitiser barrier and this detection are all
// derived from. This used to be a hand-written switch over hostnames alongside
// its own pair of id validators, which meant a new provider had to be added
// here AND in two regexes in two other packages, in agreement, with no gate
// checking that they were.
func detectVideoEmbed(rawURL string) (provider, embedSrc string) { return embeds.Detect(rawURL) }

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

	res, err := a.resolveEmbed(r.Context(), getRequestID(r), rawURL)
	if err != nil {
		var re *embedResolveError
		if errors.As(err, &re) {
			fail(re.Status, re.Message)
			return
		}
		fail(502, "could not resolve that URL")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res) //nolint:errcheck
}

// embedResolveError carries the HTTP status an unfurl failure should surface,
// so the resolver can be shared by the editor endpoint and the connector tool
// without either of them re-deriving what went wrong from an opaque error.
type embedResolveError struct {
	Status  int
	Message string
}

func (e *embedResolveError) Error() string { return e.Message }

// resolveEmbed turns a URL into a resolved embed payload: OG metadata, a locally
// stored thumbnail, and — when the URL is a recognised video — a validated
// cookie-free embed source for the click-to-load facade.
//
// It is the single implementation behind both the editor's paste-a-URL endpoint
// and the connector's embed_url tool. That matters more than the deduplication:
// every guard on this path (SSRF-safe fetch with a 1 MB cap, magic-number
// validation of the thumbnail through storeValidatedMedia, the media quota, the
// bounded and pruned cache) applies to a caller because it is on this path, not
// because that caller remembered to apply it. A second resolver written for the
// connector would have started as a copy and drifted.
func (a *App) resolveEmbed(ctx context.Context, reqID, rawURL string) (*unfurlResponse, error) {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, &embedResolveError{400, "url must be a valid http or https URL"}
	}

	// Check cache first — avoid re-fetching URLs we've already resolved.
	if cached := a.loadEmbedCache(rawURL); cached != nil {
		return cached, nil
	}

	// Fetch the page HTML via the SSRF-safe client.
	fetchRes, err := htmlFetcher.Get(ctx, rawURL)
	switch {
	case errors.Is(err, safefetch.ErrBlockedAddress):
		return nil, &embedResolveError{400, "that URL is not allowed (private/blocked address or scheme)"}
	case errors.Is(err, safefetch.ErrTooLarge):
		return nil, &embedResolveError{400, "remote page exceeds the 1 MB limit"}
	case err != nil:
		return nil, &embedResolveError{502, "could not fetch the URL: " + err.Error()}
	}
	if fetchRes.Status < 200 || fetchRes.Status >= 300 {
		return nil, &embedResolveError{502, "remote host returned an error status"}
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

	// If the URL is a known video, resolve a click-to-load facade: a validated
	// cookie-free embed URL the editor stores and the public page injects only on
	// click. Match on the original input URL (the embeddable id lives there).
	kind := "link"
	embedSrc := ""
	if vp, src := detectVideoEmbed(rawURL); src != "" {
		kind = "video"
		embedSrc = src
		// The display name comes from the provider table rather than a second
		// switch here, so adding a provider does not leave this branch labelling
		// it with its bare hostname.
		if n := embeds.Name(vp); n != "" {
			provider = n
		}
	}

	// Download and store the thumbnail image using the same validated path as
	// media imports — magic-number checked, content-addressed, SSRF-safe.
	thumbURL := ""
	if ogImage != "" {
		thumbURL = a.fetchAndStoreEmbedThumb(ctx, reqID, ogImage)
	}

	result := &unfurlResponse{
		URL:         resolvedURL,
		Title:       title,
		Description: description,
		Provider:    provider,
		ThumbURL:    thumbURL,
		Kind:        kind,
		EmbedSrc:    embedSrc,
	}

	// Persist to cache so a subsequent paste of the same URL is instant.
	a.saveEmbedCache(rawURL, result)

	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "embed", Severity: "info",
		Msg: "unfurled: " + rawURL, RequestID: reqID,
	})
	return result, nil
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
func (a *App) fetchAndStoreEmbedThumb(ctx context.Context, reqID, imgURL string) string {
	parsed, err := url.ParseRequestURI(imgURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return ""
	}

	res, err := remoteImageFetcher.Get(ctx, imgURL)
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
		Msg: "embed thumb stored: " + stored.Name, RequestID: reqID,
	})

	return stored.URL
}

// loadEmbedCache checks embed_cache for a previously unfurled URL.
func (a *App) loadEmbedCache(rawURL string) *unfurlResponse {
	if dbpkg.DB == nil {
		return nil
	}
	row := dbpkg.DB.QueryRow(
		`SELECT resolved_url, title, description, provider, thumb_name, raw_meta FROM embed_cache WHERE url = ?`,
		rawURL,
	)
	var resolvedURL, title, description, provider, thumbName, rawMeta string
	if err := row.Scan(&resolvedURL, &title, &description, &provider, &thumbName, &rawMeta); err != nil {
		return nil
	}
	thumbURL := ""
	if thumbName != "" {
		thumbURL = "/media/" + thumbName
	}
	kind := "link"
	embedSrc := ""
	if rawMeta != "" {
		var meta embedMeta
		if json.Unmarshal([]byte(rawMeta), &meta) == nil && meta.Kind != "" {
			kind = meta.Kind
			embedSrc = meta.EmbedSrc
		}
	}
	return &unfurlResponse{
		URL:         resolvedURL,
		Title:       title,
		Description: description,
		Provider:    provider,
		ThumbURL:    thumbURL,
		Kind:        kind,
		EmbedSrc:    embedSrc,
	}
}

// ── what the unfurl cache is allowed to cost ─────────────────────────────────
//
// Everything an embed_cache row carries — title, description, site name, final
// URL — is read out of a page the CALLER nominated, and the endpoint sits in the
// media capability section, so a key holding nothing but media:write reaches it.
// That is the same credential the MediaDir quota exists to contain, and until
// these bounds existed it could spend that credential here instead: the fetched
// page may be a megabyte, one og content attribute can carry most of it, the row
// key is the request URL so a varying query string makes every call an INSERT
// rather than an update, and nothing has ever deleted from this table.
//
// The consequence is not "a large cache". It is the outage the quota was written
// for, arriving one endpoint sideways — the filesystem fills, SQLite loses its
// ability to write, and the install answers 502.
//
// Both dimensions are bounded, because bounding either alone leaves the attack
// intact: rows capped in count but not in size still buy gigabytes, and rows
// capped in size but not in count still buy everything.
const (
	// Long enough for a real headline and a real card summary, short enough that
	// the whole table is a number an operator can hold in their head. Measured in
	// runes, so the cut never lands inside a multi-byte character and turns a
	// cached title into mojibake.
	maxCachedEmbedTitle       = 300
	maxCachedEmbedDescription = 1000
	maxCachedEmbedProvider    = 120
	// 2048 is the conventional practical ceiling for a URL; a redirect chain that
	// ends somewhere longer is a link nothing will render usefully anyway.
	maxCachedEmbedURL = 2048
)

// maxEmbedCacheRows caps how many URLs stay cached. A var, not a const, so a
// test can prove the eviction rather than take the number on trust.
//
// The `url` column is deliberately NOT truncated: it is the unique key, and a
// clipped key would answer a later lookup for a DIFFERENT URL with this row's
// metadata. It is bounded instead by the 8 KB request-body cap on the endpoint,
// which — multiplied by this ceiling — is what makes the table's worst case a
// constant rather than a function of how many URLs someone pasted.
var maxEmbedCacheRows = 5000

// clampRunes returns at most max runes of s, cutting on a rune boundary.
//
// Rune-counted rather than byte-counted because the fields it guards are
// attacker-supplied UTF-8 and a byte cut lands mid-sequence, which is how a
// length limit turns into a rendering bug nobody connects back to the limit.
func clampRunes(s string, max int) string {
	if max <= 0 || len(s) <= max { // bytes >= runes, so this fast path is safe
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return s[:i]
		}
		n++
	}
	return s
}

// saveEmbedCache inserts or replaces a row in embed_cache, clamped to what a
// card actually renders and followed by an eviction down to maxEmbedCacheRows.
func (a *App) saveEmbedCache(rawURL string, res *unfurlResponse) {
	if dbpkg.DB == nil {
		return
	}
	thumbName := strings.TrimPrefix(res.ThumbURL, "/media/")
	rawMeta := "{}"
	if res.Kind == "video" {
		if b, err := json.Marshal(embedMeta{Kind: res.Kind, EmbedSrc: res.EmbedSrc}); err == nil {
			rawMeta = string(b)
		}
	}
	_, _ = dbpkg.DB.Exec(
		`INSERT INTO embed_cache (url, resolved_url, title, description, provider, thumb_name, raw_meta, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		 ON CONFLICT(url) DO UPDATE SET
		   resolved_url = excluded.resolved_url,
		   title        = excluded.title,
		   description  = excluded.description,
		   provider     = excluded.provider,
		   thumb_name   = excluded.thumb_name,
		   raw_meta     = excluded.raw_meta,
		   updated_at   = excluded.updated_at`,
		rawURL,
		clampRunes(res.URL, maxCachedEmbedURL),
		clampRunes(res.Title, maxCachedEmbedTitle),
		clampRunes(res.Description, maxCachedEmbedDescription),
		clampRunes(res.Provider, maxCachedEmbedProvider),
		thumbName, rawMeta,
	)
	pruneEmbedCache()
}

// pruneEmbedCache drops everything past the newest maxEmbedCacheRows entries.
//
// It runs on every write rather than behind a "is it over yet?" check for two
// reasons: an unfurl has already paid for a network round trip so this is noise
// beside it, and an install that has been running with an unbounded table needs
// the very first write after an update to repair it rather than to leave the
// existing overgrowth in place forever.
//
// The tie-break on id is not cosmetic. updated_at has one-second granularity, so
// a burst of writes inside the same second is entirely ties, and ordering on the
// timestamp alone would let SQLite keep any subset it liked — including the
// oldest. id is the AUTOINCREMENT key, so it is the only monotonic thing here.
func pruneEmbedCache() {
	if dbpkg.DB == nil || maxEmbedCacheRows <= 0 {
		return
	}
	_, _ = dbpkg.DB.Exec(
		`DELETE FROM embed_cache
		  WHERE id NOT IN (SELECT id FROM embed_cache ORDER BY updated_at DESC, id DESC LIMIT ?)`,
		maxEmbedCacheRows,
	)
}
