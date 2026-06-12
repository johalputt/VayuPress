// VayuPress — main.go  v1.0.0-p15
// Bootstrap, route wiring, and graceful shutdown only.
// Domain logic lives in internal/* packages (ADR-0045, ADR-0046).
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/mattn/go-sqlite3"
	"github.com/microcosm-cc/bluemonday"
	"github.com/rs/cors"
	"github.com/sony/gobreaker"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/health"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/plugins"
	"github.com/johalputt/vayupress/internal/queue"
	"github.com/johalputt/vayupress/internal/render"
)

var Version = "1.0.0-p15"
var bootTime = time.Now()

// Immutable package-level values (compiled once, never mutated).
var (
	slugRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,198}[a-z0-9]$|^[a-z0-9]$`)
	htmlTagRe = regexp.MustCompile(`<[^>]+>`)
)

// App holds all mutable runtime state. Handlers are methods on *App so that
// they depend on explicit fields rather than package-level globals (ADR-0046).
type App struct {
	// HTTP
	outboundClient *http.Client

	// Sanitization
	policy *bluemonday.Policy

	// Meilisearch
	meiliCB *gobreaker.CircuitBreaker

	// Plugin subsystem
	pluginRegistry *plugins.Registry
	pluginManager  *plugins.Manager

	// Vacuum lifecycle
	vacuumMu      sync.Mutex
	vacuumLastRun time.Time

	// Smoke test
	smokeTestMutex sync.Mutex

	// Admin metrics snapshot cache
	metricsSnapshot atomic.Value

	// Benchmark state
	lastBenchmark    *benchmarkResult
	lastBenchmarkMu  sync.Mutex
	benchmarkRunning int32
}

const vacuumWriteThreshold = 10

// =============================================================================
// SSRF protection (ADR-0009)
// =============================================================================

func isPrivateOrReservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("100.100.100.200")) {
		return true
	}
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil && (v6[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

func ssrfSafeTransport() *http.Transport {
	base := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ipa := range ips {
				if isPrivateOrReservedIP(ipa.IP) && !isAllowedInternalHost(host) {
					return nil, fmt.Errorf("ssrf: refusing to connect to private/reserved IP %s (host %q)", ipa.IP, host)
				}
			}
			return base.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

func isAllowedInternalHost(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// =============================================================================
// Magic-number file-type verification
// =============================================================================

var allowedMagicNumbers = map[string][]byte{
	"image/jpeg":      {0xFF, 0xD8, 0xFF},
	"image/png":       {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
	"image/gif":       {0x47, 0x49, 0x46, 0x38},
	"image/webp":      {0x52, 0x49, 0x46, 0x46},
	"application/pdf": {0x25, 0x50, 0x44, 0x46},
}

func verifyMagicNumber(data []byte) (string, error) {
	for mime, sig := range allowedMagicNumbers {
		if len(data) >= len(sig) && bytes.Equal(data[:len(sig)], sig) {
			return mime, nil
		}
	}
	return "", fmt.Errorf("file type not allowed: magic number does not match any permitted media type")
}

// RegisterHook registers a plugin hook with the App's plugin registry.
func (a *App) RegisterHook(event string, fn plugins.HookFunc) {
	a.pluginRegistry.Register(event, fn)
}

// FireHook dispatches an event to the App's plugin manager (noop if VAYU_PLUGINS_ENABLED != true).
func (a *App) FireHook(event string, payload map[string]interface{}) {
	if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" {
		return
	}
	a.pluginManager.Fire(event, payload)
}

// =============================================================================
// Request ID context
// =============================================================================

type ctxKeyRequestID struct{}

func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			b := make([]byte, 8)
			if _, err := rand.Read(b); err != nil {
				reqID = fmt.Sprintf("ts-%x", time.Now().UnixNano())
			} else {
				reqID = hex.EncodeToString(b)
			}
		}
		w.Header().Set("X-Request-ID", reqID)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID{}, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getRequestID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyRequestID{}).(string); ok {
		return v
	}
	return ""
}

func structuredLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		dur := time.Since(start)
		metrics.HTTPLatency.Record(dur)
		logging.LogJSON(logging.LogFields{Level: "info", RequestID: getRequestID(r), Method: r.Method, Path: r.URL.Path, Status: ww.Status(), LatencyMS: dur.Milliseconds(), RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent(), Component: "http"})
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		nonce := render.GenerateCSPNonce()
		csp := fmt.Sprintf("default-src 'self'; font-src 'self'; style-src 'self'; script-src 'self' 'nonce-%s'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'", nonce)
		w.Header().Set("Content-Security-Policy", csp)
		ctx := render.WithCSPNonce(r.Context(), nonce)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func csrfCookieSecure() bool {
	if v := os.Getenv("CSRF_SECURE_COOKIE"); v != "" {
		return v == "true"
	}
	return config.Cfg.Domain != "localhost"
}

// =============================================================================
// Response helpers
// =============================================================================

type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Docs      string `json:"docs"`
}

func writeJSON(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

func writeAPIError(w http.ResponseWriter, r *http.Request, code int, errCode, msg, docsURL string) {
	reqID := ""
	if r != nil {
		reqID = getRequestID(r)
	}
	writeJSON(w, r, code, map[string]apiError{"error": {Code: errCode, Message: msg, RequestID: reqID, Docs: docsURL}})
}

func readJSONDirect(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 10<<20)).Decode(v)
}

func splitTags(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validateArticleInput(title, slug, content string, tags []string) error {
	if title == "" || len(title) > 500 {
		return fmt.Errorf("title required (1–500 chars)")
	}
	if !isValidSlug(slug) {
		return fmt.Errorf("invalid slug")
	}
	if content == "" || len(content) > 5_000_000 {
		return fmt.Errorf("content required (1 byte – 5 MB)")
	}
	if len(tags) > 20 {
		return fmt.Errorf("max 20 tags")
	}
	for _, t := range tags {
		if len(t) > 100 {
			return fmt.Errorf("tag too long: %q", t)
		}
	}
	return nil
}

func isValidSlug(s string) bool { return slugRe.MatchString(s) }

func newUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// =============================================================================
// Meilisearch (circuit-breaker guarded)
// =============================================================================

func (a *App) initMeilisearchCB() {
	a.meiliCB = gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name: "meilisearch", MaxRequests: 3, Interval: 10 * time.Second, Timeout: 30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 3 && float64(counts.TotalFailures)/float64(counts.Requests) >= 0.60
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "meili-cb", Msg: fmt.Sprintf("%s → %s", from, to)})
		},
	})
}

func (a *App) meiliDo(method, path string, body interface{}) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, config.Cfg.MeiliHost+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
	}
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("meili %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (a *App) configureMeilisearch() {
	_ = a.meiliDo("PATCH", "/indexes/articles/settings", map[string]interface{}{
		"rankingRules":         []string{"words", "proximity", "attribute", "sort", "exactness"},
		"searchableAttributes": []string{"title", "tags", "content"},
		"filterableAttributes": []string{"tags", "created_at"},
		"sortableAttributes":   []string{"created_at", "updated_at"},
	})
}

func (a *App) indexArticle(art dbpkg.Article) {
	if a.meiliCB == nil {
		return
	}
	doc := map[string]interface{}{
		"id": art.ID, "title": art.Title, "slug": art.Slug,
		"content":    htmlTagRe.ReplaceAllString(a.policy.Sanitize(art.Content), ""),
		"tags":       art.Tags,
		"created_at": art.CreatedAt.Unix(),
	}
	_, err := a.meiliCB.Execute(func() (interface{}, error) {
		return nil, a.meiliDo("POST", "/indexes/articles/documents", []map[string]interface{}{doc})
	})
	if err != nil {
		atomic.AddInt64(&metrics.MetricMeiliErrors, 1)
	}
}

func (a *App) purgeCloudflare(slug string) {
	if config.Cfg.CFZoneID == "" || config.Cfg.CFAPIToken == "" {
		return
	}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/purge_cache", config.Cfg.CFZoneID)
	body, _ := json.Marshal(map[string][]string{"files": {"https://" + config.Cfg.Domain + "/" + slug}})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+config.Cfg.CFAPIToken)
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

func (a *App) pingIndexNow(slug string) {
	if config.Cfg.IndexNowKey == "" {
		return
	}
	body, _ := json.Marshal(map[string]interface{}{
		"host": config.Cfg.Domain, "key": config.Cfg.IndexNowKey,
		"keyLocation": "https://" + config.Cfg.Domain + "/.well-known/" + config.Cfg.IndexNowKey + ".txt",
		"urlList":     []string{"https://" + config.Cfg.Domain + "/" + slug},
	})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.indexnow.org/indexnow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

// =============================================================================
// Sitemap / RSS / robots
// =============================================================================

func generateSitemap() {
	rows, err := dbpkg.DB.Query(`SELECT slug,updated_at FROM articles ORDER BY updated_at DESC LIMIT 50000`)
	if err != nil {
		return
	}
	defer rows.Close()
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for rows.Next() {
		var slug string
		var updated time.Time
		rows.Scan(&slug, &updated)
		fmt.Fprintf(&sb, "<url><loc>https://%s/%s</loc><lastmod>%s</lastmod></url>", config.Cfg.Domain, slug, updated.Format("2006-01-02"))
	}
	sb.WriteString("</urlset>")
	render.CacheWrite("sitemap.xml", sb.String()) //nolint:errcheck
}

func generateRSS() {
	rows, err := dbpkg.DB.Query(`SELECT title,slug,content,created_at FROM articles ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return
	}
	defer rows.Close()
	var items strings.Builder
	for rows.Next() {
		var title, slug, content string
		var created time.Time
		rows.Scan(&title, &slug, &content, &created)
		plain := htmlTagRe.ReplaceAllString(bluemonday.StrictPolicy().Sanitize(content), "")
		if len(plain) > 500 {
			plain = plain[:500] + "..."
		}
		fmt.Fprintf(&items, "<item><title><![CDATA[%s]]></title><link>https://%s/%s</link><guid isPermaLink=\"true\">https://%s/%s</guid><pubDate>%s</pubDate><description><![CDATA[%s]]></description></item>",
			title, config.Cfg.Domain, slug, config.Cfg.Domain, slug, created.Format(time.RFC1123Z), plain)
	}
	rss := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>%s</title><link>https://%s</link><description>%s</description>%s</channel></rss>`,
		config.Cfg.Domain, config.Cfg.Domain, config.Cfg.Domain, items.String())
	render.CacheWrite("feed.xml", rss) //nolint:errcheck
}

func generateRobots() {
	render.CacheWrite("robots.txt", fmt.Sprintf("User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /admin\n\nSitemap: https://%s/sitemap.xml\n", config.Cfg.Domain)) //nolint:errcheck
}

// =============================================================================
// Admin metrics snapshot
// =============================================================================

type adminMetricsSnapshot struct {
	TotalArticles  int
	PendingJobs    int
	FailedJobs     int
	CompletedJobs  int
	StorageBytes   int64
	QuotaBytes     int64
	StoragePct     float64
	WorkersAlive   int64
	CacheHitRatio  float64
	UptimeSeconds  float64
	HTTPP95        int64
	WriteP99       int64
	RenderP99      int64
	RecentArticles []adminRecentArticle
	SnapshotAt     time.Time
}

type adminRecentArticle struct {
	Title     string
	Slug      string
	CreatedAt time.Time
}

func (a *App) collectAdminMetrics() {
	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	row := dbpkg.DB.QueryRow(`SELECT (SELECT COUNT(1) FROM articles),SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM write_jobs`)
	row.Scan(&snap.TotalArticles, &snap.PendingJobs, &snap.FailedJobs, &snap.CompletedJobs)
	snap.StorageBytes = dbpkg.StorageUsedBytes()
	snap.QuotaBytes = dbpkg.StorageQuotaBytes()
	if snap.QuotaBytes > 0 {
		snap.StoragePct = float64(snap.StorageBytes) / float64(snap.QuotaBytes) * 100
	}
	snap.WorkersAlive = atomic.LoadInt64(&metrics.WorkerLiveness)
	snap.CacheHitRatio = metrics.CacheHitRatio()
	snap.UptimeSeconds = time.Since(bootTime).Seconds()
	snap.HTTPP95 = metrics.HTTPLatency.Percentile(95)
	snap.WriteP99 = metrics.QueueJobLatency.Percentile(99)
	snap.RenderP99 = metrics.RenderLatency.Percentile(99)
	rows, err := dbpkg.DB.Query(`SELECT title,slug,created_at FROM articles ORDER BY created_at DESC LIMIT 15`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ra adminRecentArticle
			rows.Scan(&ra.Title, &ra.Slug, &ra.CreatedAt)
			snap.RecentArticles = append(snap.RecentArticles, ra)
		}
	}
	a.metricsSnapshot.Store(snap)
}

func (a *App) startMetricsSnapshotCollector() {
	a.collectAdminMetrics()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-queue.DoneCh:
				return
			case <-ticker.C:
				a.collectAdminMetrics()
			}
		}
	}()
}

func (a *App) getAdminSnapshot() *adminMetricsSnapshot {
	if v := a.metricsSnapshot.Load(); v != nil {
		return v.(*adminMetricsSnapshot)
	}
	a.collectAdminMetrics()
	if v := a.metricsSnapshot.Load(); v != nil {
		return v.(*adminMetricsSnapshot)
	}
	return &adminMetricsSnapshot{SnapshotAt: time.Now()}
}

// =============================================================================
// Article API handlers
// =============================================================================

func (a *App) handleCreateArticle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title   string   `json:"title"`
		Slug    string   `json:"slug"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := readJSONDirect(r, &in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "https://docs.vayupress.com/api/articles")
		return
	}
	if err := validateArticleInput(in.Title, in.Slug, in.Content, in.Tags); err != nil {
		writeAPIError(w, r, 400, "validation_error", err.Error(), "https://docs.vayupress.com/api/articles")
		return
	}
	if dbpkg.StorageUsedBytes() >= dbpkg.StorageQuotaBytes() {
		writeAPIError(w, r, 413, "storage_quota_exceeded", fmt.Sprintf("quota %dGB exceeded", config.Cfg.StorageQuotaGB), "https://docs.vayupress.com/api/articles")
		return
	}
	var count int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
	if count > 0 {
		writeAPIError(w, r, 409, "slug_conflict", "slug already exists", "https://docs.vayupress.com/api/articles")
		return
	}
	art := dbpkg.Article{ID: newUUID(), Title: in.Title, Slug: in.Slug, Content: in.Content, Tags: in.Tags, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(art)
	if _, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		writeAPIError(w, r, 500, "queue_error", err.Error(), "https://docs.vayupress.com/api/errors")
		return
	}
	dbpkg.AuditLog("article.create", dbpkg.AuditActor(r), art.Slug, "id="+art.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "id": art.ID, "slug": art.Slug})
}

func (a *App) handleBulkCreateArticles(w http.ResponseWriter, r *http.Request) {
	var articles []struct {
		Title, Slug, Content string
		Tags                 []string `json:"tags"`
	}
	if err := readJSONDirect(r, &articles); err != nil {
		writeAPIError(w, r, 400, "invalid_json", err.Error(), "https://docs.vayupress.com/api/articles")
		return
	}
	if len(articles) > 1000 {
		writeAPIError(w, r, 400, "too_many_articles", "max 1000", "https://docs.vayupress.com/api/articles")
		return
	}
	queued, skipped := 0, 0
	var skipReasons []string
	for _, in := range articles {
		if err := validateArticleInput(in.Title, in.Slug, in.Content, in.Tags); err != nil {
			skipped++
			skipReasons = append(skipReasons, in.Slug+": "+err.Error())
			continue
		}
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
		if count > 0 {
			skipped++
			skipReasons = append(skipReasons, in.Slug+": duplicate slug")
			continue
		}
		a := dbpkg.Article{ID: newUUID(), Title: in.Title, Slug: in.Slug, Content: in.Content, Tags: in.Tags, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		payload, _ := json.Marshal(a)
		dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload)
		queued++
	}
	writeJSON(w, r, 202, map[string]interface{}{"status": "queued", "queued": queued, "skipped": skipped, "skip_reasons": skipReasons})
}

func (a *App) handleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	art.Tags = splitTags(tagsStr)
	var in struct {
		Title   *string  `json:"title"`
		Content *string  `json:"content"`
		Tags    []string `json:"tags"`
	}
	if err := readJSONDirect(r, &in); err != nil {
		writeAPIError(w, r, 400, "invalid_json", "", "https://docs.vayupress.com/api/articles")
		return
	}
	if in.Title != nil {
		art.Title = *in.Title
	}
	if in.Content != nil {
		art.Content = *in.Content
	}
	if in.Tags != nil {
		art.Tags = in.Tags
	}
	art.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(art)
	dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'update')`, payload)
	dbpkg.AuditLog("article.update", dbpkg.AuditActor(r), art.Slug, "id="+art.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "slug": art.Slug})
}

func (a *App) handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	art.Tags = splitTags(tagsStr)
	payload, _ := json.Marshal(art)
	dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	dbpkg.AuditLog("article.delete", dbpkg.AuditActor(r), slug, "id="+art.ID)
	writeJSON(w, r, 200, map[string]string{"status": "queued", "slug": slug})
}

func (a *App) handleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !isValidSlug(slug) {
		writeAPIError(w, r, 400, "invalid_slug", "invalid slug", "https://docs.vayupress.com/api/articles")
		return
	}
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	art.Tags = splitTags(tagsStr)
	writeJSON(w, r, 200, art)
}

func (a *App) handleListArticles(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tag := r.URL.Query().Get("tag")
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit
	type row struct {
		ID, Title, Slug      string
		Tags                 []string
		CreatedAt, UpdatedAt time.Time
	}
	var (
		rows_ *sql.Rows
		err   error
		total int
	)
	if tag != "" {
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE tags LIKE ?`, "%"+tag+"%").Scan(&total)
		rows_, err = dbpkg.DB.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles WHERE tags LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, "%"+tag+"%", limit, offset)
	} else {
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&total)
		rows_, err = dbpkg.DB.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
	}
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "database error", "https://docs.vayupress.com/api/errors")
		return
	}
	defer rows_.Close()
	var result []row
	for rows_.Next() {
		var rr row
		var tagsStr string
		rows_.Scan(&rr.ID, &rr.Title, &rr.Slug, &tagsStr, &rr.CreatedAt, &rr.UpdatedAt)
		rr.Tags = splitTags(tagsStr)
		result = append(result, rr)
	}
	if result == nil {
		result = []row{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"articles": result, "page": page, "limit": limit, "total": total, "pages": (total + limit - 1) / limit})
}

func (a *App) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if q == "" {
		writeJSON(w, r, 200, map[string]interface{}{"hits": []interface{}{}, "query": ""})
		return
	}
	if a.meiliCB == nil || a.meiliCB.State() != gobreaker.StateClosed {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	body, _ := json.Marshal(map[string]interface{}{"q": q, "limit": limit, "attributesToRetrieve": []string{"title", "slug", "tags", "created_at"}})
	req, err := http.NewRequestWithContext(context.Background(), "POST", config.Cfg.MeiliHost+"/indexes/articles/search", bytes.NewReader(body))
	if err != nil {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if config.Cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.MeiliMasterKey)
	}
	resp, err := a.outboundClient.Do(req)
	if err != nil {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		a.handleSearchFallback(w, r, q, limit)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.Copy(w, resp.Body)
}

func (a *App) handleSearchFallback(w http.ResponseWriter, r *http.Request, q string, limit int) {
	pattern := "%" + q + "%"
	rows, err := dbpkg.DB.Query(`SELECT title,slug,tags,created_at FROM articles WHERE title LIKE ? OR content LIKE ? OR tags LIKE ? ORDER BY created_at DESC LIMIT ?`, pattern, pattern, pattern, limit)
	if err != nil {
		writeAPIError(w, r, 500, "search_error", "search unavailable", "https://docs.vayupress.com/api/search")
		return
	}
	defer rows.Close()
	type hit struct {
		Title, Slug string
		Tags        []string
		CreatedAt   time.Time
	}
	var hits []hit
	for rows.Next() {
		var h hit
		var tagsStr string
		rows.Scan(&h.Title, &h.Slug, &tagsStr, &h.CreatedAt)
		h.Tags = splitTags(tagsStr)
		hits = append(hits, h)
	}
	if hits == nil {
		hits = []hit{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"hits": hits, "query": q, "fallback": true})
}

func (a *App) handleListTags(w http.ResponseWriter, r *http.Request) {
	rows, err := dbpkg.DB.Query(`SELECT tags FROM articles WHERE tags != ''`)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "", "https://docs.vayupress.com/api/errors")
		return
	}
	defer rows.Close()
	tagCount := make(map[string]int)
	for rows.Next() {
		var tagsStr string
		rows.Scan(&tagsStr)
		for _, t := range splitTags(tagsStr) {
			if t != "" {
				tagCount[t]++
			}
		}
	}
	type tagRow struct {
		Tag   string `json:"tag"`
		Count int    `json:"count"`
	}
	result := make([]tagRow, 0, len(tagCount))
	for t, c := range tagCount {
		result = append(result, tagRow{t, c})
	}
	writeJSON(w, r, 200, map[string]interface{}{"tags": result, "total": len(result)})
}

// =============================================================================
// Stats / metrics / queue handlers
// =============================================================================

func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	var totalArticles, pendingJobs, failedJobs int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&failedJobs)
	used := dbpkg.StorageUsedBytes()
	quota := dbpkg.StorageQuotaBytes()
	writeJSON(w, r, 200, map[string]interface{}{
		"version": Version, "uptime_seconds": time.Since(bootTime).Seconds(),
		"config_version": config.ConfigVersion,
		"articles_total": totalArticles, "queue_pending": pendingJobs, "queue_failed": failedJobs,
		"storage_used_bytes": used, "storage_quota_bytes": quota,
		"workers_alive":    atomic.LoadInt64(&metrics.WorkerLiveness),
		"maintenance_mode": config.Cfg.MaintenanceMode,
		"metrics": map[string]int64{
			"articles_created": atomic.LoadInt64(&metrics.MetricArticlesCreated), "articles_updated": atomic.LoadInt64(&metrics.MetricArticlesUpdated),
			"articles_deleted": atomic.LoadInt64(&metrics.MetricArticlesDeleted), "queue_processed": atomic.LoadInt64(&metrics.MetricQueueProcessed),
			"wal_adaptive_checkpoints": atomic.LoadInt64(&metrics.MetricWALAdaptiveCheckpoints),
			"migration_drift_detected": atomic.LoadInt64(&metrics.MetricMigrationDriftDetected),
			"poison_jobs_quarantined":  atomic.LoadInt64(&metrics.MetricPoisonJobsQuarantined),
			"pprof_accesses":           atomic.LoadInt64(&metrics.MetricPprofAccesses),
			"vacuum_rejected":          atomic.LoadInt64(&metrics.MetricVacuumRejected),
		},
		"latency_ms": map[string]interface{}{
			"http_p95": metrics.HTTPLatency.Percentile(95), "http_p99": metrics.HTTPLatency.Percentile(99),
			"render_p99": metrics.RenderLatency.Percentile(99), "queue_job_p99": metrics.QueueJobLatency.Percentile(99),
			"sqlite_write_p99": metrics.SQLiteWriteLatency.Percentile(99),
		},
	})
}

func (a *App) handleQueueStatus(w http.ResponseWriter, r *http.Request) {
	queue.HandleQueueStatus(w, r, writeJSON)
}

func (a *App) handleQueueReplay(w http.ResponseWriter, r *http.Request) {
	queue.HandleQueueReplay(w, r, writeJSON, writeAPIError)
}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var totalArticles int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w,
		"vayupress_uptime_seconds %.0f\nvayupress_articles_total %d\n"+
			"vayupress_articles_created_total %d\nvayupress_articles_updated_total %d\nvayupress_articles_deleted_total %d\n"+
			"vayupress_queue_processed_total %d\nvayupress_queue_failed_total %d\nvayupress_queue_stuck_resets_total %d\n"+
			"vayupress_meili_errors_total %d\nvayupress_cache_hits_total %d\nvayupress_cache_misses_total %d\n"+
			"vayupress_cache_hit_ratio %.4f\nvayupress_memory_alloc_bytes %d\nvayupress_workers_alive %d\n"+
			"vayupress_storage_used_bytes %d\nvayupress_plugin_panics_total %d\nvayupress_auth_lockouts_total %d\n"+
			"vayupress_wal_checkpoints_total %d\nvayupress_slow_queries_total %d\nvayupress_dead_letter_total %d\n"+
			"vayupress_wal_checkpoint_duration_ms_total %d\nvayupress_wal_adaptive_checkpoints_total %d\n"+
			"vayupress_migration_drift_detected_total %d\nvayupress_poison_jobs_quarantined_total %d\n"+
			"vayupress_pprof_accesses_total %d\nvayupress_vacuum_rejected_total %d\n"+
			"vayupress_health_degraded_events_total %d\n",
		time.Since(bootTime).Seconds(), totalArticles,
		atomic.LoadInt64(&metrics.MetricArticlesCreated), atomic.LoadInt64(&metrics.MetricArticlesUpdated), atomic.LoadInt64(&metrics.MetricArticlesDeleted),
		atomic.LoadInt64(&metrics.MetricQueueProcessed), atomic.LoadInt64(&metrics.MetricQueueFailed), atomic.LoadInt64(&metrics.MetricQueueStuckResets),
		atomic.LoadInt64(&metrics.MetricMeiliErrors), atomic.LoadInt64(&metrics.MetricCacheHits), atomic.LoadInt64(&metrics.MetricCacheMisses),
		metrics.CacheHitRatio(), ms.Alloc, atomic.LoadInt64(&metrics.WorkerLiveness),
		atomic.LoadInt64(&metrics.CachedStorageBytes), atomic.LoadInt64(&metrics.MetricPluginPanics), atomic.LoadInt64(&metrics.MetricAuthLockouts),
		atomic.LoadInt64(&metrics.MetricWALCheckpoints), atomic.LoadInt64(&metrics.MetricSlowQueries), atomic.LoadInt64(&metrics.MetricDeadLetterJobs),
		atomic.LoadInt64(&metrics.MetricWALCheckpointDurationMS), atomic.LoadInt64(&metrics.MetricWALAdaptiveCheckpoints),
		atomic.LoadInt64(&metrics.MetricMigrationDriftDetected), atomic.LoadInt64(&metrics.MetricPoisonJobsQuarantined),
		atomic.LoadInt64(&metrics.MetricPprofAccesses), atomic.LoadInt64(&metrics.MetricVacuumRejected),
		atomic.LoadInt64(&metrics.MetricHealthDegradedEvents),
	)
	fmt.Fprint(w, metrics.HTTPLatency.Prometheus("vayupress_http_request_duration_seconds", "HTTP latency"))
	fmt.Fprint(w, metrics.RenderLatency.Prometheus("vayupress_render_duration_seconds", "Render latency"))
	fmt.Fprint(w, metrics.QueueJobLatency.Prometheus("vayupress_queue_job_duration_seconds", "Queue job latency"))
	fmt.Fprint(w, metrics.SQLiteWriteLatency.Prometheus("vayupress_sqlite_write_duration_seconds", "SQLite write latency"))
}

// =============================================================================
// Admin handlers
// =============================================================================

func (a *App) handleAdminVacuum(w http.ResponseWriter, r *http.Request) {
	a.vacuumMu.Lock()
	defer a.vacuumMu.Unlock()
	cooldown := time.Duration(config.Cfg.VacuumCooldownMin) * time.Minute
	if !a.vacuumLastRun.IsZero() && time.Since(a.vacuumLastRun) < cooldown {
		remaining := cooldown - time.Since(a.vacuumLastRun)
		atomic.AddInt64(&metrics.MetricVacuumRejected, 1)
		writeAPIError(w, r, 429, "vacuum_cooldown", fmt.Sprintf("cooldown active — %ds remaining", int(remaining.Seconds())), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	var pending int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	if pending > vacuumWriteThreshold {
		atomic.AddInt64(&metrics.MetricVacuumRejected, 1)
		writeAPIError(w, r, 503, "vacuum_write_threshold", fmt.Sprintf("VACUUM rejected: %d pending jobs > threshold %d", pending, vacuumWriteThreshold), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	start := time.Now()
	var integrityResult string
	dbpkg.DB.QueryRow(`PRAGMA integrity_check`).Scan(&integrityResult)
	if integrityResult != "ok" {
		writeAPIError(w, r, 500, "integrity_failed", "SQLite integrity check failed: "+integrityResult, "https://docs.vayupress.com/operations/vacuum")
		return
	}
	if _, err := dbpkg.DB.Exec(`VACUUM`); err != nil {
		writeAPIError(w, r, 500, "vacuum_failed", "VACUUM error: "+err.Error(), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	a.vacuumLastRun = time.Now()
	logging.LogInfo("vacuum", fmt.Sprintf("VACUUM complete dur=%dms (ADR-0038)", time.Since(start).Milliseconds()))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "integrity": "ok", "duration_ms": time.Since(start).Milliseconds(), "next_allowed_in_minutes": config.Cfg.VacuumCooldownMin})
}

var pprofMux = http.NewServeMux()

func initPprofMux() {
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	logging.LogInfo("pprof", "explicit pprof mux initialized — DefaultServeMux unmodified (ADR-0037)")
}

func (a *App) pprofHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip = xri
	}
	if !auth.AllowPprof(ip) {
		atomic.AddInt64(&metrics.MetricPprofAccesses, 1)
		writeAPIError(w, r, 429, "pprof_rate_limited", fmt.Sprintf("pprof rate limit exceeded (%d/min)", config.Cfg.PprofRateLimit), "https://docs.vayupress.com/operations/profiling")
		return
	}
	atomic.AddInt64(&metrics.MetricPprofAccesses, 1)
	logging.LogJSON(logging.LogFields{Level: "info", Component: "pprof-access", RequestID: getRequestID(r), RemoteAddr: ip, Path: r.URL.Path, Msg: "pprof access (ADR-0037)"})
	pprofMux.ServeHTTP(w, r)
}

func (a *App) handleAdminBackupValidate(w http.ResponseWriter, r *http.Request) {
	backupDir := "/backups/vayupress"
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		writeAPIError(w, r, 404, "no_backup_dir", "backup directory not found: "+backupDir, "https://docs.vayupress.com/operations/backup")
		return
	}
	var latestBackup string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".db") && !strings.HasSuffix(e.Name(), ".db.gz") {
			continue
		}
		info, _ := e.Info()
		if info != nil && info.ModTime().After(latestMod) {
			latestMod = info.ModTime()
			latestBackup = filepath.Join(backupDir, e.Name())
		}
	}
	if latestBackup == "" {
		writeAPIError(w, r, 404, "no_backup", "no backup files found", "https://docs.vayupress.com/operations/backup")
		return
	}
	start := time.Now()
	checksumOK := false
	checksumFile := filepath.Join(backupDir, "checksums.json")
	if data, err := os.ReadFile(checksumFile); err == nil {
		var registry map[string]string
		if json.Unmarshal(data, &registry) == nil {
			if storedSum, ok := registry[filepath.Base(latestBackup)]; ok {
				if f, ferr := os.Open(latestBackup); ferr == nil {
					h := sha256.New()
					io.Copy(h, f)
					f.Close()
					checksumOK = hex.EncodeToString(h.Sum(nil)) == storedSum
				}
			}
		}
	}
	logging.LogInfo("backup-validate", fmt.Sprintf("backup=%s checksum_ok=%v dur=%dms (ADR-0042)", filepath.Base(latestBackup), checksumOK, time.Since(start).Milliseconds()))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "latest_backup": filepath.Base(latestBackup), "backup_age_hours": time.Since(latestMod).Hours(), "checksum_verified": checksumOK, "duration_ms": time.Since(start).Milliseconds()})
}

func (a *App) handleAdminCachePurge(w http.ResponseWriter, r *http.Request) {
	rid := getRequestID(r)
	slug := r.URL.Query().Get("slug")
	purged := 0
	purgeType := "targeted"
	if slug != "" {
		if !isValidSlug(slug) {
			writeAPIError(w, r, 400, "invalid_slug", "invalid slug", "https://docs.vayupress.com/api/cache")
			return
		}
		var tags string
		dbpkg.DB.QueryRow(`SELECT tags FROM articles WHERE slug=?`, slug).Scan(&tags)
		render.CachePurge(slug, splitTags(tags), generateSitemap, generateRSS, generateRobots)
		purged = 1
	} else {
		purgeType = "full"
		remoteIP := r.Header.Get("X-Real-IP")
		if remoteIP == "" {
			remoteIP = strings.Split(r.RemoteAddr, ":")[0]
		}
		if !auth.AllowPurge(remoteIP) {
			writeAPIError(w, r, 429, "rate_limited", "full cache purge rate-limited", "https://docs.vayupress.com/api/cache")
			return
		}
		postsDir := filepath.Join(config.Cfg.CacheDir, "posts")
		if files, err := os.ReadDir(postsDir); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".html") {
					fpath := filepath.Join(postsDir, f.Name())
					if fi, err := os.Stat(fpath); err == nil {
						dbpkg.UpdateStorageDelta(-fi.Size())
					}
					if err := os.Remove(fpath); err == nil {
						purged++
					}
				}
			}
		}
		os.Remove(filepath.Join(config.Cfg.CacheDir, "home", "index.html"))
		if files, err := os.ReadDir(filepath.Join(config.Cfg.CacheDir, "tags")); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".html") {
					os.Remove(filepath.Join(config.Cfg.CacheDir, "tags", f.Name()))
					purged++
				}
			}
		}
		go generateSitemap()
		go generateRSS()
		go generateRobots()
	}
	logging.LogJSON(logging.LogFields{Level: "info", Component: "cache-purge", RequestID: rid, Msg: fmt.Sprintf("type=%s purged=%d", purgeType, purged)})
	a.FireHook("cache.purge", map[string]interface{}{"purge_type": purgeType, "slug": slug, "purged_count": purged})
	writeJSON(w, r, 200, map[string]interface{}{"message": "cache purged", "purge_type": purgeType, "purged": purged, "request_id": rid})
}

func (a *App) handleArticlePage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !isValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	isAdmin := r.Header.Get("X-API-Key") == config.Cfg.APIKey
	cachePath := filepath.Join(config.Cfg.CacheDir, "posts", slug+".html")
	if !isAdmin || r.URL.Query().Get("layout") == "" {
		if _, err := os.Stat(cachePath); err == nil {
			atomic.AddInt64(&metrics.MetricCacheHits, 1)
			http.ServeFile(w, r, cachePath)
			return
		}
	}
	atomic.AddInt64(&metrics.MetricCacheMisses, 1)
	var art dbpkg.Article
	var tagsStr string
	if err := dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt); err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	art.Tags = splitTags(tagsStr)
	layout := render.DetectLayout(art, r, isAdmin)
	html, err := render.RenderArticleWithLayout(art, layout)
	if err != nil {
		http.Error(w, "render error", 500)
		return
	}
	if layout == render.ArticleLayoutDefault {
		render.CacheWrite(filepath.Join("posts", slug+".html"), html) //nolint:errcheck
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func (a *App) handleSmokeTest(w http.ResponseWriter, r *http.Request) {
	if !a.smokeTestMutex.TryLock() {
		http.Error(w, "smoke-test already running", http.StatusServiceUnavailable)
		return
	}
	defer a.smokeTestMutex.Unlock()
	testSlug := fmt.Sprintf("smoke-test-%d", time.Now().UnixNano())
	testID := newUUID()
	smokeArt := dbpkg.Article{ID: testID, Title: "Smoke Test", Slug: testSlug, Content: "<p>VayuPress smoke test.</p>", Tags: []string{"smoke-test"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(smokeArt)
	if _, err := dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		http.Error(w, "smoke-test: enqueue failed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	deadline := time.Now().Add(config.Cfg.SmokeTestTimeout)
	processed := false
	for time.Now().Before(deadline) {
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, testSlug).Scan(&count)
		if count > 0 {
			processed = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !processed {
		dbpkg.DB.Exec(`DELETE FROM write_jobs WHERE article_json LIKE ? AND status='pending'`, "%\"slug\":\""+testSlug+"\"%")
		http.Error(w, fmt.Sprintf("smoke-test: worker timeout (%s)", config.Cfg.SmokeTestTimeout), http.StatusServiceUnavailable)
		return
	}
	dbpkg.DB.Exec(`DELETE FROM articles WHERE slug=?`, testSlug)
	dbpkg.DB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	os.Remove(filepath.Join(config.Cfg.CacheDir, "posts", testSlug+".html"))
	if a.meiliCB != nil {
		go a.meiliDo("DELETE", "/indexes/articles/documents/"+testID, nil)
	}
	logging.LogInfo("smoke-test", fmt.Sprintf("PASS slug=%s", testSlug))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "OK")
}

func (a *App) handleAdminADR(w http.ResponseWriter, r *http.Request) {
	adrDir := filepath.Join(config.EnvOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs"), "adr")
	entries, err := os.ReadDir(adrDir)
	if err != nil {
		writeAPIError(w, r, 404, "adr_dir_not_found", "ADR directory not found", "https://docs.vayupress.com/governance/adrs")
		return
	}
	type adrEntry struct {
		Filename string `json:"filename"`
	}
	var adrs []adrEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			adrs = append(adrs, adrEntry{e.Name()})
		}
	}
	if adrs == nil {
		adrs = []adrEntry{}
	}
	writeJSON(w, r, 200, map[string]interface{}{"adrs": adrs, "total": len(adrs)})
}

// =============================================================================
// Benchmark handlers
// =============================================================================

type benchmarkResult struct {
	RunAt                                          time.Time `json:"run_at"`
	ArticlesWritten, ReadRequests, ReadConcurrency int
	ReadP50, ReadP95, ReadP99, ReadMax             int64
	ReadMean, ReadRPS                              float64
	P95Pass, P99Pass                               bool
	Overall, Notes                                 string
}

func (a *App) handleHealthBenchmarks(w http.ResponseWriter, r *http.Request) {
	a.lastBenchmarkMu.Lock()
	result := a.lastBenchmark
	a.lastBenchmarkMu.Unlock()
	if result == nil {
		writeAPIError(w, r, 404, "no_benchmark", "no benchmark run yet; POST /admin/benchmark", "https://docs.vayupress.com/operations/benchmarks")
		return
	}
	writeJSON(w, r, 200, result)
}

func (a *App) handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	if !atomic.CompareAndSwapInt32(&a.benchmarkRunning, 0, 1) {
		writeAPIError(w, r, 409, "benchmark_running", "benchmark already in progress", "https://docs.vayupress.com/operations/benchmarks")
		return
	}
	defer atomic.StoreInt32(&a.benchmarkRunning, 0)
	articleCount := 50
	readConcurrency := 20
	totalRequests := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("articles")); err == nil && v > 0 && v <= 500 {
		articleCount = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("readers")); err == nil && v > 0 && v <= 100 {
		readConcurrency = v
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("requests")); err == nil && v > 0 && v <= 2000 {
		totalRequests = v
	}
	baseSlug := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	var writtenSlugs []string
	var writeMu sync.Mutex
	for i := 0; i < articleCount; i++ {
		slug := fmt.Sprintf("%s-%04d", baseSlug, i)
		a := dbpkg.Article{ID: newUUID(), Title: fmt.Sprintf("Bench %d", i), Slug: slug, Content: fmt.Sprintf("<p>%s</p>", strings.Repeat("Benchmark content. ", 200)), Tags: []string{"benchmark"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		payload, _ := json.Marshal(a)
		if _, err := dbpkg.WDB.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err == nil {
			writeMu.Lock()
			writtenSlugs = append(writtenSlugs, slug)
			writeMu.Unlock()
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`, baseSlug+"%").Scan(&count)
		if count >= len(writtenSlugs) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	var actualWritten int
	dbpkg.DB.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`, baseSlug+"%").Scan(&actualWritten)
	var (
		readHistogram metrics.Histogram
		readErrors    int64
		reqCh         = make(chan string, totalRequests)
		readWg        sync.WaitGroup
	)
	for _, slug := range writtenSlugs {
		reqCh <- slug
	}
	close(reqCh)
	readStart := time.Now()
	readClient := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < readConcurrency; i++ {
		readWg.Add(1)
		go func() {
			defer readWg.Done()
			for slug := range reqCh {
				start := time.Now()
				resp, err := readClient.Get(fmt.Sprintf("http://localhost:%s/%s", config.Cfg.Port, slug))
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == 200 {
					readHistogram.Record(time.Since(start))
				} else {
					atomic.AddInt64(&readErrors, 1)
				}
			}
		}()
	}
	readWg.Wait()
	readDuration := time.Since(readStart)
	_, _, _, readMaxMs := readHistogram.Snapshot()
	p95 := readHistogram.Percentile(95)
	p99 := readHistogram.Percentile(99)
	rps := float64(totalRequests) / readDuration.Seconds()
	go func() {
		for _, slug := range writtenSlugs {
			dbpkg.WDB.Exec(`DELETE FROM articles WHERE slug=?`, slug)
			os.Remove(filepath.Join(config.Cfg.CacheDir, "posts", slug+".html"))
		}
	}()
	p95Pass := p95 <= 200
	writeP99 := metrics.QueueJobLatency.Percentile(99)
	p99Pass := writeP99 <= 1000
	overall := "PASS"
	var notes []string
	if !p95Pass {
		overall = "FAIL"
		notes = append(notes, fmt.Sprintf("p95 %dms > 200ms", p95))
	}
	if !p99Pass {
		overall = "FAIL"
		notes = append(notes, fmt.Sprintf("p99 write %dms > 1000ms", writeP99))
	}
	if readErrors > int64(totalRequests/10) {
		overall = "FAIL"
		notes = append(notes, fmt.Sprintf("%d read errors", readErrors))
	}
	if overall == "PASS" && (p95 > 100 || writeP99 > 500) {
		overall = "WARN"
		notes = append(notes, "approaching limits")
	}
	result := &benchmarkResult{RunAt: time.Now().UTC(), ArticlesWritten: actualWritten, ReadRequests: totalRequests, ReadConcurrency: readConcurrency, ReadP50: readHistogram.Percentile(50), ReadP95: p95, ReadP99: p99, ReadMean: readHistogram.Mean(), ReadMax: readMaxMs, ReadRPS: rps, P95Pass: p95Pass, P99Pass: p99Pass, Overall: overall, Notes: strings.Join(notes, "; ")}
	a.lastBenchmarkMu.Lock()
	a.lastBenchmark = result
	a.lastBenchmarkMu.Unlock()
	logging.LogJSON(logging.LogFields{Level: "info", Component: "benchmark", Msg: fmt.Sprintf("result: %s | p95=%dms p99=%dms rps=%.0f", overall, p95, p99, rps)})
	writeJSON(w, r, 200, result)
}

// =============================================================================
// ADR document writing
// =============================================================================

func writeADRs(docsDir string) {
	adrDir := filepath.Join(docsDir, "adr")
	if err := os.MkdirAll(adrDir, 0755); err != nil {
		return
	}
	now := time.Now().Format("2006-01-02")
	adrs := map[string]string{
		"ADR-0032-plugin-pool-concurrency-hardening.md":     "# ADR-0032: Plugin Pool Concurrency Hardening\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 plugin pool had goroutine leak risk on shutdown.\n\n## Decision\npluginCtx/pluginCancel + workerPluginWg + per-goroutine recover().\n",
		"ADR-0033-wal-adaptive-checkpoint.md":               "# ADR-0033: WAL Adaptive Checkpoint Strategy\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nAdaptive WAL checkpoint based on WAL_SIZE_THRESHOLD_MB.\n",
		"ADR-0034-migration-checksum-drift-verification.md": "# ADR-0034: Migration Checksum Drift Verification\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nverifyMigrationChecksums() called at startup.\n",
		"ADR-0035-dead-letter-replay-safety.md":             "# ADR-0035: Dead-Letter Queue Replay Safety Controls\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nReplay limited to REPLAY_BATCH_LIMIT; quarantine after MAX_REPLAY_COUNT.\n",
		"ADR-0036-csp-nonce-template-helpers.md":            "# ADR-0036: CSP Nonce Centralized Template Helpers\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nrender.CSPNonce(r) canonical nonce accessor.\n",
		"ADR-0037-pprof-explicit-handler-hardening.md":      "# ADR-0037: Pprof Explicit Handler Registration\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nExplicit pprofMux; rate-limited; no DefaultServeMux.\n",
		"ADR-0038-vacuum-rate-limiting.md":                  "# ADR-0038: VACUUM Rate Limiting\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nCooldown + write-threshold guard.\n",
		"ADR-0039-deploy-sourced-components.md":             "# ADR-0039: Deploy Script Sourced Components\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0040-config-versioning.md":                     "# ADR-0040: Config Versioning\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0041-structured-health-contracts.md":           "# ADR-0041: Structured Health Contracts\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0042-backup-restore-automation.md":             "# ADR-0042: Backup Restore Automation\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0043-integration-test-failure-modes.md":        "# ADR-0043: Integration Test Failure Mode Coverage\n\n**Status**: Accepted\n**Date**: " + now + "\n",
		"ADR-0045-internal-package-decomposition.md":        "# ADR-0045: Internal Package Decomposition (P14)\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nSplit main.go into internal/* packages: logging, config, metrics, db, auth, render, queue, health.\n",
	}
	for filename, content := range adrs {
		path := filepath.Join(adrDir, filename)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			logging.LogError("adr", "write failed: "+filename, err.Error())
		} else {
			logging.LogInfo("adr", "written: "+filename)
		}
	}
}

// =============================================================================
// Admin dashboard
// =============================================================================

func (a *App) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	snap := a.getAdminSnapshot()
	pluginPanics := atomic.LoadInt64(&metrics.MetricPluginPanics)
	failedClass := "stat-ok"
	if snap.FailedJobs > 0 {
		failedClass = "stat-err"
	}
	storageClass := "stat-ok"
	if snap.StoragePct >= 90 {
		storageClass = "stat-err"
	} else if snap.StoragePct >= 75 {
		storageClass = "stat-warn"
	}
	panicClass := "stat-ok"
	if pluginPanics > 0 {
		panicClass = "stat-warn"
	}
	threshClass := func(ok bool) string {
		if ok {
			return "thresh-ok"
		}
		return "thresh-fail"
	}
	threshLabel := func(ok bool) string {
		if ok {
			return "✓ OK"
		}
		return "✗ FAIL"
	}
	httpOK := snap.HTTPP95 <= 200
	writeOK := snap.WriteP99 <= 1000
	renderOK := snap.RenderP99 <= 500
	cacheOK := snap.CacheHitRatio >= 0.80

	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")

	nonce := render.CSPNonce(r)

	maintenanceBanner := ""
	if config.Cfg.MaintenanceMode {
		maintenanceBanner = `<div style="background:var(--warn);color:#000;padding:8px 16px;font-size:12px;font-weight:600;text-align:center">⚠ MAINTENANCE MODE ACTIVE — write queue paused</div>`
	}

	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>VayuPress Admin — %s</title><meta name="robots" content="noindex, nofollow">
%s%s</head><body>%s
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="app-shell">
<header class="topbar" role="banner">
  <a href="/admin" class="topbar-brand"><span aria-hidden="true">⚡</span><span>VayuPress</span><span class="topbar-domain">%s</span></a>
  <nav class="topbar-actions">
    <span style="font-size:11px;color:var(--muted);font-family:var(--mono)">⟳ %ds ago</span>
    <button class="kbd-hint" id="shortcut-help-btn" aria-haspopup="dialog">? shortcuts</button>
  </nav>
</header>
<main id="main-content">
<h2 class="section-title">Overview</h2>
<div class="stat-grid">
  <div class="stat-card"><div class="stat-val">%d</div><div class="stat-lbl">Articles</div></div>
  <div class="stat-card"><div class="stat-val">%d</div><div class="stat-lbl">Queue Pending</div><div class="stat-sub">%d completed</div></div>
  <div class="stat-card"><div class="stat-val %s">%d</div><div class="stat-lbl">Queue Failed</div></div>
  <div class="stat-card"><div class="stat-val">%.0fs</div><div class="stat-lbl">Uptime</div></div>
  <div class="stat-card"><div class="stat-val %s">%s</div><div class="stat-lbl">Storage Used</div>
    <div class="storage-bar" role="progressbar" aria-valuenow="%.0f" aria-valuemin="0" aria-valuemax="100"><div class="storage-fill" style="width:%.0f%%"></div></div>
  </div>
  <div class="stat-card"><div class="stat-val %s">%d</div><div class="stat-lbl">Plugin Panics</div><div class="stat-sub">%.1f%% cache hit</div></div>
</div>
<h2 class="section-title">Performance Thresholds</h2>
<div class="thresh-grid">
  <div class="thresh-item"><span class="thresh-name">HTTP p95</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 200ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Write p99</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 1000ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Render p99</span><span><span class="thresh-val">%dms</span> <span class="%s">%s</span> <span class="thresh-name">/ 500ms</span></span></div>
  <div class="thresh-item"><span class="thresh-name">Cache hit</span><span><span class="thresh-val">%.0f%%</span> <span class="%s">%s</span> <span class="thresh-name">/ 80%%</span></span></div>
</div>
<h2 class="section-title">Quick Actions</h2>
<div id="action-msg" role="status" aria-live="polite" class="action-msg"></div>
<div class="action-row">
  <button class="btn" id="btn-smoke">Smoke test</button>
  <button class="btn" id="btn-purge">Purge cache</button>
  <button class="btn" id="btn-bench">Benchmark</button>
  <a href="/api/v1/stats" class="btn" target="_blank" rel="noopener">Stats JSON</a>
  <a href="/metrics" class="btn" target="_blank" rel="noopener">Metrics</a>
  <a href="/admin/adr" class="btn" target="_blank" rel="noopener">ADRs</a>
</div>
<h2 class="section-title">Recent Articles</h2>
<table class="data-table"><thead><tr><th>Title</th><th>Slug</th><th>Published</th></tr></thead><tbody>`,
		config.Cfg.Domain,
		render.AdminCSSLink(), render.HighContrastCSSLink(),
		template.HTML(maintenanceBanner),
		config.Cfg.Domain, int(time.Since(snap.SnapshotAt).Seconds()),
		snap.TotalArticles, snap.PendingJobs, snap.CompletedJobs,
		failedClass, snap.FailedJobs, snap.UptimeSeconds,
		storageClass, dbpkg.FormatBytes(snap.StorageBytes), snap.StoragePct, snap.StoragePct,
		panicClass, pluginPanics, snap.CacheHitRatio*100,
		snap.HTTPP95, threshClass(httpOK), threshLabel(httpOK),
		snap.WriteP99, threshClass(writeOK), threshLabel(writeOK),
		snap.RenderP99, threshClass(renderOK), threshLabel(renderOK),
		snap.CacheHitRatio*100, threshClass(cacheOK), threshLabel(cacheOK),
	)

	if len(snap.RecentArticles) == 0 {
		fmt.Fprint(w, `<tr><td colspan="3" style="color:var(--muted);text-align:center;padding:2rem">No articles yet.</td></tr>`)
	} else {
		for _, row := range snap.RecentArticles {
			fmt.Fprintf(w, `<tr><td>%s</td><td><a href="/%s" target="_blank">%s</a></td><td><time>%s</time></td></tr>`,
				row.Title, row.Slug, row.Slug, row.CreatedAt.Format("2 Jan 2006"))
		}
	}

	fmt.Fprintf(w, `</tbody></table>
<h2 class="section-title">P8 Health Contracts</h2>
<nav class="links-row">
  <a href="/health/dependencies" target="_blank">Dependencies</a>
  <a href="/health/search" target="_blank">Search</a>
  <a href="/health/queue" target="_blank">Queue</a>
  <a href="/health/workers" target="_blank">Workers</a>
  <a href="/health/storage" target="_blank">Storage</a>
  <a href="/health/migrations" target="_blank">Migrations</a>
  <a href="/admin/backup/validate" target="_blank">Backup Validate</a>
  <a href="/health/benchmarks" target="_blank">Benchmarks</a>
</nav>
<footer class="admin-footer">VayuPress %s &middot; Constitution v6.0 &middot; P1–P14 compliant &middot; Config v%s &middot; Snapshot: %s</footer>
</main></div>
<div class="modal-backdrop" id="shortcut-modal" role="dialog" aria-modal="true" aria-labelledby="modal-title" tabindex="-1">
  <div class="modal">
    <div class="modal-title"><span id="modal-title">Keyboard Shortcuts</span><button class="modal-close" id="modal-close-btn" aria-label="Close">✕</button></div>
    <ul class="shortcut-list">
      <li class="shortcut-item"><span>This help</span><kbd>?</kbd></li>
      <li class="shortcut-item"><span>Smoke test</span><kbd>s</kbd></li>
      <li class="shortcut-item"><span>Benchmark</span><kbd>b</kbd></li>
      <li class="shortcut-item"><span>Reload</span><kbd>r</kbd></li>
      <li class="shortcut-item"><span>Close dialog</span><kbd>Esc</kbd></li>
    </ul>
  </div>
</div>
<script nonce="%s">
(function(){
  'use strict';
  var modal=document.getElementById('shortcut-modal'),
      closeBtn=document.getElementById('modal-close-btn'),
      actionMsg=document.getElementById('action-msg');
  function csrf(){var m=document.cookie.split('; ').find(function(r){return r.startsWith('vp_csrf=');});return m?m.split('=')[1]:'';}
  function post(url){return fetch(url,{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf()}});}
  function openModal(){modal.classList.add('open');document.body.style.overflow='hidden';closeBtn.focus();}
  function closeModal(){modal.classList.remove('open');document.body.style.overflow='';}
  function showMsg(text,isErr){actionMsg.textContent=text;actionMsg.style.borderColor=isErr?'var(--error)':'var(--success)';actionMsg.classList.add('visible');setTimeout(function(){actionMsg.classList.remove('visible');},5000);}
  function runSmoke(){showMsg('Running smoke test…',false);fetch('/smoke-test').then(function(r){return r.text();}).then(function(t){showMsg('Smoke test: '+t,t.trim()!=='OK');}).catch(function(e){showMsg('Error: '+e,true);});}
  function runPurge(){showMsg('Purging cache…',false);post('/admin/cache-purge').then(function(r){return r.json();}).then(function(d){showMsg('Cache purge: '+(d.message||'done'),false);}).catch(function(e){showMsg('Error: '+e,true);});}
  function runBench(){showMsg('Benchmark started (up to 60s)…',false);post('/admin/benchmark').then(function(r){return r.json();}).then(function(d){showMsg('Benchmark: '+(d.overall||'done')+' · p95='+d.read_p95_ms+'ms',d.overall==='FAIL');}).catch(function(e){showMsg('Error: '+e,true);});}
  document.getElementById('btn-smoke').addEventListener('click',runSmoke);
  document.getElementById('btn-purge').addEventListener('click',runPurge);
  document.getElementById('btn-bench').addEventListener('click',runBench);
  document.getElementById('shortcut-help-btn').addEventListener('click',openModal);
  closeBtn.addEventListener('click',closeModal);
  modal.addEventListener('click',function(e){if(e.target===modal)closeModal();});
  document.addEventListener('keydown',function(e){
    var tag=document.activeElement&&document.activeElement.tagName;
    if(tag==='INPUT'||tag==='TEXTAREA'||tag==='SELECT')return;
    if(e.key==='Escape'){if(modal.classList.contains('open'))closeModal();return;}
    if(e.key==='?'){e.preventDefault();openModal();return;}
    if(e.key==='s'&&!e.ctrlKey&&!e.metaKey){runSmoke();return;}
    if(e.key==='b'&&!e.ctrlKey&&!e.metaKey){runBench();return;}
    if(e.key==='r'&&!e.ctrlKey&&!e.metaKey){location.reload();return;}
  });
})();
</script></body></html>`,
		Version, config.ConfigVersion, snap.SnapshotAt.UTC().Format("15:04:05 UTC"),
		nonce,
	)
}

// =============================================================================
// main
// =============================================================================

func main() {
	log.SetFlags(0)
	logging.LogInfo("main", fmt.Sprintf("VayuPress v%s starting — P1–P16 active", Version))
	config.Load()
	logging.LogInfo("main", fmt.Sprintf("domain=%s port=%s workers=%d config_version=%s maintenance=%v",
		config.Cfg.Domain, config.Cfg.Port, config.Cfg.WorkerCount, config.ConfigVersion, config.Cfg.MaintenanceMode))

	// Initialise App — the single owner of all mutable runtime state (ADR-0046).
	a := &App{
		policy:         bluemonday.UGCPolicy(),
		outboundClient: &http.Client{Timeout: 5 * time.Second, Transport: ssrfSafeTransport()},
		pluginRegistry: plugins.NewRegistry(),
	}
	a.pluginManager = plugins.New(a.pluginRegistry)

	auth.InitCSRFSecret()
	initPprofMux()
	auth.StartBucketSweeper(context.Background())

	staticDir := config.EnvOr("STATIC_DIR", "/var/www/vayupress/static")
	render.WriteCSSAssets(staticDir)

	docsDir := config.EnvOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs")
	os.MkdirAll(docsDir, 0755)
	writeADRs(docsDir)

	if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
		a.pluginManager.Start(plugins.DefaultPoolSize, plugins.DefaultQueueDepth)
	}

	if err := dbpkg.Init(); err != nil {
		logging.LogError("main", "DB init failed", err.Error())
		os.Exit(1)
	}
	logging.LogInfo("main", "database ready — WAL adaptive + migrations + checksum drift verified (ADR-0033/0034)")

	if n, err := dbpkg.DB.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing'`); err == nil {
		if rows, _ := n.RowsAffected(); rows > 0 {
			logging.LogInfo("main", fmt.Sprintf("recovered %d stale processing jobs", rows))
		}
	}

	dbpkg.InitStorageCachedBytes()
	dbpkg.StartWALCheckpointGoroutine(queue.DoneCh)
	dbpkg.StartStuckJobReaper(queue.DoneCh)
	a.startMetricsSnapshotCollector()

	// Wire dependency injections into queue package
	queue.RenderFn = render.RenderArticle
	queue.SetCacheWriteFn(func(relPath, content string) {
		render.CacheWrite(relPath, content) //nolint:errcheck
	})
	queue.FireHookFn = func(event string, payload map[string]interface{}) {
		a.FireHook(event, payload)
		slug, _ := payload["slug"].(string)
		id, _ := payload["id"].(string)
		switch event {
		case "article.create", "article.update":
			go func(s string) {
				var art dbpkg.Article
				var tagsStr string
				if dbpkg.DB.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, s).
					Scan(&art.ID, &art.Title, &art.Slug, &art.Content, &tagsStr, &art.CreatedAt, &art.UpdatedAt) == nil {
					art.Tags = splitTags(tagsStr)
					a.indexArticle(art)
				}
				render.CachePurge(s, nil, generateSitemap, generateRSS, generateRobots)
				go a.purgeCloudflare(s)
				go a.pingIndexNow(s)
			}(slug)
		case "article.delete":
			go a.meiliDo("DELETE", "/indexes/articles/documents/"+id, nil)
			go a.purgeCloudflare(slug)
		}
	}

	// Wire health package injections
	health.Version = Version
	health.ConfigVersion = config.ConfigVersion
	health.BootTime = bootTime
	health.MeiliDoFn = a.meiliDo
	health.WriteJSON = writeJSON
	health.WriteAPIError = writeAPIError

	// Wire render package version
	render.Version = Version

	// Meilisearch startup
	for i := 0; i < 12; i++ {
		if err := a.meiliDo("GET", "/health", nil); err == nil {
			logging.LogInfo("main", "Meilisearch ready")
			break
		}
		if i == 11 {
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "main", Msg: "Meilisearch unavailable — SQLite search fallback active"})
		}
		time.Sleep(5 * time.Second)
	}
	a.configureMeilisearch()
	a.initMeilisearchCB()

	go func() {
		logging.LogInfo("cache-warm", "starting...")
		render.WarmCache(splitTags)
		generateSitemap()
		generateRSS()
		generateRobots()
		logging.LogInfo("cache-warm", "complete")
	}()

	queue.StartWorkerPool(&metrics.WorkerWg)
	logging.LogInfo("main", fmt.Sprintf("started %d write workers (maintenance_mode=%v)", config.Cfg.WorkerCount, config.Cfg.MaintenanceMode))

	logging.LogInfo("main", fmt.Sprintf("startup complete in %dms", time.Since(bootTime).Milliseconds()))

	r := chi.NewRouter()
	r.Use(
		requestIDMiddleware,
		middleware.RealIP,
		structuredLoggerMiddleware,
		middleware.Recoverer,
		middleware.Timeout(30*time.Second),
		securityHeadersMiddleware,
	)
	r.Use(cors.New(cors.Options{
		AllowedOrigins:   []string{"https://" + config.Cfg.Domain},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "X-API-Key", "Authorization", "X-Request-ID", "X-CSRF-Token"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
	}).Handler)

	// Public health endpoints
	r.Get("/health", health.HandleHealthLiveness)
	r.Get("/health/live", health.HandleHealthLiveness)
	r.Get("/health/ready", health.HandleHealthReady)
	r.Get("/health/db", health.HandleHealthDB)
	r.Get("/health/meilisearch", health.HandleHealthMeilisearch)
	r.Get("/health/workers", health.HandleHealthWorkers)
	r.Get("/health/storage", health.HandleHealthStorage)
	r.Get("/health/benchmarks", a.handleHealthBenchmarks)
	r.Get("/health/migrations", health.HandleHealthMigrations)
	r.Get("/health/ethics", health.HandleHealthEthics)
	r.Get("/health/dependencies", health.HandleHealthDependencies)
	r.Get("/health/search", health.HandleHealthSearch)
	r.Get("/health/queue", health.HandleHealthQueue)

	// Static files + feeds
	r.Get("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, "sitemap.xml"))
	})
	r.Get("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, "feed.xml"))
	})
	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(config.Cfg.CacheDir, "robots.txt"))
	})
	r.Get("/static/css/{file}", func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "file")
		if !map[string]bool{"article.css": true, "admin.css": true, "high-contrast.css": true}[file] {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(staticDir, "css", file))
	})

	// Public API
	r.Get("/api/v1/articles", a.handleListArticles)
	r.Get("/api/v1/articles/{slug}", a.handleGetArticle)
	r.Get("/api/v1/search", a.handleSearch)
	r.Get("/api/v1/tags", a.handleListTags)
	r.Get("/api/v1/stats", a.handleStats)
	r.Get("/metrics", a.handleMetrics)
	r.Get("/smoke-test", a.handleSmokeTest)

	// Protected admin + write API
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAPIKey, auth.RateLimitMiddleware)

		r.Post("/api/v1/articles", a.handleCreateArticle)
		r.Post("/api/v1/articles/bulk", a.handleBulkCreateArticles)
		r.Put("/api/v1/articles/{slug}", a.handleUpdateArticle)
		r.Delete("/api/v1/articles/{slug}", a.handleDeleteArticle)
		r.Get("/api/v1/queue", a.handleQueueStatus)
		r.Post("/api/v1/queue/replay", a.handleQueueReplay)

		r.Get("/admin", a.handleAdminDashboard)
		r.Get("/admin/adr", a.handleAdminADR)
		r.Get("/admin/backup/validate", a.handleAdminBackupValidate)

		r.With(auth.CSRFTokenMiddleware).Post("/admin/benchmark", a.handleRunBenchmark)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/cache-purge", a.handleAdminCachePurge)
		r.With(auth.CSRFTokenMiddleware).Post("/admin/vacuum", a.handleAdminVacuum)

		r.HandleFunc("/debug/pprof/", a.pprofHandler)
		r.HandleFunc("/debug/pprof/cmdline", a.pprofHandler)
		r.HandleFunc("/debug/pprof/profile", a.pprofHandler)
		r.HandleFunc("/debug/pprof/symbol", a.pprofHandler)
		r.HandleFunc("/debug/pprof/trace", a.pprofHandler)
		r.HandleFunc("/debug/pprof/*", a.pprofHandler)
	})

	r.Get("/{slug}", a.handleArticlePage)

	srv := &http.Server{
		Addr:         ":" + config.Cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logging.LogInfo("main", fmt.Sprintf("received %v — P14 graceful shutdown", sig))

		// Phase 1: stop ingress
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer httpCancel()
		if err := srv.Shutdown(httpCtx); err != nil {
			logging.LogError("main", "HTTP shutdown", err.Error())
		}
		logging.LogInfo("main", "phase 1 complete — ingress stopped")

		// Phase 2: drain write queue (45s)
		close(queue.DoneCh)
		drainDone := make(chan struct{})
		go func() { metrics.WorkerWg.Wait(); close(drainDone) }()
		select {
		case <-drainDone:
			logging.LogInfo("main", "phase 2 complete — write queue drained")
		case <-time.After(45 * time.Second):
			logging.LogJSON(logging.LogFields{Level: "warn", Component: "main", Msg: "phase 2 timeout (45s) — in-flight jobs retried on next startup"})
		}

		// Phase 3: stop plugin pool
		if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
			a.pluginManager.Shutdown()
			logging.LogInfo("main", "phase 3 complete — plugin pool stopped")
		}

		// Phase 4: WAL checkpoint before close
		if dbpkg.DB != nil {
			if _, err := dbpkg.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				logging.LogError("main", "WAL checkpoint on shutdown", err.Error())
			} else {
				logging.LogInfo("main", "phase 4 complete — WAL checkpointed")
			}
		}

		// Phase 5: flush final metrics snapshot
		a.collectAdminMetrics()
		logging.LogInfo("main", "phase 5 complete — metrics flushed")

		// Phase 6: close database
		if dbpkg.DB != nil {
			if err := dbpkg.DB.Close(); err != nil {
				logging.LogError("main", "DB close", err.Error())
			} else {
				logging.LogInfo("main", "phase 6 complete — database closed")
			}
		}

		logging.LogInfo("main", "shutdown complete — goodbye")
		os.Exit(0)
	}()

	logging.LogInfo("main", fmt.Sprintf("listening on :%s (v%s — P1–P14 active)", config.Cfg.Port, Version))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logging.LogError("main", "ListenAndServe error", err.Error())
		os.Exit(1)
	}
}

// suppress unused import for verifyMagicNumber (kept for media upload endpoints)
var _ = verifyMagicNumber
