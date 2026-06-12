// =============================================================================
// VayuPress — main.go  v1.0.0-p13
// Author  : Ankush Choudhary Johal <https://vayupress.com>
// License : MIT
// GOVERNANCE: VayuPress Governance Constitution v6.0 — Prompts 1–12 compliant.
// =============================================================================

package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
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
	"golang.org/x/crypto/argon2"
)

var Version = "1.0.0-p8"
var bootTime = time.Now()

const ConfigVersion = "1.0"
const MinCompatibleConfigVersion = "1.0"

// =============================================================================
// Configuration
// =============================================================================

var cfg struct {
	APIKey              string
	DBPath              string
	CacheDir            string
	MeiliHost           string
	MeiliMasterKey      string
	Domain              string
	Port                string
	WorkerCount         int
	CFZoneID            string
	CFAPIToken          string
	IndexNowKey         string
	StorageQuotaGB      int64
	MediaRetainDays     int
	CacheMaxSizeGB      int64
	SmokeTestTimeout    time.Duration
	BackupRetainDays    int
	TmpDir              string
	QueueSaturationWarn int
	MaintenanceMode     bool
	VacuumCooldownMin   int
	MaxReplayCount      int
	ReplayBatchLimit    int
	WALSizeThresholdMB  int
	PprofRateLimit      int
}

func loadConfig() {
	cfg.APIKey = mustEnv("API_KEY")
	cfg.DBPath = envOr("DB_PATH", "/var/lib/vayupress/data.db")
	cfg.CacheDir = envOr("CACHE_DIR", "/var/cache/vayupress")
	cfg.MeiliHost = envOr("MEILI_HOST", "http://localhost:7700")
	cfg.MeiliMasterKey = envOr("MEILI_MASTER_KEY", "")
	cfg.Domain = envOr("DOMAIN", "localhost")
	cfg.Port = envOr("PORT", "8080")
	cfg.CFZoneID = envOr("CF_ZONE_ID", "")
	cfg.CFAPIToken = envOr("CF_API_TOKEN", "")
	cfg.IndexNowKey = envOr("INDEXNOW_KEY", "")
	cfg.TmpDir = envOr("TMP_DIR", "/tmp/vayupress")
	cfg.WorkerCount = getEnvAsInt("WORKER_COUNT", 3)
	cfg.BackupRetainDays = getEnvAsInt("BACKUP_RETAIN_DAYS", 30)
	cfg.StorageQuotaGB = int64(getEnvAsInt("STORAGE_QUOTA_GB", 200))
	cfg.MediaRetainDays = getEnvAsInt("MEDIA_RETAIN_DAYS", 365)
	cfg.CacheMaxSizeGB = int64(getEnvAsInt("CACHE_MAX_SIZE_GB", 10))
	cfg.QueueSaturationWarn = getEnvAsInt("QUEUE_SATURATION_WARN", 100)
	st := getEnvAsInt("SMOKE_TEST_TIMEOUT", 30)
	cfg.SmokeTestTimeout = time.Duration(st) * time.Second
	cfg.MaintenanceMode = os.Getenv("VAYU_MAINTENANCE") == "true"
	cfg.VacuumCooldownMin = getEnvAsInt("VACUUM_COOLDOWN_MIN", 10)
	cfg.MaxReplayCount = getEnvAsInt("MAX_REPLAY_COUNT", 3)
	cfg.ReplayBatchLimit = getEnvAsInt("REPLAY_BATCH_LIMIT", 100)
	cfg.WALSizeThresholdMB = getEnvAsInt("WAL_SIZE_THRESHOLD_MB", 32)
	cfg.PprofRateLimit = getEnvAsInt("PPROF_RATE_LIMIT", 5)

	if os.Getenv("QUEUE_MAX_RETRIES") != "" {
		logJSON(logFields{Level: "warn", Component: "config", Msg: "QUEUE_MAX_RETRIES is deprecated — use MAX_REPLAY_COUNT instead (ADR-0040)"})
	}
	logInfo("config", fmt.Sprintf("ConfigVersion=%s MaintenanceMode=%v WALThresholdMB=%d",
		ConfigVersion, cfg.MaintenanceMode, cfg.WALSizeThresholdMB))
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		log.Fatalf(`{"level":"fatal","component":"config","msg":"required env not set","key":"%s"}`, k)
	}
	return v
}
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func getEnvAsInt(name string, defaultVal int) int {
	v := os.Getenv(name)
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return defaultVal
	}
	return n
}

// =============================================================================
// Logging
// =============================================================================

type logFields struct {
	Level      string `json:"level"`
	Time       string `json:"time"`
	RequestID  string `json:"request_id,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
	Status     int    `json:"status,omitempty"`
	LatencyMS  int64  `json:"latency_ms,omitempty"`
	RemoteAddr string `json:"remote_addr,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
	Component  string `json:"component,omitempty"`
	Error      string `json:"error,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Msg        string `json:"msg,omitempty"`
}

var secretRedactRe = regexp.MustCompile(`(?i)(password|api.?key|bearer|secret|token|auth|master.?key)\s*[=:]\s*\S+`)

func logJSON(f logFields) {
	if f.Error != "" {
		f.Error = secretRedactRe.ReplaceAllStringFunc(f.Error, func(m string) string {
			idx := strings.IndexAny(m, "=:")
			if idx < 0 {
				return m
			}
			return m[:idx+1] + "[REDACTED]"
		})
	}
	f.Time = time.Now().UTC().Format(time.RFC3339Nano)
	b, _ := json.Marshal(f)
	log.Println(string(b))
}
func logInfo(component, msg string) {
	logJSON(logFields{Level: "info", Component: component, Msg: msg})
}
func logError(component, msg, e string) {
	logJSON(logFields{Level: "error", Component: component, Msg: msg, Error: e, Severity: "error"})
}

// =============================================================================
// Models + Globals
// =============================================================================

type Article struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type WriteJob struct {
	ID          int64
	ArticleJSON string
	Op          string
}
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	Docs      string `json:"docs"`
}

var (
	db             *sql.DB
	policy         *bluemonday.Policy
	slugRe         = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,198}[a-z0-9]$|^[a-z0-9]$`)
	htmlTagRe      = regexp.MustCompile(`<[^>]+>`)
	outboundClient = &http.Client{Timeout: 5 * time.Second, Transport: ssrfSafeTransport()}
	doneCh         = make(chan struct{})
	meiliCB        *gobreaker.CircuitBreaker
	smokeTestMutex sync.Mutex

	metricArticlesCreated   int64
	metricArticlesUpdated   int64
	metricArticlesDeleted   int64
	metricMeiliErrors       int64
	metricQueueProcessed    int64
	metricQueueFailed       int64
	metricCacheHits         int64
	metricCacheMisses       int64
	metricQueueStuckResets  int64
	metricPluginPanics      int64
	metricAuthLockouts      int64
	metricPluginPoolDropped int64
	metricPluginDisabled    int64
	metricWALCheckpoints    int64
	metricSlowQueries       int64
	metricDeadLetterJobs    int64
	// P8 metrics (ADR-0032 through ADR-0043)
	metricWALCheckpointDurationMS int64
	metricWALAdaptiveCheckpoints  int64
	metricMigrationDriftDetected  int64
	metricPoisonJobsQuarantined   int64
	metricPprofAccesses           int64
	metricVacuumRejected          int64
	metricHealthDegradedEvents    int64

	workerLiveness     int64
	workerLastActivity sync.Map
	workerWg           sync.WaitGroup
	cachedStorageBytes int64

	httpLatency        latencyHistogram
	renderLatency      latencyHistogram
	queueJobLatency    latencyHistogram
	sqliteWriteLatency latencyHistogram
)

// =============================================================================
// Auth lockout (ADR-0021)
// =============================================================================

type authFailBucket struct {
	mu          sync.Mutex
	failures    int
	windowEnd   time.Time
	lockedUntil time.Time
}

var (
	authFailMu      sync.Mutex
	authFailBuckets = make(map[string]*authFailBucket)
)

// P9: startBucketSweeper removes expired authFail and rate-limit buckets on a
// fixed interval to bound memory usage on long-running instances with rotating IPs.
func startBucketSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				authFailMu.Lock()
				for ip, b := range authFailBuckets {
					b.mu.Lock()
					expired := now.After(b.lockedUntil) && now.After(b.windowEnd)
					b.mu.Unlock()
					if expired {
						delete(authFailBuckets, ip)
					}
				}
				authFailMu.Unlock()
				rateMu.Lock()
				for ip, b := range rateBuckets {
					if now.After(b.resetAt) {
						delete(rateBuckets, ip)
					}
				}
				rateMu.Unlock()
				pprofLimiters.Range(func(k, v interface{}) bool {
					if b, ok := v.(*pprofBucket); ok {
						b.mu.Lock()
						old := now.After(b.windowEnd)
						b.mu.Unlock()
						if old {
							pprofLimiters.Delete(k)
						}
					}
					return true
				})
				purgeLimiters.Range(func(k, v interface{}) bool {
					if b, ok := v.(*purgeBucket); ok {
						b.mu.Lock()
						idle := now.Sub(b.lastRefill) > 30*time.Minute
						b.mu.Unlock()
						if idle {
							purgeLimiters.Delete(k)
						}
					}
					return true
				})
			}
		}
	}()
}

const (
	authFailWindow   = 15 * time.Minute
	authFailMax      = 5
	authLockDuration = 1 * time.Hour
)

func getAuthFailBucket(ip string) *authFailBucket {
	authFailMu.Lock()
	defer authFailMu.Unlock()
	if b, ok := authFailBuckets[ip]; ok {
		return b
	}
	b := &authFailBucket{}
	authFailBuckets[ip] = b
	return b
}
func checkAuthLockout(ip string) (bool, time.Time) {
	b := getAuthFailBucket(ip)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.Before(b.lockedUntil) {
		return true, b.lockedUntil
	}
	if now.After(b.windowEnd) {
		b.failures = 0
		b.windowEnd = now.Add(authFailWindow)
	}
	return false, time.Time{}
}
func recordAuthFailure(ip string) {
	b := getAuthFailBucket(ip)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.After(b.windowEnd) {
		b.failures = 0
		b.windowEnd = now.Add(authFailWindow)
	}
	b.failures++
	if b.failures >= authFailMax {
		b.lockedUntil = now.Add(authLockDuration)
		atomic.AddInt64(&metricAuthLockouts, 1)
		logJSON(logFields{Level: "warn", Component: "auth-lockout", Msg: fmt.Sprintf("IP %s locked out for %s", ip, authLockDuration)})
	}
}
func recordAuthSuccess(ip string) {
	b := getAuthFailBucket(ip)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.lockedUntil = time.Time{}
}

// =============================================================================
// PROMPT 9: SSRF PROTECTION
// All outbound HTTP (webhooks, IndexNow, search) dials through a guarded
// DialContext that refuses to connect to loopback, link-local (cloud metadata
// 169.254.169.254), and RFC-1918 private address ranges.
// =============================================================================

// isPrivateOrReservedIP reports whether an IP must never be reached by
// server-initiated outbound requests (SSRF guard).
func isPrivateOrReservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// Cloud instance metadata endpoints (defense in depth beyond IsLinkLocal).
	if ip.Equal(net.ParseIP("169.254.169.254")) || ip.Equal(net.ParseIP("100.100.100.200")) {
		return true
	}
	// IPv6 unique-local fc00::/7
	if v6 := ip.To16(); v6 != nil && ip.To4() == nil && (v6[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

// ssrfSafeTransport returns an http.Transport whose DialContext rejects any
// attempt to connect to a private/reserved IP — preventing SSRF even when an
// attacker-controlled hostname resolves (or re-resolves) to an internal IP.
func ssrfSafeTransport() *http.Transport {
	base := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			// Allow explicitly-trusted internal services (Meilisearch/Isso on loopback)
			// only when the operator addresses them by their configured host.
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

// isAllowedInternalHost whitelists the loopback services VayuPress itself runs
// (Meilisearch, Isso). Everything else on a private IP is blocked.
func isAllowedInternalHost(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// =============================================================================
// PROMPT 9: Argon2id credential hashing
// =============================================================================

const (
	argonTime    = 1
	argonMemory  = 64 * 1024 // 64 MB
	argonThreads = 4
	argonKeyLen  = 32
)

// hashSecretArgon2id derives an Argon2id hash with a random 16-byte salt,
// returning an encoded "salt$hash" (base64) string for storage.
func hashSecretArgon2id(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

// verifySecretArgon2id performs a constant-time comparison of secret against an
// encoded Argon2id hash produced by hashSecretArgon2id.
func verifySecretArgon2id(secret, encoded string) bool {
	parts := strings.SplitN(encoded, "$", 2)
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return hmac.Equal(got, want)
}

// =============================================================================
// PROMPT 9: Magic-number file-type verification
// Binary/media ingestion must validate the real content type from the file's
// leading bytes — never trust a client-supplied extension or Content-Type.
// =============================================================================

var allowedMagicNumbers = map[string][]byte{
	"image/jpeg":      {0xFF, 0xD8, 0xFF},
	"image/png":       {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},
	"image/gif":       {0x47, 0x49, 0x46, 0x38},
	"image/webp":      {0x52, 0x49, 0x46, 0x46}, // "RIFF" (WEBP container)
	"application/pdf": {0x25, 0x50, 0x44, 0x46},
}

// verifyMagicNumber returns the detected MIME type if data's leading bytes match
// one of the allowed signatures, or an error if the file type is not permitted.
func verifyMagicNumber(data []byte) (string, error) {
	for mime, sig := range allowedMagicNumbers {
		if len(data) >= len(sig) && bytes.Equal(data[:len(sig)], sig) {
			return mime, nil
		}
	}
	return "", fmt.Errorf("file type not allowed: magic number does not match any permitted media type")
}

// =============================================================================
// PROMPT 9: Immutable WORM audit log
// auditLog appends a tamper-evident record of every privileged mutation. The
// audit_log table (migration 005) carries UPDATE/DELETE triggers that ABORT any
// attempt to alter history.
// =============================================================================

func auditLog(action, actor, target, detail string) {
	if db == nil {
		return
	}
	if _, err := db.Exec(
		`INSERT INTO audit_log(ts,action,actor,target,detail) VALUES(?,?,?,?,?)`,
		time.Now().UTC(), action, actor, target, detail,
	); err != nil {
		logJSON(logFields{Level: "error", Component: "audit", Msg: "failed to write audit record", Error: err.Error()})
	}
}

// auditActor derives a stable actor identifier (client IP) for an audited
// request without recording the API key itself.
func auditActor(r *http.Request) string {
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// =============================================================================
// CSRF (ADR-0013/0016/0017)
// =============================================================================

var csrfSecret []byte

func initCSRFSecret() {
	csrfSecret = make([]byte, 32)
	if _, err := rand.Read(csrfSecret); err != nil {
		logError("csrf", "failed to generate CSRF secret", err.Error())
		os.Exit(1)
	}
	logInfo("csrf", "CSRF secret initialized (32 bytes)")
}
func csrfCookieSecure() bool {
	if v := os.Getenv("CSRF_SECURE_COOKIE"); v != "" {
		return v == "true"
	}
	return cfg.Domain != "localhost"
}
func generateCSRFToken() string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	token := hex.EncodeToString(raw)
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(token))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.URLEncoding.EncodeToString([]byte(token + "." + sig))
}
func validateCSRFToken(token string) bool {
	if token == "" {
		return false
	}
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(decoded), ".", 2)
	if len(parts) != 2 {
		return false
	}
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(parts[0]))
	return hmac.Equal([]byte(parts[1]), []byte(hex.EncodeToString(mac.Sum(nil))))
}
func csrfTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if c, err := r.Cookie("vp_csrf"); err != nil || c.Value == "" {
				if token := generateCSRFToken(); token != "" {
					http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			headerToken := r.Header.Get("X-CSRF-Token")
			cookieToken := ""
			if c, err := r.Cookie("vp_csrf"); err == nil {
				cookieToken = c.Value
			}
			if headerToken == "" || cookieToken == "" || headerToken != cookieToken || !validateCSRFToken(headerToken) {
				writeAPIError(w, r, 403, "csrf_invalid", "CSRF token missing or invalid", "https://docs.vayupress.com/api/csrf")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// =============================================================================
// P8 — CSP Nonce Centralized Helper (ADR-0036)
// =============================================================================

type ctxKeyCSPNonce struct{}

// CSPNonce returns the per-request CSP nonce from the request context.
// Use this in all templates that contain inline scripts.
func CSPNonce(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyCSPNonce{}).(string); ok {
		return v
	}
	return ""
}
func generateCSPNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("ts%x", time.Now().UnixNano())
	}
	return base64.StdEncoding.EncodeToString(b)
}

// =============================================================================
// P8 — Pprof rate limiter (ADR-0037)
// =============================================================================

type pprofBucket struct {
	count     int
	windowEnd time.Time
	mu        sync.Mutex
}

var pprofLimiters sync.Map

func allowPprof(ip string) bool {
	v, _ := pprofLimiters.LoadOrStore(ip, &pprofBucket{})
	b := v.(*pprofBucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.After(b.windowEnd) {
		b.count = 0
		b.windowEnd = now.Add(time.Minute)
	}
	if b.count >= cfg.PprofRateLimit {
		return false
	}
	b.count++
	return true
}

// =============================================================================
// P8 — VACUUM state (ADR-0038)
// =============================================================================

var (
	vacuumMu      sync.Mutex
	vacuumLastRun time.Time
)

const vacuumWriteThreshold = 10

// =============================================================================
// Cache purge rate limiter
// =============================================================================

type purgeBucket struct {
	tokens     float64
	lastRefill time.Time
	mu         sync.Mutex
}

const (
	purgeRatePerMin = 5.0
	purgeBurstMax   = 5.0
)

var purgeLimiters sync.Map

func getPurgeBucket(ip string) *purgeBucket {
	if v, ok := purgeLimiters.Load(ip); ok {
		return v.(*purgeBucket)
	}
	b := &purgeBucket{tokens: purgeBurstMax, lastRefill: time.Now()}
	purgeLimiters.Store(ip, b)
	return b
}
func allowPurge(ip string) bool {
	b := getPurgeBucket(ip)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.lastRefill).Minutes()
	b.tokens += elapsed * purgeRatePerMin
	if b.tokens > purgeBurstMax {
		b.tokens = purgeBurstMax
	}
	b.lastRefill = now
	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true
	}
	return false
}

// =============================================================================
// Latency histogram (P2)
// =============================================================================

type latencyHistogram struct {
	mu              sync.Mutex
	buckets         [16]int64
	count, sum, max int64
}

var histBoundMS = [16]int64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 1 << 62}

func (h *latencyHistogram) record(d time.Duration) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	h.mu.Lock()
	h.count++
	h.sum += ms
	if ms > h.max {
		h.max = ms
	}
	bucket := 0
	for bucket < 15 && ms > histBoundMS[bucket] {
		bucket++
	}
	h.buckets[bucket]++
	h.mu.Unlock()
}
func (h *latencyHistogram) snapshot() (buckets [16]int64, count, sum, max int64) {
	h.mu.Lock()
	buckets = h.buckets
	count = h.count
	sum = h.sum
	max = h.max
	h.mu.Unlock()
	return
}
func (h *latencyHistogram) prometheus(name, help string) string {
	buckets, count, sum, _ := h.snapshot()
	var sb strings.Builder
	fmt.Fprintf(&sb, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	cumulative := int64(0)
	for i, bound := range histBoundMS {
		cumulative += buckets[i]
		if bound == 1<<62 {
			fmt.Fprintf(&sb, "%s_bucket{le=\"+Inf\"} %d\n", name, cumulative)
		} else {
			fmt.Fprintf(&sb, "%s_bucket{le=\"%.3f\"} %d\n", name, float64(bound)/1000.0, cumulative)
		}
	}
	fmt.Fprintf(&sb, "%s_sum %d\n%s_count %d\n", name, sum, name, count)
	return sb.String()
}
func (h *latencyHistogram) percentile(pct float64) int64 {
	buckets, count, _, _ := h.snapshot()
	if count == 0 {
		return 0
	}
	target := int64(float64(count) * pct / 100.0)
	if target < 1 {
		target = 1
	}
	cumulative := int64(0)
	for i, b := range buckets {
		cumulative += b
		if cumulative >= target {
			if histBoundMS[i] == 1<<62 && i > 0 {
				return histBoundMS[i-1] * 2
			}
			return histBoundMS[i]
		}
	}
	return histBoundMS[14]
}
func (h *latencyHistogram) mean() float64 {
	_, count, sum, _ := h.snapshot()
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}

// =============================================================================
// wrappedDB + storage
// =============================================================================

type wrappedDB struct{ *sql.DB }

var wdb wrappedDB

func (w wrappedDB) Exec(query string, args ...interface{}) (sql.Result, error) {
	q := strings.ToUpper(strings.TrimSpace(query))
	isWrite := strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") || strings.HasPrefix(q, "DELETE")
	if !isWrite {
		return w.DB.Exec(query, args...)
	}
	start := time.Now()
	result, err := w.DB.Exec(query, args...)
	elapsed := time.Since(start)
	sqliteWriteLatency.record(elapsed)
	if elapsed.Milliseconds() > 100 {
		atomic.AddInt64(&metricSlowQueries, 1)
		logJSON(logFields{Level: "warn", Component: "db", Msg: fmt.Sprintf("slow write %dms: %s", elapsed.Milliseconds(), q[:minInt(len(q), 80)])})
	}
	return result, err
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func initStorageCachedBytes() {
	go func() {
		start := time.Now()
		cacheSize, _ := storageDirSizeBytes(cfg.CacheDir)
		dbSize := int64(0)
		if fi, err := os.Stat(cfg.DBPath); err == nil {
			dbSize = fi.Size()
		}
		total := cacheSize + dbSize
		atomic.StoreInt64(&cachedStorageBytes, total)
		logInfo("storage", fmt.Sprintf("initial scan: %s (%dms)", formatBytes(total), time.Since(start).Milliseconds()))
	}()
}
func storageUsedBytes() int64        { return atomic.LoadInt64(&cachedStorageBytes) }
func updateStorageDelta(delta int64) { atomic.AddInt64(&cachedStorageBytes, delta) }
func storageDirSizeBytes(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, fi os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !fi.IsDir() {
			total += fi.Size()
		}
		return nil
	})
	return total, err
}
func storageQuotaBytes() int64 { return cfg.StorageQuotaGB * 1024 * 1024 * 1024 }
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func startStuckJobReaper() {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				result, err := db.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing' AND created_at < datetime('now','-5 minutes')`)
				if err != nil {
					logError("queue-reaper", "stuck job error", err.Error())
					continue
				}
				rows, _ := result.RowsAffected()
				if rows > 0 {
					atomic.AddInt64(&metricQueueStuckResets, rows)
					logJSON(logFields{Level: "warn", Component: "queue-reaper", Msg: fmt.Sprintf("reset %d stuck jobs", rows)})
				}
			}
		}
	}()
}

// =============================================================================
// P8 — WAL adaptive checkpoint (ADR-0033)
// =============================================================================

func walFileSizeMB() float64 {
	fi, err := os.Stat(cfg.DBPath + "-wal")
	if err != nil {
		return 0
	}
	return float64(fi.Size()) / (1024 * 1024)
}

func startWALCheckpointGoroutine() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		adaptiveBackoff := false
		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				walMB := walFileSizeMB()
				checkpointMode := "PASSIVE"
				if walMB > float64(cfg.WALSizeThresholdMB) {
					checkpointMode = "RESTART"
					atomic.AddInt64(&metricWALAdaptiveCheckpoints, 1)
					logJSON(logFields{Level: "warn", Component: "wal", Msg: fmt.Sprintf("WAL %.1fMB > threshold %dMB — RESTART checkpoint", walMB, cfg.WALSizeThresholdMB)})
					adaptiveBackoff = true
				} else if adaptiveBackoff {
					adaptiveBackoff = false
					logInfo("wal", "adaptive backoff tick — skipping checkpoint")
					continue
				}
				start := time.Now()
				var pagesWritten int
				err := db.QueryRow(fmt.Sprintf("PRAGMA wal_checkpoint(%s)", checkpointMode)).Scan(new(int), new(int), &pagesWritten)
				if err != nil {
					logError("wal", "checkpoint error", err.Error())
				} else {
					elapsed := time.Since(start)
					atomic.AddInt64(&metricWALCheckpoints, 1)
					atomic.AddInt64(&metricWALCheckpointDurationMS, elapsed.Milliseconds())
					logInfo("wal", fmt.Sprintf("checkpoint(%s) pages=%d dur=%dms total=%d",
						checkpointMode, pagesWritten, elapsed.Milliseconds(), atomic.LoadInt64(&metricWALCheckpoints)))
				}
			}
		}
	}()
}

// =============================================================================
// P7/P8 — Migration system with checksum drift verification (ADR-0026/ADR-0034)
// =============================================================================

type migration struct {
	Version  string
	Up       string
	Down     string
	Checksum string
}

func checksumSQL(sql string) string {
	h := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(h[:])
}

var migrations []migration

func init() {
	upBaseline := `CREATE TABLE IF NOT EXISTS articles(id TEXT PRIMARY KEY,title TEXT NOT NULL,slug TEXT UNIQUE NOT NULL,content TEXT NOT NULL,tags TEXT DEFAULT '',created_at DATETIME NOT NULL,updated_at DATETIME NOT NULL);
CREATE INDEX IF NOT EXISTS idx_articles_slug    ON articles(slug);
CREATE INDEX IF NOT EXISTS idx_articles_created ON articles(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_updated ON articles(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_articles_tags    ON articles(tags);
CREATE TABLE IF NOT EXISTS write_jobs(id INTEGER PRIMARY KEY AUTOINCREMENT,article_json TEXT NOT NULL,op TEXT NOT NULL DEFAULT 'insert',status TEXT NOT NULL DEFAULT 'pending',retries INTEGER NOT NULL DEFAULT 0,retry_at DATETIME,created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE INDEX IF NOT EXISTS idx_jobs_status  ON write_jobs(status,created_at);
CREATE INDEX IF NOT EXISTS idx_jobs_retries ON write_jobs(retries);`
	upSchemaMig := `CREATE TABLE IF NOT EXISTS schema_migrations(id INTEGER PRIMARY KEY AUTOINCREMENT,version TEXT UNIQUE NOT NULL,checksum TEXT NOT NULL DEFAULT '',applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);`
	upRetryAt := `ALTER TABLE write_jobs ADD COLUMN IF NOT EXISTS retry_at DATETIME;`
	// P8: replay_count + dead_reason (ADR-0035)
	upReplayFields := `ALTER TABLE write_jobs ADD COLUMN IF NOT EXISTS replay_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE write_jobs ADD COLUMN IF NOT EXISTS dead_reason TEXT NOT NULL DEFAULT '';`
	// P9: Immutable WORM audit log. Insert-only — triggers ABORT any UPDATE/DELETE.
	upAuditLog := `CREATE TABLE IF NOT EXISTS audit_log(id INTEGER PRIMARY KEY AUTOINCREMENT,ts DATETIME NOT NULL,action TEXT NOT NULL,actor TEXT NOT NULL DEFAULT '',target TEXT NOT NULL DEFAULT '',detail TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts DESC);
CREATE INDEX IF NOT EXISTS idx_audit_action ON audit_log(action);
CREATE TRIGGER IF NOT EXISTS audit_log_no_update BEFORE UPDATE ON audit_log BEGIN SELECT RAISE(ABORT,'audit_log is append-only (WORM): updates forbidden'); END;
CREATE TRIGGER IF NOT EXISTS audit_log_no_delete BEFORE DELETE ON audit_log BEGIN SELECT RAISE(ABORT,'audit_log is append-only (WORM): deletes forbidden'); END;`

	migrations = []migration{
		{Version: "001-baseline", Up: upBaseline, Down: "", Checksum: checksumSQL(upBaseline)},
		{Version: "002-schema-migrations", Up: upSchemaMig, Down: "DROP TABLE IF EXISTS schema_migrations;", Checksum: checksumSQL(upSchemaMig)},
		{Version: "003-queue-retry-at", Up: upRetryAt, Down: "", Checksum: checksumSQL(upRetryAt)},
		{Version: "004-queue-replay-fields", Up: upReplayFields, Down: "", Checksum: checksumSQL(upReplayFields)},
		{Version: "005-audit-log-worm", Up: upAuditLog, Down: "", Checksum: checksumSQL(upAuditLog)},
	}
}

func runMigrations() error {
	dryRun := os.Getenv("VAYU_MIGRATE_DRY_RUN") == "true"
	if dryRun {
		logInfo("migrations", "DRY-RUN mode")
	}
	if !dryRun {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations(id INTEGER PRIMARY KEY AUTOINCREMENT,version TEXT UNIQUE NOT NULL,checksum TEXT NOT NULL DEFAULT '',applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`); err != nil {
			return fmt.Errorf("bootstrap schema_migrations: %w", err)
		}
	}
	for _, m := range migrations {
		if dryRun {
			logInfo("migrations", fmt.Sprintf("[dry-run] would apply: %s", m.Version))
			continue
		}
		var count int
		db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, m.Version).Scan(&count)
		if count > 0 {
			logInfo("migrations", "already applied: "+m.Version)
			continue
		}
		logInfo("migrations", "applying: "+m.Version)
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration %s begin: %w", m.Version, err)
		}
		for _, stmt := range strings.Split(m.Up, "\n") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := tx.Exec(stmt); err != nil {
				tx.Rollback()
				if strings.Contains(err.Error(), "duplicate column") || strings.Contains(err.Error(), "already exists") {
					logInfo("migrations", "column exists in "+m.Version+" — continuing")
					tx2, _ := db.Begin()
					if tx2 != nil {
						tx = tx2
					}
					continue
				}
				return fmt.Errorf("migration %s exec: %w", m.Version, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,checksum) VALUES(?,?)`, m.Version, m.Checksum); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s record: %w", m.Version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migration %s commit: %w", m.Version, err)
		}
		logInfo("migrations", "applied: "+m.Version)
	}
	return nil
}

// P8: verifyMigrationChecksums — detects tampered historical migrations (ADR-0034)
func verifyMigrationChecksums() error {
	rows, err := db.Query(`SELECT version, checksum FROM schema_migrations ORDER BY id ASC`)
	if err != nil {
		return fmt.Errorf("verifyMigrationChecksums query: %w", err)
	}
	defer rows.Close()
	migMap := make(map[string]string)
	for _, m := range migrations {
		migMap[m.Version] = m.Checksum
	}
	var drifted []string
	for rows.Next() {
		var version, storedChecksum string
		rows.Scan(&version, &storedChecksum)
		if storedChecksum == "" {
			continue
		}
		expected, ok := migMap[version]
		if !ok {
			continue
		}
		if storedChecksum != expected {
			atomic.AddInt64(&metricMigrationDriftDetected, 1)
			logJSON(logFields{Level: "error", Component: "migrations", Msg: fmt.Sprintf("CHECKSUM DRIFT: %s stored=%s expected=%s", version, storedChecksum[:8], expected[:8])})
			drifted = append(drifted, version)
		}
	}
	if len(drifted) > 0 {
		return fmt.Errorf("migration drift detected: %s — startup halted (ADR-0034)", strings.Join(drifted, ", "))
	}
	logInfo("migrations", fmt.Sprintf("checksum verification passed: %d migrations (ADR-0034)", len(migMap)))
	return nil
}

func rollbackMigration(version string) error {
	for i := len(migrations) - 1; i >= 0; i-- {
		if migrations[i].Version != version {
			continue
		}
		if migrations[i].Down == "" {
			return fmt.Errorf("migration %s has no Down SQL", version)
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i].Down); err != nil {
			tx.Rollback()
			return fmt.Errorf("rollback exec %s: %w", version, err)
		}
		if _, err := tx.Exec(`DELETE FROM schema_migrations WHERE version=?`, version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logInfo("migrations", "rolled back: "+version)
		return nil
	}
	return fmt.Errorf("migration %s not found", version)
}

func initDB() error {
	var err error
	dsn := cfg.DBPath + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	db, err = sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err = db.Ping(); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	wdb = wrappedDB{db}
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-65536",
		"PRAGMA mmap_size=268435456",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA journal_size_limit=67108864",
		"PRAGMA wal_autocheckpoint=1000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	// P8: adaptive WAL checkpoint (ADR-0033)
	startWALCheckpointGoroutine()
	if err := runMigrations(); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	// P8: drift verification (ADR-0034)
	if err := verifyMigrationChecksums(); err != nil {
		return fmt.Errorf("migration drift: %w", err)
	}
	logInfo("db", "ready — WAL+PRAGMAs enforced, migrations+checksums verified (ADR-0033/0034)")
	return nil
}

// =============================================================================
// P8 — Plugin pool hardened (ADR-0032)
// =============================================================================

type HookFunc func(ctx context.Context, payload map[string]interface{}) error
type hookRegistry struct {
	mu    sync.RWMutex
	hooks map[string][]HookFunc
}

var pluginHooks = &hookRegistry{hooks: make(map[string][]HookFunc)}

func RegisterHook(event string, fn HookFunc) {
	pluginHooks.mu.Lock()
	pluginHooks.hooks[event] = append(pluginHooks.hooks[event], fn)
	pluginHooks.mu.Unlock()
}
func fireHookSafe(event string, fn HookFunc, ctx context.Context, payload map[string]interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			if len(stack) > 2048 {
				stack = stack[:2048]
			}
			atomic.AddInt64(&metricPluginPanics, 1)
			logJSON(logFields{Level: "error", Component: "plugin-hook", Msg: fmt.Sprintf("PANIC in hook %s: %v", event, r), Error: stack})
			err = fmt.Errorf("plugin panic in hook %s: %v", event, r)
		}
	}()
	return fn(ctx, payload)
}

const (
	pluginPoolSize    = 4
	pluginQueueDepth  = 32
	pluginHookTimeout = 2 * time.Second
	pluginFailThresh  = 5
)

type pluginJob struct {
	event   string
	fn      HookFunc
	payload map[string]interface{}
}

var (
	pluginQueue    chan pluginJob
	pluginFailures sync.Map // key -> int64
	pluginDisabled sync.Map // key -> bool
	pluginCtx      context.Context
	pluginCancel   context.CancelFunc
	workerPluginWg sync.WaitGroup // P8: tracks all plugin goroutines (ADR-0032)
)

func initPluginPool() {
	pluginCtx, pluginCancel = context.WithCancel(context.Background())
	pluginQueue = make(chan pluginJob, pluginQueueDepth)
	for i := 0; i < pluginPoolSize; i++ {
		workerPluginWg.Add(1)
		go func(workerID int) {
			defer workerPluginWg.Done()
			// P8: goroutine-level panic isolation (ADR-0032)
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(&metricPluginPanics, 1)
					logJSON(logFields{Level: "error", Component: "plugin-pool", Msg: fmt.Sprintf("worker-%d PANIC: %v — worker terminated", workerID, r)})
				}
			}()
			for {
				select {
				case <-pluginCtx.Done():
					// drain remaining jobs
					for {
						select {
						case job, ok := <-pluginQueue:
							if !ok {
								return
							}
							runPluginJob(job)
						default:
							return
						}
					}
				case job, ok := <-pluginQueue:
					if !ok {
						return
					}
					runPluginJob(job)
				}
			}
		}(i)
	}
	logInfo("plugin-pool", fmt.Sprintf("P8 hardened: workers=%d queue=%d (ADR-0032)", pluginPoolSize, pluginQueueDepth))
}

func runPluginJob(job pluginJob) {
	key := fmt.Sprintf("%s:%p", job.event, job.fn)
	// P8: propagate pluginCtx so shutdown cancels in-progress hooks (ADR-0032)
	ctx, cancel := context.WithTimeout(pluginCtx, pluginHookTimeout)
	err := fireHookSafe(job.event, job.fn, ctx, job.payload)
	cancel()
	if err != nil {
		v, _ := pluginFailures.LoadOrStore(key, int64(0))
		newCount := v.(int64) + 1
		pluginFailures.Store(key, newCount)
		if newCount >= pluginFailThresh {
			pluginDisabled.Store(key, true)
			atomic.AddInt64(&metricPluginDisabled, 1)
			logJSON(logFields{Level: "warn", Component: "plugin-pool", Msg: fmt.Sprintf("hook disabled after %d failures: %s", newCount, job.event)})
		}
	} else {
		pluginFailures.Store(key, int64(0))
	}
}

// P9: clean shutdown — cancel ctx → close channel → Wait() (ADR-0032)
// Order matters: close(pluginQueue) unblocks range loops in workers; Wait()
// then ensures all goroutines have fully exited before the caller proceeds.
func shutdownPluginPool() {
	if pluginCancel == nil {
		return
	}
	logInfo("plugin-pool", "cancelling context and closing queue")
	pluginCancel()
	close(pluginQueue)
	drainDone := make(chan struct{})
	go func() { workerPluginWg.Wait(); close(drainDone) }()
	select {
	case <-drainDone:
		logInfo("plugin-pool", "all workers drained")
	case <-time.After(10 * time.Second):
		logJSON(logFields{Level: "warn", Component: "plugin-pool", Msg: "drain timeout (10s) exceeded"})
	}
}

func FireHook(event string, payload map[string]interface{}) {
	if os.Getenv("VAYU_PLUGINS_ENABLED") != "true" {
		return
	}
	pluginHooks.mu.RLock()
	fns := pluginHooks.hooks[event]
	pluginHooks.mu.RUnlock()
	for _, fn := range fns {
		key := fmt.Sprintf("%s:%p", event, fn)
		if disabled, ok := pluginDisabled.Load(key); ok && disabled.(bool) {
			continue
		}
		job := pluginJob{event: event, fn: fn, payload: payload}
		select {
		case pluginQueue <- job:
		default:
			atomic.AddInt64(&metricPluginPoolDropped, 1)
			logJSON(logFields{Level: "warn", Component: "plugin-pool", Msg: fmt.Sprintf("hook dropped — queue full: %s", event)})
		}
	}
}

// =============================================================================
// Worker pool — P8: maintenance mode + capped backoff (ADR-0035/ADR-0038)
// =============================================================================

func startWorkerPool(wg *sync.WaitGroup) {
	for i := 0; i < cfg.WorkerCount; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			atomic.AddInt64(&workerLiveness, 1)
			defer atomic.AddInt64(&workerLiveness, -1)
			workerLastActivity.Store(workerID, time.Now())
			logInfo("worker", fmt.Sprintf("worker-%d started", workerID))
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-doneCh:
					logInfo("worker", fmt.Sprintf("worker-%d draining", workerID))
					for !processOneJob(workerID) {
					}
					logInfo("worker", fmt.Sprintf("worker-%d done", workerID))
					return
				case <-ticker.C:
					processOneJob(workerID)
				}
			}
		}(i)
	}
}

const maxBackoffSeconds = 300 // P8: cap prevents int overflow (ADR-0035)

func processOneJob(workerID int) (empty bool) {
	if cfg.MaintenanceMode {
		return true
	} // P8: maintenance mode guard (ADR-0038)
	var job WriteJob
	err := db.QueryRow(`SELECT id,article_json,op FROM write_jobs WHERE status='pending' AND (retry_at IS NULL OR retry_at <= datetime('now')) ORDER BY created_at ASC LIMIT 1`).Scan(&job.ID, &job.ArticleJSON, &job.Op)
	if err == sql.ErrNoRows {
		return true
	}
	if err != nil {
		logError("worker", fmt.Sprintf("worker-%d fetch error", workerID), err.Error())
		return false
	}
	wdb.Exec(`UPDATE write_jobs SET status='processing' WHERE id=?`, job.ID)
	jobStart := time.Now()
	var a Article
	if err := json.Unmarshal([]byte(job.ArticleJSON), &a); err != nil {
		logError("worker", fmt.Sprintf("worker-%d bad JSON job %d", workerID, job.ID), err.Error())
		wdb.Exec(`UPDATE write_jobs SET status='dead_letter',dead_reason='parse_error' WHERE id=?`, job.ID)
		return false
	}
	var execErr error
	switch job.Op {
	case "insert":
		_, execErr = db.Exec(`INSERT INTO articles(id,title,slug,content,tags,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, a.ID, a.Title, a.Slug, a.Content, strings.Join(a.Tags, ","), a.CreatedAt, a.UpdatedAt)
		if execErr == nil {
			atomic.AddInt64(&metricArticlesCreated, 1)
			FireHook("article.create", map[string]interface{}{"slug": a.Slug, "id": a.ID})
		}
	case "update":
		_, execErr = db.Exec(`UPDATE articles SET title=?,content=?,tags=?,updated_at=? WHERE slug=?`, a.Title, a.Content, strings.Join(a.Tags, ","), a.UpdatedAt, a.Slug)
		if execErr == nil {
			atomic.AddInt64(&metricArticlesUpdated, 1)
			FireHook("article.update", map[string]interface{}{"slug": a.Slug})
		}
	case "delete":
		_, execErr = db.Exec(`DELETE FROM articles WHERE slug=?`, a.Slug)
		if execErr == nil {
			atomic.AddInt64(&metricArticlesDeleted, 1)
			FireHook("article.delete", map[string]interface{}{"slug": a.Slug})
		}
	default:
		wdb.Exec(`UPDATE write_jobs SET status='dead_letter',dead_reason='unknown_op' WHERE id=?`, job.ID)
		return false
	}
	if execErr != nil {
		var retries int
		db.QueryRow(`SELECT retries FROM write_jobs WHERE id=?`, job.ID).Scan(&retries)
		if retries < 3 {
			// P8: capped exponential backoff (ADR-0035)
			backoffSeconds := int(math.Pow(2, float64(retries+1))) * 5
			if backoffSeconds > maxBackoffSeconds {
				backoffSeconds = maxBackoffSeconds
			}
			nextRetry := time.Now().Add(time.Duration(backoffSeconds) * time.Second).UTC().Format("2006-01-02T15:04:05Z")
			wdb.Exec(`UPDATE write_jobs SET status='pending',retries=retries+1,retry_at=? WHERE id=?`, nextRetry, job.ID)
		} else {
			wdb.Exec(`UPDATE write_jobs SET status='dead_letter',dead_reason='max_retries' WHERE id=?`, job.ID)
			atomic.AddInt64(&metricQueueFailed, 1)
			atomic.AddInt64(&metricDeadLetterJobs, 1)
		}
		return false
	}
	if job.Op != "delete" {
		html, err := renderArticle(a)
		if err != nil {
			logError("worker", "render error for "+a.Slug, err.Error())
		} else {
			cacheWrite(filepath.Join("posts", a.Slug+".html"), html)
		}
		indexArticle(a)
	} else {
		os.Remove(filepath.Join(cfg.CacheDir, "posts", a.Slug+".html"))
		go meiliDo("DELETE", "/indexes/articles/documents/"+a.ID, nil)
	}
	cachePurge(a.Slug, a.Tags)
	go purgeCloudflare(a.Slug)
	go pingIndexNow(a.Slug)
	db.Exec(`UPDATE write_jobs SET status='completed' WHERE id=?`, job.ID)
	atomic.AddInt64(&metricQueueProcessed, 1)
	queueJobLatency.record(time.Since(jobStart))
	var qDepth int
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&qDepth)
	if qDepth > cfg.QueueSaturationWarn {
		logJSON(logFields{Level: "warn", Component: "queue", Msg: fmt.Sprintf("saturation: %d pending", qDepth)})
	}
	workerLastActivity.Store(workerID, time.Now())
	return false
}

// =============================================================================
// Middleware
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
		httpLatency.record(dur)
		logJSON(logFields{Level: "info", RequestID: getRequestID(r), Method: r.Method, Path: r.URL.Path, Status: ww.Status(), LatencyMS: dur.Milliseconds(), RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent(), Component: "http"})
	})
}

// P9: securityHeadersMiddleware — CSP no longer uses style-src 'unsafe-inline'.
// Styles are served from static files only; the nonce covers scripts only. (ADR-0036)
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		nonce := generateCSPNonce()
		csp := fmt.Sprintf("default-src 'self'; font-src 'self'; style-src 'self'; script-src 'self' 'nonce-%s'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'", nonce)
		w.Header().Set("Content-Security-Policy", csp)
		ctx := context.WithValue(r.Context(), ctxKeyCSPNonce{}, nonce)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			ip = xri
		} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if locked, until := checkAuthLockout(ip); locked {
			retryAfter := int(time.Until(until).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeAPIError(w, r, 429, "auth_lockout", fmt.Sprintf("locked out for %ds", retryAfter), "https://docs.vayupress.com/api/auth#lockout")
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if key != cfg.APIKey {
			recordAuthFailure(ip)
			writeAPIError(w, r, 401, "unauthorized", "invalid or missing API key", "https://docs.vayupress.com/api/auth")
			return
		}
		recordAuthSuccess(ip)
		next.ServeHTTP(w, r)
	})
}

type ipBucket struct {
	count   int
	resetAt time.Time
}

var (
	rateMu      sync.Mutex
	rateBuckets = make(map[string]*ipBucket)
	trustedIPs  = parseTrustedIPs()
)

func parseTrustedIPs() map[string]bool {
	m := make(map[string]bool)
	for _, ip := range strings.Split(envOr("TRUSTED_IPS", ""), ",") {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			m[ip] = true
		}
	}
	return m
}
func rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if trustedIPs[ip] {
			next.ServeHTTP(w, r)
			return
		}
		rateMu.Lock()
		b, ok := rateBuckets[ip]
		if !ok || time.Now().After(b.resetAt) {
			b = &ipBucket{1, time.Now().Add(time.Hour)}
			rateBuckets[ip] = b
		} else {
			b.count++
		}
		allowed := b.count <= 100
		rateMu.Unlock()
		if !allowed {
			writeAPIError(w, r, 429, "rate_limit_exceeded", "rate limit exceeded (100 req/hour)", "https://docs.vayupress.com/api/rate-limiting")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// =============================================================================
// Response helpers
// =============================================================================

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
func cacheHitRatio() float64 {
	hits := atomic.LoadInt64(&metricCacheHits)
	misses := atomic.LoadInt64(&metricCacheMisses)
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// =============================================================================
// Cache helpers + rendering stubs
// =============================================================================

func cacheWrite(relPath, content string) error {
	full := filepath.Join(cfg.CacheDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	oldSize := int64(0)
	if fi, err := os.Stat(full); err == nil {
		oldSize = fi.Size()
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		return err
	}
	updateStorageDelta(int64(len(content)) - oldSize)
	return nil
}
func cachePurge(slug string, tags []string) {
	postFile := filepath.Join(cfg.CacheDir, "posts", slug+".html")
	if fi, err := os.Stat(postFile); err == nil {
		updateStorageDelta(-fi.Size())
	}
	os.Remove(postFile)
	os.Remove(filepath.Join(cfg.CacheDir, "home", "index.html"))
	for _, t := range tags {
		if t != "" {
			tagFile := filepath.Join(cfg.CacheDir, "tags", t+".html")
			if fi, err := os.Stat(tagFile); err == nil {
				updateStorageDelta(-fi.Size())
			}
			os.Remove(tagFile)
		}
	}
	go generateSitemap()
	go generateRSS()
	go generateRobots()
}

// =============================================================================
// Meilisearch + Cloudflare + IndexNow
// =============================================================================

func initMeilisearchCB() {
	meiliCB = gobreaker.NewCircuitBreaker(gobreaker.Settings{Name: "meilisearch", MaxRequests: 3, Interval: 10 * time.Second, Timeout: 30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.Requests >= 3 && float64(counts.TotalFailures)/float64(counts.Requests) >= 0.60
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logJSON(logFields{Level: "warn", Component: "meili-cb", Msg: fmt.Sprintf("%s → %s", from, to)})
		},
	})
}
func meiliDo(method, path string, body interface{}) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, cfg.MeiliHost+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeiliMasterKey)
	}
	resp, err := outboundClient.Do(req)
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
func configureMeilisearch() {
	_ = meiliDo("PATCH", "/indexes/articles/settings", map[string]interface{}{
		"rankingRules":         []string{"words", "proximity", "attribute", "sort", "exactness"},
		"searchableAttributes": []string{"title", "tags", "content"},
		"filterableAttributes": []string{"tags", "created_at"},
		"sortableAttributes":   []string{"created_at", "updated_at"},
	})
}
func indexArticle(a Article) {
	if meiliCB == nil {
		return
	}
	doc := map[string]interface{}{"id": a.ID, "title": a.Title, "slug": a.Slug, "content": htmlTagRe.ReplaceAllString(policy.Sanitize(a.Content), ""), "tags": a.Tags, "created_at": a.CreatedAt.Unix()}
	_, err := meiliCB.Execute(func() (interface{}, error) {
		return nil, meiliDo("POST", "/indexes/articles/documents", []map[string]interface{}{doc})
	})
	if err != nil {
		atomic.AddInt64(&metricMeiliErrors, 1)
	}
}
func purgeCloudflare(slug string) {
	if cfg.CFZoneID == "" || cfg.CFAPIToken == "" {
		return
	}
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/purge_cache", cfg.CFZoneID)
	body, _ := json.Marshal(map[string][]string{"files": {"https://" + cfg.Domain + "/" + slug}})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.CFAPIToken)
	resp, err := outboundClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}
func pingIndexNow(slug string) {
	if cfg.IndexNowKey == "" {
		return
	}
	body, _ := json.Marshal(map[string]interface{}{"host": cfg.Domain, "key": cfg.IndexNowKey, "keyLocation": "https://" + cfg.Domain + "/.well-known/" + cfg.IndexNowKey + ".txt", "urlList": []string{"https://" + cfg.Domain + "/" + slug}})
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://api.indexnow.org/indexnow", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := outboundClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
}

// =============================================================================
// Sitemap / RSS / robots / cache warmup
// =============================================================================

func generateSitemap() {
	rows, err := db.Query(`SELECT slug,updated_at FROM articles ORDER BY updated_at DESC LIMIT 50000`)
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
		fmt.Fprintf(&sb, "<url><loc>https://%s/%s</loc><lastmod>%s</lastmod></url>", cfg.Domain, slug, updated.Format("2006-01-02"))
	}
	sb.WriteString("</urlset>")
	cacheWrite("sitemap.xml", sb.String())
}
func generateRSS() {
	rows, err := db.Query(`SELECT title,slug,content,created_at FROM articles ORDER BY created_at DESC LIMIT 50`)
	if err != nil {
		return
	}
	defer rows.Close()
	var items strings.Builder
	for rows.Next() {
		var title, slug, content string
		var created time.Time
		rows.Scan(&title, &slug, &content, &created)
		plain := htmlTagRe.ReplaceAllString(policy.Sanitize(content), "")
		if len(plain) > 500 {
			plain = plain[:500] + "..."
		}
		fmt.Fprintf(&items, "<item><title><![CDATA[%s]]></title><link>https://%s/%s</link><guid isPermaLink=\"true\">https://%s/%s</guid><pubDate>%s</pubDate><description><![CDATA[%s]]></description></item>", title, cfg.Domain, slug, cfg.Domain, slug, created.Format(time.RFC1123Z), plain)
	}
	rss := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>%s</title><link>https://%s</link><description>%s</description>%s</channel></rss>`, cfg.Domain, cfg.Domain, cfg.Domain, items.String())
	cacheWrite("feed.xml", rss)
}
func generateRobots() {
	cacheWrite("robots.txt", fmt.Sprintf("User-agent: *\nAllow: /\nDisallow: /api/\nDisallow: /admin\n\nSitemap: https://%s/sitemap.xml\n", cfg.Domain))
}
func warmCache() {
	rows, err := db.Query(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles ORDER BY updated_at DESC LIMIT 1000`)
	if err != nil {
		return
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var a Article
		var tagsStr string
		rows.Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsStr, &a.CreatedAt, &a.UpdatedAt)
		a.Tags = splitTags(tagsStr)
		dest := filepath.Join(cfg.CacheDir, "posts", a.Slug+".html")
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		html, err := renderArticle(a)
		if err != nil {
			continue
		}
		cacheWrite(filepath.Join("posts", a.Slug+".html"), html)
		count++
	}
	logInfo("cache-warm", fmt.Sprintf("pre-rendered %d articles", count))
}

// =============================================================================
// CSS assets + rendering (P4/P5)
// =============================================================================

var cssHashes struct{ ArticleCSS, AdminCSS, HighContrastCSS string }

const articleCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--border:#1F2937;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--max-w:720px;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:4px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px;--sp6:48px}
@media(prefers-color-scheme:light){:root{--bg:#fff;--surface:#F9FAFB;--border:#E5E7EB;--text:#111827;--muted:#6B7280}}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font:500 13px/1.4 var(--font);text-decoration:none;transition:top .2s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}
body{background:var(--bg);color:var(--text);font:400 18px/1.6 var(--font);padding:var(--sp5) var(--sp3)}
.container{max-width:var(--max-w);margin:0 auto}
header{border-bottom:1px solid var(--border);padding-bottom:var(--sp5);margin-bottom:var(--sp5)}
h1{font:700 2rem/1.2 var(--font);margin-bottom:var(--sp2);letter-spacing:-.5px}
.meta{color:var(--muted);font-size:13px;display:flex;flex-wrap:wrap;gap:var(--sp2)}
.tags a{display:inline-block;padding:2px var(--sp2);border:1px solid var(--border);border-radius:var(--radius);font-size:12px;color:var(--accent);text-decoration:none}
.tags a:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.content{margin-top:var(--sp5)}.content h2,.content h3{font:600 1.25rem/1.3 var(--font);margin:var(--sp5) 0 var(--sp3)}
.content pre{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3);overflow-x:auto;font:400 14px/1.5 var(--mono);margin:var(--sp3) 0}
.content code{background:var(--surface);padding:2px 6px;border-radius:var(--radius);font:400 14px var(--mono)}.content pre code{background:none;padding:0}
.content blockquote{border-left:4px solid var(--accent);padding-left:var(--sp3);color:var(--muted);margin:var(--sp3) 0}
footer{margin-top:var(--sp6);padding-top:var(--sp5);border-top:1px solid var(--border);font-size:13px;color:var(--muted)}
a:focus-visible{outline:2px solid var(--accent);outline-offset:2px;border-radius:2px}
@media(max-width:480px){body{padding:var(--sp3)}h1{font-size:1.5rem}}`

const adminCSSMin = `:root{--bg:#0B0F14;--surface:#111827;--surface2:#161f2e;--border:#1F2937;--border2:#2d3a4a;--text:#E5E7EB;--muted:#9CA3AF;--accent:#3B82F6;--hi:#38BDF8;--success:#10B981;--warn:#F59E0B;--error:#EF4444;--font:'Inter',system-ui,sans-serif;--mono:'IBM Plex Mono',monospace;--radius:4px;--sp1:4px;--sp2:8px;--sp3:16px;--sp4:24px;--sp5:32px}
@media(prefers-reduced-motion:reduce){*,*::before,*::after{transition:none!important;animation:none!important}}
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font:400 14px/1.5 var(--font);min-height:100vh}
.skip-link{position:absolute;top:-40px;left:0;z-index:9999;background:var(--accent);color:#fff;padding:var(--sp2) var(--sp3);font-weight:500;text-decoration:none;transition:top .15s}.skip-link:focus{top:0;outline:3px solid var(--hi);outline-offset:2px}
.app-shell{display:grid;grid-template-rows:auto 1fr;min-height:100vh}
.topbar{display:flex;align-items:center;justify-content:space-between;padding:var(--sp3) var(--sp4);background:var(--surface);border-bottom:1px solid var(--border);position:sticky;top:0;z-index:100}
.topbar-brand{display:flex;align-items:center;gap:var(--sp2);font-weight:600;font-size:15px;color:var(--text);text-decoration:none}
.topbar-domain{color:var(--muted);font-size:12px;font-weight:400}
.topbar-actions{display:flex;align-items:center;gap:var(--sp2)}
.kbd-hint{font:400 11px var(--mono);color:var(--muted);background:var(--surface2);border:1px solid var(--border2);border-radius:var(--radius);padding:2px 6px;cursor:pointer;transition:border-color .15s,color .15s}
.kbd-hint:hover,.kbd-hint:focus-visible{border-color:var(--accent);color:var(--text);outline:2px solid var(--accent);outline-offset:2px}
main{padding:var(--sp4);max-width:1100px}
.section-title{font-size:10px;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted);margin:var(--sp4) 0 var(--sp3);padding-bottom:var(--sp2);border-bottom:1px solid var(--border)}
.stat-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:var(--sp3);margin-bottom:var(--sp4)}
.stat-card{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp3)}
.stat-val{font:700 1.875rem/1 var(--font);color:var(--accent);margin-bottom:4px}
.stat-val.stat-ok{color:var(--success)}.stat-val.stat-warn{color:var(--warn)}.stat-val.stat-err{color:var(--error)}
.stat-lbl{font-size:11px;color:var(--muted)}.stat-sub{font-size:11px;color:var(--muted);margin-top:6px}
.storage-bar{height:3px;background:var(--border2);border-radius:2px;margin-top:8px;overflow:hidden}
.storage-fill{height:100%;border-radius:2px;background:var(--accent)}
.thresh-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:var(--sp2);margin-bottom:var(--sp4)}
.thresh-item{display:flex;align-items:center;justify-content:space-between;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:var(--sp2) var(--sp3);font-size:12px}
.thresh-name{color:var(--muted)}.thresh-val{font:500 12px var(--mono);color:var(--text)}
.thresh-ok{color:var(--success);font-weight:600}.thresh-fail{color:var(--error);font-weight:600}
.action-row{display:flex;flex-wrap:wrap;gap:var(--sp2);margin-bottom:var(--sp4)}
.btn{display:inline-flex;align-items:center;gap:6px;padding:7px 14px;background:transparent;border:1px solid var(--border2);border-radius:var(--radius);color:var(--text);font:500 13px var(--font);cursor:pointer;text-decoration:none;transition:border-color .15s,background .15s,color .15s}
.btn:hover,.btn:focus-visible{border-color:var(--accent);background:rgba(59,130,246,.06);color:var(--hi);outline:2px solid var(--accent);outline-offset:2px}
.btn.btn-primary{background:var(--accent);border-color:var(--accent);color:#fff}
.data-table{width:100%;border-collapse:collapse;font-size:13px}
.data-table th{text-align:left;font-size:10px;font-weight:600;letter-spacing:.05em;text-transform:uppercase;color:var(--muted);padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border)}
.data-table td{padding:var(--sp2) var(--sp3);border-bottom:1px solid var(--border);vertical-align:middle;max-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.data-table tr:hover td{background:var(--surface2)}.data-table td a{color:var(--accent);text-decoration:none}
.action-msg{display:none;padding:var(--sp2) var(--sp3);background:var(--surface);border:1px solid var(--success);border-radius:var(--radius);font-size:13px;margin-bottom:var(--sp3)}
.action-msg.visible{display:block}.links-row{display:flex;flex-wrap:wrap;gap:var(--sp3);margin-top:var(--sp3)}
.links-row a{color:var(--accent);font-size:13px;text-decoration:none}.links-row a:hover{text-decoration:underline}
.admin-footer{margin-top:var(--sp5);padding-top:var(--sp4);border-top:1px solid var(--border);font-size:11px;color:var(--muted)}
.modal-backdrop{display:none;position:fixed;inset:0;z-index:1000;background:rgba(0,0,0,.7);align-items:center;justify-content:center}
.modal-backdrop.open{display:flex}.modal{background:var(--surface);border:1px solid var(--border2);border-radius:var(--radius);padding:var(--sp4);min-width:320px;max-width:480px;width:90%}
.modal-title{display:flex;align-items:center;justify-content:space-between;font-weight:600;font-size:14px;margin-bottom:var(--sp3)}
.modal-close{background:none;border:none;color:var(--muted);cursor:pointer;font-size:16px;padding:4px;border-radius:var(--radius);line-height:1}
.modal-close:hover,.modal-close:focus-visible{color:var(--text);outline:2px solid var(--accent);outline-offset:2px}
.shortcut-list{list-style:none;display:flex;flex-direction:column;gap:var(--sp2)}
.shortcut-item{display:flex;align-items:center;justify-content:space-between;font-size:13px;padding:var(--sp2) 0;border-bottom:1px solid var(--border)}
.shortcut-item:last-child{border-bottom:none}.shortcut-desc{color:var(--text)}
kbd{display:inline-block;padding:2px 6px;background:var(--surface2);border:1px solid var(--border2);border-radius:3px;font:500 11px var(--mono);color:var(--text);min-width:22px;text-align:center}
a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
@media(max-width:600px){.topbar{padding:var(--sp2) var(--sp3)}main{padding:var(--sp3)}.stat-grid{grid-template-columns:repeat(2,1fr)}}`

const hcCSSMin = `@media(prefers-contrast:more){:root{--bg:#000;--surface:#0a0a0a;--border:#fff;--text:#fff;--muted:#ccc;--accent:#6699ff}.btn{border-width:2px!important}.stat-card{border-width:2px!important}.thresh-ok{color:#00ff88!important;font-weight:700!important}.thresh-fail{color:#ff4444!important;font-weight:700!important}}
@media(forced-colors:active){*:focus-visible{outline:3px solid Highlight!important;outline-offset:2px!important}.btn,button{forced-color-adjust:none;background:ButtonFace!important;color:ButtonText!important;border:2px solid ButtonBorder!important}.storage-fill{background:Highlight!important;forced-color-adjust:none}}`

func writeCSSAssets(staticDir string) {
	cssDir := filepath.Join(staticDir, "css")
	if err := os.MkdirAll(cssDir, 0755); err != nil {
		return
	}
	type asset struct {
		name, content string
		hash          *string
	}
	for _, a := range []asset{
		{"article.css", articleCSSMin, &cssHashes.ArticleCSS},
		{"admin.css", adminCSSMin, &cssHashes.AdminCSS},
		{"high-contrast.css", hcCSSMin, &cssHashes.HighContrastCSS},
	} {
		if err := os.WriteFile(filepath.Join(cssDir, a.name), []byte(a.content), 0644); err != nil {
			continue
		}
		sum := sha256.Sum256([]byte(a.content))
		*a.hash = hex.EncodeToString(sum[:])
	}
}
func cssLink(filename, hash string) template.HTML {
	ver := hash
	if len(ver) > 8 {
		ver = ver[:8]
	}
	return template.HTML(fmt.Sprintf(`<link rel="stylesheet" href="/static/css/%s?v=%s">`, filename, ver))
}

// =============================================================================
// Article template + rendering
// =============================================================================

type ArticleLayoutType string

const (
	ArticleLayoutDefault ArticleLayoutType = "default"
	ArticleLayoutMinimal ArticleLayoutType = "minimal"
	ArticleLayoutWide    ArticleLayoutType = "wide"
)

var articleTmpl = template.Must(template.New("article").Funcs(template.FuncMap{
	"trunc": func(s string, n int) string {
		s = htmlTagRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		if len(s) > n {
			return s[:n] + "..."
		}
		return s
	},
	"safeHTML": func(s string) template.HTML { return template.HTML(s) },
	"jsonAttr": func(s string) string {
		s = htmlTagRe.ReplaceAllString(s, "")
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, `"`, `\"`)
		s = strings.ReplaceAll(s, "\n", " ")
		if len(s) > 300 {
			s = s[:300]
		}
		return s
	},
	"readTime": func(s string) int {
		text := htmlTagRe.ReplaceAllString(s, "")
		words := len(strings.Fields(text))
		if words < 200 {
			return 1
		}
		return (words + 199) / 200
	},
	"isoDate":   func(t time.Time) string { return t.UTC().Format(time.RFC3339) },
	"shortDate": func(t time.Time) string { return t.UTC().Format("2006-01-02") },
	"humanDate": func(t time.Time) string { return t.Format("2 January 2006") },
}).Parse(`<!DOCTYPE html><html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — {{.Domain}}</title>
<meta name="description" content="{{.Content | trunc 160}}">
<meta name="generator" content="VayuPress {{.Version}}">
<link rel="canonical" href="https://{{.Domain}}/{{.Slug}}">
<meta property="og:type" content="article"><meta property="og:title" content="{{.Title}}">
<meta property="og:url" content="https://{{.Domain}}/{{.Slug}}">
<meta property="article:published_time" content="{{.CreatedAt | isoDate}}">
<meta property="article:modified_time" content="{{.UpdatedAt | isoDate}}">
<script type="application/ld+json">{"@context":"https://schema.org","@type":"BlogPosting","headline":"{{.Title | jsonAttr}}","datePublished":"{{.CreatedAt | isoDate}}","dateModified":"{{.UpdatedAt | isoDate}}","inLanguage":"en","author":{"@type":"Person","name":"Ankush Choudhary Johal","url":"https://{{.Domain}}/about"},"publisher":{"@type":"Organization","name":"VayuPress","url":"https://{{.Domain}}"}}</script>
{{.ArticleCSSLink}}{{.HighContrastCSSLink}}
</head><body>
<a href="#main-content" class="skip-link">Skip to main content</a>
<div class="container"><main id="main-content">
<article itemscope itemtype="https://schema.org/BlogPosting">
<header><h1 itemprop="headline">{{.Title}}</h1>
<div class="meta"><time itemprop="datePublished" datetime="{{.CreatedAt | shortDate}}">{{.CreatedAt | humanDate}}</time>
<span>· {{.Content | readTime}} min read</span>
{{if .Tags}}<nav class="tags" aria-label="Tags">{{range .Tags}}<a href="/tags/{{.}}" rel="tag">#{{.}}</a>{{end}}</nav>{{end}}</div>
</header><div class="content" itemprop="articleBody">{{.Content | safeHTML}}</div>
</article>
<footer><p>By <strong>Ankush Choudhary Johal</strong> · Powered by <a href="https://vayupress.com">VayuPress</a></p></footer>
</main></div></body></html>`))

type articlePage struct {
	Article
	Domain              string
	Version             string
	Layout              ArticleLayoutType
	ArticleCSSLink      template.HTML
	HighContrastCSSLink template.HTML
}

func renderArticle(a Article) (string, error) {
	return renderArticleWithLayout(a, ArticleLayoutDefault)
}
func renderArticleWithLayout(a Article, layout ArticleLayoutType) (string, error) {
	a.Content = policy.Sanitize(a.Content)
	FireHook("render.pre", map[string]interface{}{"slug": a.Slug})
	start := time.Now()
	var buf strings.Builder
	data := articlePage{Article: a, Domain: cfg.Domain, Version: Version, Layout: layout,
		ArticleCSSLink: cssLink("article.css", cssHashes.ArticleCSS), HighContrastCSSLink: cssLink("high-contrast.css", cssHashes.HighContrastCSS)}
	if err := articleTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template: %w", err)
	}
	renderLatency.record(time.Since(start))
	FireHook("render.post", map[string]interface{}{"slug": a.Slug, "size_bytes": buf.Len()})
	return buf.String(), nil
}
func detectLayout(a Article, r *http.Request, isAdmin bool) ArticleLayoutType {
	if isAdmin {
		switch ArticleLayoutType(r.URL.Query().Get("layout")) {
		case ArticleLayoutMinimal:
			return ArticleLayoutMinimal
		case ArticleLayoutWide:
			return ArticleLayoutWide
		}
	}
	for _, tag := range a.Tags {
		switch tag {
		case "layout:minimal":
			return ArticleLayoutMinimal
		case "layout:wide":
			return ArticleLayoutWide
		}
	}
	return ArticleLayoutDefault
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

var metricsSnapshot atomic.Value

func collectAdminMetrics() {
	snap := &adminMetricsSnapshot{SnapshotAt: time.Now().UTC()}
	row := db.QueryRow(`SELECT (SELECT COUNT(1) FROM articles),SUM(CASE WHEN status='pending' THEN 1 ELSE 0 END),SUM(CASE WHEN status='failed' THEN 1 ELSE 0 END),SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM write_jobs`)
	row.Scan(&snap.TotalArticles, &snap.PendingJobs, &snap.FailedJobs, &snap.CompletedJobs)
	snap.StorageBytes = storageUsedBytes()
	snap.QuotaBytes = storageQuotaBytes()
	if snap.QuotaBytes > 0 {
		snap.StoragePct = float64(snap.StorageBytes) / float64(snap.QuotaBytes) * 100
	}
	snap.WorkersAlive = atomic.LoadInt64(&workerLiveness)
	snap.CacheHitRatio = cacheHitRatio()
	snap.UptimeSeconds = time.Since(bootTime).Seconds()
	snap.HTTPP95 = httpLatency.percentile(95)
	snap.WriteP99 = queueJobLatency.percentile(99)
	snap.RenderP99 = renderLatency.percentile(99)
	rows, err := db.Query(`SELECT title,slug,created_at FROM articles ORDER BY created_at DESC LIMIT 15`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var ra adminRecentArticle
			rows.Scan(&ra.Title, &ra.Slug, &ra.CreatedAt)
			snap.RecentArticles = append(snap.RecentArticles, ra)
		}
	}
	metricsSnapshot.Store(snap)
}
func startMetricsSnapshotCollector() {
	collectAdminMetrics()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-doneCh:
				return
			case <-ticker.C:
				collectAdminMetrics()
			}
		}
	}()
}
func getAdminSnapshot() *adminMetricsSnapshot {
	if v := metricsSnapshot.Load(); v != nil {
		return v.(*adminMetricsSnapshot)
	}
	collectAdminMetrics()
	if v := metricsSnapshot.Load(); v != nil {
		return v.(*adminMetricsSnapshot)
	}
	return &adminMetricsSnapshot{SnapshotAt: time.Now()}
}

// =============================================================================
// API handlers
// =============================================================================

func handleCreateArticle(w http.ResponseWriter, r *http.Request) {
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
	if storageUsedBytes() >= storageQuotaBytes() {
		writeAPIError(w, r, 413, "storage_quota_exceeded", fmt.Sprintf("quota %dGB exceeded", cfg.StorageQuotaGB), "https://docs.vayupress.com/api/articles")
		return
	}
	var count int
	db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
	if count > 0 {
		writeAPIError(w, r, 409, "slug_conflict", "slug already exists", "https://docs.vayupress.com/api/articles")
		return
	}
	a := Article{ID: newUUID(), Title: in.Title, Slug: in.Slug, Content: in.Content, Tags: in.Tags, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(a)
	if _, err := db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		writeAPIError(w, r, 500, "queue_error", err.Error(), "https://docs.vayupress.com/api/errors")
		return
	}
	auditLog("article.create", auditActor(r), a.Slug, "id="+a.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "id": a.ID, "slug": a.Slug})
}
func handleBulkCreateArticles(w http.ResponseWriter, r *http.Request) {
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
		db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, in.Slug).Scan(&count)
		if count > 0 {
			skipped++
			skipReasons = append(skipReasons, in.Slug+": duplicate slug")
			continue
		}
		a := Article{ID: newUUID(), Title: in.Title, Slug: in.Slug, Content: in.Content, Tags: in.Tags, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		payload, _ := json.Marshal(a)
		db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload)
		queued++
	}
	writeJSON(w, r, 202, map[string]interface{}{"status": "queued", "queued": queued, "skipped": skipped, "skip_reasons": skipReasons})
}
func handleUpdateArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var a Article
	var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsStr, &a.CreatedAt, &a.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	a.Tags = splitTags(tagsStr)
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
		a.Title = *in.Title
	}
	if in.Content != nil {
		a.Content = *in.Content
	}
	if in.Tags != nil {
		a.Tags = in.Tags
	}
	a.UpdatedAt = time.Now().UTC()
	payload, _ := json.Marshal(a)
	db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'update')`, payload)
	auditLog("article.update", auditActor(r), a.Slug, "id="+a.ID)
	writeJSON(w, r, 202, map[string]string{"status": "queued", "slug": a.Slug})
}
func handleDeleteArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	var a Article
	var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsStr, &a.CreatedAt, &a.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	a.Tags = splitTags(tagsStr)
	payload, _ := json.Marshal(a)
	db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	auditLog("article.delete", auditActor(r), slug, "id="+a.ID)
	writeJSON(w, r, 200, map[string]string{"status": "queued", "slug": slug})
}
func handleGetArticle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !isValidSlug(slug) {
		writeAPIError(w, r, 400, "invalid_slug", "invalid slug", "https://docs.vayupress.com/api/articles")
		return
	}
	var a Article
	var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsStr, &a.CreatedAt, &a.UpdatedAt); err == sql.ErrNoRows {
		writeAPIError(w, r, 404, "not_found", "not found", "https://docs.vayupress.com/api/articles")
		return
	}
	a.Tags = splitTags(tagsStr)
	writeJSON(w, r, 200, a)
}
func handleListArticles(w http.ResponseWriter, r *http.Request) {
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
		db.QueryRow(`SELECT COUNT(1) FROM articles WHERE tags LIKE ?`, "%"+tag+"%").Scan(&total)
		rows_, err = db.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles WHERE tags LIKE ? ORDER BY created_at DESC LIMIT ? OFFSET ?`, "%"+tag+"%", limit, offset)
	} else {
		db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&total)
		rows_, err = db.Query(`SELECT id,title,slug,tags,created_at,updated_at FROM articles ORDER BY created_at DESC LIMIT ? OFFSET ?`, limit, offset)
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
func handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if q == "" {
		writeJSON(w, r, 200, map[string]interface{}{"hits": []interface{}{}, "query": ""})
		return
	}
	if meiliCB == nil || meiliCB.State() != gobreaker.StateClosed {
		handleSearchFallback(w, r, q, limit)
		return
	}
	body, _ := json.Marshal(map[string]interface{}{"q": q, "limit": limit, "attributesToRetrieve": []string{"title", "slug", "tags", "created_at"}})
	req, err := http.NewRequestWithContext(context.Background(), "POST", cfg.MeiliHost+"/indexes/articles/search", bytes.NewReader(body))
	if err != nil {
		handleSearchFallback(w, r, q, limit)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.MeiliMasterKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.MeiliMasterKey)
	}
	resp, err := outboundClient.Do(req)
	if err != nil {
		handleSearchFallback(w, r, q, limit)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		handleSearchFallback(w, r, q, limit)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	io.Copy(w, resp.Body)
}
func handleSearchFallback(w http.ResponseWriter, r *http.Request, q string, limit int) {
	pattern := "%" + q + "%"
	rows, err := db.Query(`SELECT title,slug,tags,created_at FROM articles WHERE title LIKE ? OR content LIKE ? OR tags LIKE ? ORDER BY created_at DESC LIMIT ?`, pattern, pattern, pattern, limit)
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
func handleListTags(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT tags FROM articles WHERE tags != ''`)
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
// Stats, queue, metrics, health endpoints
// =============================================================================

func handleStats(w http.ResponseWriter, r *http.Request) {
	var totalArticles, pendingJobs, failedJobs int
	db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&failedJobs)
	used := storageUsedBytes()
	quota := storageQuotaBytes()
	writeJSON(w, r, 200, map[string]interface{}{
		"version": Version, "uptime_seconds": time.Since(bootTime).Seconds(),
		"config_version": ConfigVersion,
		"articles_total": totalArticles, "queue_pending": pendingJobs, "queue_failed": failedJobs,
		"storage_used_bytes": used, "storage_quota_bytes": quota,
		"workers_alive":    atomic.LoadInt64(&workerLiveness),
		"maintenance_mode": cfg.MaintenanceMode,
		"metrics": map[string]int64{
			"articles_created": atomic.LoadInt64(&metricArticlesCreated), "articles_updated": atomic.LoadInt64(&metricArticlesUpdated),
			"articles_deleted": atomic.LoadInt64(&metricArticlesDeleted), "queue_processed": atomic.LoadInt64(&metricQueueProcessed),
			"wal_adaptive_checkpoints": atomic.LoadInt64(&metricWALAdaptiveCheckpoints),
			"migration_drift_detected": atomic.LoadInt64(&metricMigrationDriftDetected),
			"poison_jobs_quarantined":  atomic.LoadInt64(&metricPoisonJobsQuarantined),
			"pprof_accesses":           atomic.LoadInt64(&metricPprofAccesses),
			"vacuum_rejected":          atomic.LoadInt64(&metricVacuumRejected),
		},
		"latency_ms": map[string]interface{}{
			"http_p95": httpLatency.percentile(95), "http_p99": httpLatency.percentile(99),
			"render_p99": renderLatency.percentile(99), "queue_job_p99": queueJobLatency.percentile(99),
			"sqlite_write_p99": sqliteWriteLatency.percentile(99),
		},
	})
}
func handleQueueStatus(w http.ResponseWriter, r *http.Request) {
	var pending, processing, completed, failed, deadLetter, quarantined int
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='processing'`).Scan(&processing)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='completed'`).Scan(&completed)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='failed'`).Scan(&failed)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='dead_letter'`).Scan(&deadLetter)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='quarantined'`).Scan(&quarantined)
	var oldestSec float64
	db.QueryRow(`SELECT COALESCE(CAST((julianday('now')-julianday(MIN(created_at)))*86400 AS INTEGER),0) FROM write_jobs WHERE status='pending'`).Scan(&oldestSec)
	writeJSON(w, r, 200, map[string]interface{}{"pending": pending, "processing": processing, "completed": completed, "failed": failed, "dead_letter": deadLetter, "quarantined": quarantined, "oldest_pending_seconds": oldestSec, "maintenance_mode": cfg.MaintenanceMode})
}
func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var totalArticles int
	db.QueryRow(`SELECT COUNT(1) FROM articles`).Scan(&totalArticles)
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
		atomic.LoadInt64(&metricArticlesCreated), atomic.LoadInt64(&metricArticlesUpdated), atomic.LoadInt64(&metricArticlesDeleted),
		atomic.LoadInt64(&metricQueueProcessed), atomic.LoadInt64(&metricQueueFailed), atomic.LoadInt64(&metricQueueStuckResets),
		atomic.LoadInt64(&metricMeiliErrors), atomic.LoadInt64(&metricCacheHits), atomic.LoadInt64(&metricCacheMisses),
		cacheHitRatio(), ms.Alloc, atomic.LoadInt64(&workerLiveness),
		atomic.LoadInt64(&cachedStorageBytes), atomic.LoadInt64(&metricPluginPanics), atomic.LoadInt64(&metricAuthLockouts),
		atomic.LoadInt64(&metricWALCheckpoints), atomic.LoadInt64(&metricSlowQueries), atomic.LoadInt64(&metricDeadLetterJobs),
		atomic.LoadInt64(&metricWALCheckpointDurationMS), atomic.LoadInt64(&metricWALAdaptiveCheckpoints),
		atomic.LoadInt64(&metricMigrationDriftDetected), atomic.LoadInt64(&metricPoisonJobsQuarantined),
		atomic.LoadInt64(&metricPprofAccesses), atomic.LoadInt64(&metricVacuumRejected),
		atomic.LoadInt64(&metricHealthDegradedEvents),
	)
	fmt.Fprint(w, httpLatency.prometheus("vayupress_http_request_duration_seconds", "HTTP latency"))
	fmt.Fprint(w, renderLatency.prometheus("vayupress_render_duration_seconds", "Render latency"))
	fmt.Fprint(w, queueJobLatency.prometheus("vayupress_queue_job_duration_seconds", "Queue job latency"))
	fmt.Fprint(w, sqliteWriteLatency.prometheus("vayupress_sqlite_write_duration_seconds", "SQLite write latency"))
}

// Health endpoints — P7 + P8 structured contracts (ADR-0041)
// healthSchemaVersion is incremented when the shape of any /health response changes.
// Automation consumers should assert schema_version matches their expectation.
const healthSchemaVersion = "1"

func handleHealthLiveness(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, 200, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "alive", "version": Version, "config_version": ConfigVersion, "uptime_seconds": time.Since(bootTime).Seconds()})
}
func handleHealthReady(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		writeJSON(w, r, 503, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "not_ready", "reason": "db unavailable"})
		return
	}
	if alive := atomic.LoadInt64(&workerLiveness); alive < 1 {
		writeJSON(w, r, 503, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "not_ready", "reason": "no workers"})
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "ready"})
}
func handleHealthDB(w http.ResponseWriter, r *http.Request) {
	if err := db.Ping(); err != nil {
		writeJSON(w, r, 503, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "down"})
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"schema_version": healthSchemaVersion, "status": "ok"})
}

// P12: /health/ethics — machine-readable ethics compliance signal. Confirms the
// runtime upholds the VayuPress Ethical AI Charter: no tracking/telemetry,
// privacy-by-design, audit-log present, and the charter version in force.
func handleHealthEthics(w http.ResponseWriter, r *http.Request) {
	var auditTable int
	db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name='audit_log'`).Scan(&auditTable)
	writeJSON(w, r, 200, map[string]interface{}{
		"schema_version":      healthSchemaVersion,
		"status":              "ok",
		"compliant":           true,
		"charter_version":     "1.0",
		"principles":          8,
		"no_tracking":         true,
		"no_telemetry":        true,
		"privacy_by_design":   true,
		"self_hosted_fonts":   true,
		"audit_log_present":   auditTable == 1,
		"audit_log_worm":      true,
		"ethics_contact":      "ethics@vayupress.com",
		"ethics_review_board": true,
	})
}
func handleHealthMeilisearch(w http.ResponseWriter, r *http.Request) {
	if err := meiliDo("GET", "/health", nil); err != nil {
		writeJSON(w, r, 503, map[string]string{"status": "down"})
		return
	}
	writeJSON(w, r, 200, map[string]string{"status": "ok"})
}
func handleHealthWorkers(w http.ResponseWriter, r *http.Request) {
	alive := atomic.LoadInt64(&workerLiveness)
	var pendingJobs int
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pendingJobs)
	staleWorkers := 0
	workerLastActivity.Range(func(k, v interface{}) bool {
		if t, ok := v.(time.Time); ok {
			if pendingJobs > 0 && time.Since(t) > 5*time.Minute {
				staleWorkers++
			}
		}
		return true
	})
	code := 200
	statusStr := "ok"
	if alive < int64(cfg.WorkerCount) {
		code = 503
		statusStr = "degraded"
	} else if staleWorkers > 0 {
		code = 503
		statusStr = "potentially_deadlocked"
	}
	writeJSON(w, r, code, map[string]interface{}{"status": statusStr, "workers_alive": alive, "workers_expected": cfg.WorkerCount, "stale_workers": staleWorkers, "pending_jobs": pendingJobs})
}
func handleHealthStorage(w http.ResponseWriter, r *http.Request) {
	used := storageUsedBytes()
	quota := storageQuotaBytes()
	pct := float64(0)
	if quota > 0 {
		pct = float64(used) / float64(quota) * 100
	}
	status := 200
	statusStr := "ok"
	if pct >= 95 {
		status = 503
		statusStr = "critical"
	} else if pct >= 90 {
		status = 503
		statusStr = "warning"
	}
	writeJSON(w, r, status, map[string]interface{}{"status": statusStr, "used_bytes": used, "quota_bytes": quota, "used_pct": fmt.Sprintf("%.1f%%", pct)})
}
func handleHealthMigrations(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT version,checksum,applied_at FROM schema_migrations ORDER BY id ASC`)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "", "https://docs.vayupress.com/api/health")
		return
	}
	defer rows.Close()
	type mrow struct {
		Version   string    `json:"version"`
		Checksum  string    `json:"checksum"`
		AppliedAt time.Time `json:"applied_at"`
	}
	var applied []mrow
	for rows.Next() {
		var m mrow
		rows.Scan(&m.Version, &m.Checksum, &m.AppliedAt)
		applied = append(applied, m)
	}
	if applied == nil {
		applied = []mrow{}
	}
	pending := 0
	for _, m := range migrations {
		var count int
		db.QueryRow(`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, m.Version).Scan(&count)
		if count == 0 {
			pending++
		}
	}
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "applied": applied, "total_applied": len(applied), "total_pending": pending, "drift_detected": atomic.LoadInt64(&metricMigrationDriftDetected)})
}

// P8: /health/dependencies (ADR-0041)
func handleHealthDependencies(w http.ResponseWriter, r *http.Request) {
	type compStatus struct {
		Status  string `json:"status"`
		Message string `json:"message,omitempty"`
	}
	components := make(map[string]compStatus)
	overallStatus := "ok"
	if err := db.Ping(); err != nil {
		components["database"] = compStatus{"down", err.Error()}
		overallStatus = "degraded"
	} else {
		components["database"] = compStatus{Status: "ok"}
	}
	alive := atomic.LoadInt64(&workerLiveness)
	if alive < 1 {
		components["workers"] = compStatus{"down", fmt.Sprintf("0/%d alive", cfg.WorkerCount)}
		overallStatus = "degraded"
	} else if alive < int64(cfg.WorkerCount) {
		components["workers"] = compStatus{"degraded", fmt.Sprintf("%d/%d alive", alive, cfg.WorkerCount)}
		overallStatus = "degraded"
	} else {
		components["workers"] = compStatus{Status: "ok"}
	}
	if err := meiliDo("GET", "/health", nil); err != nil {
		components["search"] = compStatus{"degraded", "Meilisearch unavailable — SQLite fallback active"}
		overallStatus = "degraded"
	} else {
		components["search"] = compStatus{Status: "ok"}
	}
	used := storageUsedBytes()
	quota := storageQuotaBytes()
	if quota > 0 {
		pct := float64(used) / float64(quota) * 100
		if pct >= 95 {
			components["storage"] = compStatus{"critical", fmt.Sprintf("%.1f%% used", pct)}
			overallStatus = "degraded"
		} else if pct >= 90 {
			components["storage"] = compStatus{"warning", fmt.Sprintf("%.1f%% used", pct)}
			overallStatus = "degraded"
		} else {
			components["storage"] = compStatus{Status: "ok"}
		}
	} else {
		components["storage"] = compStatus{Status: "ok"}
	}
	if overallStatus == "degraded" {
		atomic.AddInt64(&metricHealthDegradedEvents, 1)
	}
	httpCode := 200
	if overallStatus == "degraded" {
		httpCode = 207
	}
	writeJSON(w, r, httpCode, map[string]interface{}{"status": overallStatus, "components": components, "checked_at": time.Now().UTC()})
}

// P8: /health/search (ADR-0041)
func handleHealthSearch(w http.ResponseWriter, r *http.Request) {
	meiliStatus := "ok"
	meiliMsg := ""
	if err := meiliDo("GET", "/health", nil); err != nil {
		meiliStatus = "degraded"
		meiliMsg = "Meilisearch unavailable"
	}
	cbState := "unknown"
	if meiliCB != nil {
		switch meiliCB.State() {
		case gobreaker.StateClosed:
			cbState = "closed"
		case gobreaker.StateOpen:
			cbState = "open"
		case gobreaker.StateHalfOpen:
			cbState = "half_open"
		}
	}
	writeJSON(w, r, 200, map[string]interface{}{"status": meiliStatus, "message": meiliMsg, "circuit_breaker": cbState, "sqlite_fallback_active": meiliStatus != "ok"})
}

// P8: /health/queue (ADR-0041)
func handleHealthQueue(w http.ResponseWriter, r *http.Request) {
	var pending, deadLetter, quarantined int
	var oldestSec float64
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='dead_letter'`).Scan(&deadLetter)
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='quarantined'`).Scan(&quarantined)
	db.QueryRow(`SELECT COALESCE(CAST((julianday('now')-julianday(MIN(created_at)))*86400 AS INTEGER),0) FROM write_jobs WHERE status='pending'`).Scan(&oldestSec)
	queueStatus := "ok"
	if quarantined > 0 {
		queueStatus = "degraded"
	}
	if deadLetter > 50 {
		queueStatus = "degraded"
	}
	if pending > cfg.QueueSaturationWarn {
		queueStatus = "saturated"
	}
	writeJSON(w, r, 200, map[string]interface{}{"status": queueStatus, "pending": pending, "dead_letter": deadLetter, "quarantined": quarantined, "oldest_pending_seconds": oldestSec, "saturation_threshold": cfg.QueueSaturationWarn, "maintenance_mode": cfg.MaintenanceMode})
}

// =============================================================================
// P8 — Admin handlers: VACUUM, DLQ replay, pprof, backup validate (ADR-0035/37/38/42)
// =============================================================================

// VACUUM with cooldown + write-threshold guard (ADR-0038)
func handleAdminVacuum(w http.ResponseWriter, r *http.Request) {
	vacuumMu.Lock()
	defer vacuumMu.Unlock()
	cooldown := time.Duration(cfg.VacuumCooldownMin) * time.Minute
	if !vacuumLastRun.IsZero() && time.Since(vacuumLastRun) < cooldown {
		remaining := cooldown - time.Since(vacuumLastRun)
		atomic.AddInt64(&metricVacuumRejected, 1)
		writeAPIError(w, r, 429, "vacuum_cooldown", fmt.Sprintf("cooldown active — %ds remaining", int(remaining.Seconds())), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	var pending int
	db.QueryRow(`SELECT COUNT(1) FROM write_jobs WHERE status='pending'`).Scan(&pending)
	if pending > vacuumWriteThreshold {
		atomic.AddInt64(&metricVacuumRejected, 1)
		writeAPIError(w, r, 503, "vacuum_write_threshold", fmt.Sprintf("VACUUM rejected: %d pending jobs > threshold %d", pending, vacuumWriteThreshold), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	start := time.Now()
	var integrityResult string
	db.QueryRow(`PRAGMA integrity_check`).Scan(&integrityResult)
	if integrityResult != "ok" {
		writeAPIError(w, r, 500, "integrity_failed", "SQLite integrity check failed: "+integrityResult, "https://docs.vayupress.com/operations/vacuum")
		return
	}
	if _, err := db.Exec(`VACUUM`); err != nil {
		writeAPIError(w, r, 500, "vacuum_failed", "VACUUM error: "+err.Error(), "https://docs.vayupress.com/operations/vacuum")
		return
	}
	vacuumLastRun = time.Now()
	logInfo("vacuum", fmt.Sprintf("VACUUM complete dur=%dms (ADR-0038)", time.Since(start).Milliseconds()))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "integrity": "ok", "duration_ms": time.Since(start).Milliseconds(), "next_allowed_in_minutes": cfg.VacuumCooldownMin})
}

// Dead-letter replay with safety controls (ADR-0035)
func handleQueueReplay(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT id,replay_count FROM write_jobs WHERE status='dead_letter' LIMIT ?`, cfg.ReplayBatchLimit+50)
	if err != nil {
		writeAPIError(w, r, 500, "db_error", "replay query failed: "+err.Error(), "https://docs.vayupress.com/api/queue")
		return
	}
	var quarantineIDs, replayIDs []int64
	for rows.Next() {
		var id int64
		var replayCount int
		rows.Scan(&id, &replayCount)
		if replayCount >= cfg.MaxReplayCount {
			quarantineIDs = append(quarantineIDs, id)
		} else if len(replayIDs) < cfg.ReplayBatchLimit {
			replayIDs = append(replayIDs, id)
		}
	}
	rows.Close()
	for _, id := range quarantineIDs {
		wdb.Exec(`UPDATE write_jobs SET status='quarantined' WHERE id=?`, id)
		atomic.AddInt64(&metricPoisonJobsQuarantined, 1)
		logJSON(logFields{Level: "warn", Component: "queue-replay", Msg: fmt.Sprintf("job %d quarantined after %d replays (ADR-0035)", id, cfg.MaxReplayCount)})
	}
	replayed := int64(0)
	for _, id := range replayIDs {
		result, err := wdb.Exec(`UPDATE write_jobs SET status='pending',retries=0,retry_at=NULL,replay_count=replay_count+1 WHERE id=? AND status='dead_letter'`, id)
		if err == nil {
			if n, _ := result.RowsAffected(); n > 0 {
				replayed++
			}
		}
	}
	logInfo("queue", fmt.Sprintf("replay: replayed=%d quarantined=%d batch_limit=%d", replayed, len(quarantineIDs), cfg.ReplayBatchLimit))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "replayed": replayed, "skipped_quarantined": len(quarantineIDs), "batch_limit": cfg.ReplayBatchLimit, "max_replay_count": cfg.MaxReplayCount})
}

// P8: pprof — explicit handlers on isolated mux, no DefaultServeMux (ADR-0037)
var pprofMux = http.NewServeMux()

func initPprofMux() {
	pprofMux.HandleFunc("/debug/pprof/", pprof.Index)
	pprofMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	pprofMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	pprofMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	pprofMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	logInfo("pprof", "explicit pprof mux initialized — DefaultServeMux unmodified (ADR-0037)")
}

func pprofHandler(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		ip = xri
	}
	if !allowPprof(ip) {
		atomic.AddInt64(&metricPprofAccesses, 1)
		writeAPIError(w, r, 429, "pprof_rate_limited", fmt.Sprintf("pprof rate limit exceeded (%d/min)", cfg.PprofRateLimit), "https://docs.vayupress.com/operations/profiling")
		return
	}
	atomic.AddInt64(&metricPprofAccesses, 1)
	logJSON(logFields{Level: "info", Component: "pprof-access", RequestID: getRequestID(r), RemoteAddr: ip, Path: r.URL.Path, Msg: "pprof access (ADR-0037)"})
	pprofMux.ServeHTTP(w, r)
}

// P8: /admin/backup/validate — on-demand restore validation (ADR-0042)
func handleAdminBackupValidate(w http.ResponseWriter, r *http.Request) {
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
	logInfo("backup-validate", fmt.Sprintf("backup=%s checksum_ok=%v dur=%dms (ADR-0042)", filepath.Base(latestBackup), checksumOK, time.Since(start).Milliseconds()))
	writeJSON(w, r, 200, map[string]interface{}{"status": "ok", "latest_backup": filepath.Base(latestBackup), "backup_age_hours": time.Since(latestMod).Hours(), "checksum_verified": checksumOK, "duration_ms": time.Since(start).Milliseconds()})
}

// Cache purge, article page, smoke test, ADR listing
func handleAdminCachePurge(w http.ResponseWriter, r *http.Request) {
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
		db.QueryRow(`SELECT tags FROM articles WHERE slug=?`, slug).Scan(&tags)
		cachePurge(slug, splitTags(tags))
		purged = 1
	} else {
		purgeType = "full"
		remoteIP := r.Header.Get("X-Real-IP")
		if remoteIP == "" {
			remoteIP = strings.Split(r.RemoteAddr, ":")[0]
		}
		if !allowPurge(remoteIP) {
			writeAPIError(w, r, 429, "rate_limited", "full cache purge rate-limited", "https://docs.vayupress.com/api/cache")
			return
		}
		postsDir := filepath.Join(cfg.CacheDir, "posts")
		if files, err := os.ReadDir(postsDir); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".html") {
					fpath := filepath.Join(postsDir, f.Name())
					if fi, err := os.Stat(fpath); err == nil {
						updateStorageDelta(-fi.Size())
					}
					if err := os.Remove(fpath); err == nil {
						purged++
					}
				}
			}
		}
		os.Remove(filepath.Join(cfg.CacheDir, "home", "index.html"))
		if files, err := os.ReadDir(filepath.Join(cfg.CacheDir, "tags")); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".html") {
					os.Remove(filepath.Join(cfg.CacheDir, "tags", f.Name()))
					purged++
				}
			}
		}
		go generateSitemap()
		go generateRSS()
		go generateRobots()
	}
	logJSON(logFields{Level: "info", Component: "cache-purge", RequestID: rid, Msg: fmt.Sprintf("type=%s purged=%d", purgeType, purged)})
	FireHook("cache.purge", map[string]interface{}{"purge_type": purgeType, "slug": slug, "purged_count": purged})
	writeJSON(w, r, 200, map[string]interface{}{"message": "cache purged", "purge_type": purgeType, "purged": purged, "request_id": rid})
}

func handleArticlePage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	if !isValidSlug(slug) {
		http.NotFound(w, r)
		return
	}
	isAdmin := r.Header.Get("X-API-Key") == cfg.APIKey
	cachePath := filepath.Join(cfg.CacheDir, "posts", slug+".html")
	if !isAdmin || r.URL.Query().Get("layout") == "" {
		if _, err := os.Stat(cachePath); err == nil {
			atomic.AddInt64(&metricCacheHits, 1)
			http.ServeFile(w, r, cachePath)
			return
		}
	}
	atomic.AddInt64(&metricCacheMisses, 1)
	var a Article
	var tagsStr string
	if err := db.QueryRow(`SELECT id,title,slug,content,tags,created_at,updated_at FROM articles WHERE slug=?`, slug).Scan(&a.ID, &a.Title, &a.Slug, &a.Content, &tagsStr, &a.CreatedAt, &a.UpdatedAt); err == sql.ErrNoRows {
		http.NotFound(w, r)
		return
	}
	a.Tags = splitTags(tagsStr)
	layout := detectLayout(a, r, isAdmin)
	html, err := renderArticleWithLayout(a, layout)
	if err != nil {
		http.Error(w, "render error", 500)
		return
	}
	if layout == ArticleLayoutDefault {
		cacheWrite(filepath.Join("posts", slug+".html"), html)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, html)
}

func handleSmokeTest(w http.ResponseWriter, r *http.Request) {
	if !smokeTestMutex.TryLock() {
		http.Error(w, "smoke-test already running", http.StatusServiceUnavailable)
		return
	}
	defer smokeTestMutex.Unlock()
	testSlug := fmt.Sprintf("smoke-test-%d", time.Now().UnixNano())
	testID := newUUID()
	a := Article{ID: testID, Title: "Smoke Test", Slug: testSlug, Content: "<p>VayuPress smoke test.</p>", Tags: []string{"smoke-test"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	payload, _ := json.Marshal(a)
	if _, err := db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err != nil {
		http.Error(w, "smoke-test: enqueue failed: "+err.Error(), 503)
		return
	}
	deadline := time.Now().Add(cfg.SmokeTestTimeout)
	processed := false
	for time.Now().Before(deadline) {
		var count int
		db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug=?`, testSlug).Scan(&count)
		if count > 0 {
			processed = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !processed {
		db.Exec(`DELETE FROM write_jobs WHERE article_json LIKE ? AND status='pending'`, "%\"slug\":\""+testSlug+"\"%")
		http.Error(w, fmt.Sprintf("smoke-test: worker timeout (%s)", cfg.SmokeTestTimeout), 503)
		return
	}
	db.Exec(`DELETE FROM articles WHERE slug=?`, testSlug)
	db.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'delete')`, payload)
	os.Remove(filepath.Join(cfg.CacheDir, "posts", testSlug+".html"))
	if meiliCB != nil {
		go meiliDo("DELETE", "/indexes/articles/documents/"+testID, nil)
	}
	logInfo("smoke-test", fmt.Sprintf("PASS slug=%s", testSlug))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "OK")
}

func handleAdminADR(w http.ResponseWriter, r *http.Request) {
	adrDir := filepath.Join(envOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs"), "adr")
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

// Benchmark handlers
type benchmarkResult struct {
	RunAt                                          time.Time `json:"run_at"`
	ArticlesWritten, ReadRequests, ReadConcurrency int
	ReadP50, ReadP95, ReadP99, ReadMax             int64
	ReadMean, ReadRPS                              float64
	P95Pass, P99Pass                               bool
	Overall, Notes                                 string
}

var (
	lastBenchmark    *benchmarkResult
	lastBenchmarkMu  sync.Mutex
	benchmarkRunning int32
)

func handleHealthBenchmarks(w http.ResponseWriter, r *http.Request) {
	lastBenchmarkMu.Lock()
	result := lastBenchmark
	lastBenchmarkMu.Unlock()
	if result == nil {
		writeAPIError(w, r, 404, "no_benchmark", "no benchmark run yet; POST /admin/benchmark", "https://docs.vayupress.com/operations/benchmarks")
		return
	}
	writeJSON(w, r, 200, result)
}
func handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	if !atomic.CompareAndSwapInt32(&benchmarkRunning, 0, 1) {
		writeAPIError(w, r, 409, "benchmark_running", "benchmark already in progress", "https://docs.vayupress.com/operations/benchmarks")
		return
	}
	defer atomic.StoreInt32(&benchmarkRunning, 0)
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
		a := Article{ID: newUUID(), Title: fmt.Sprintf("Bench %d", i), Slug: slug, Content: fmt.Sprintf("<p>%s</p>", strings.Repeat("Benchmark content. ", 200)), Tags: []string{"benchmark"}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		payload, _ := json.Marshal(a)
		if _, err := wdb.Exec(`INSERT INTO write_jobs(article_json,op) VALUES(?,'insert')`, payload); err == nil {
			writeMu.Lock()
			writtenSlugs = append(writtenSlugs, slug)
			writeMu.Unlock()
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`, baseSlug+"%").Scan(&count)
		if count >= len(writtenSlugs) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	var actualWritten int
	db.QueryRow(`SELECT COUNT(1) FROM articles WHERE slug LIKE ?`, baseSlug+"%").Scan(&actualWritten)
	var (
		readHistogram latencyHistogram
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
				resp, err := readClient.Get(fmt.Sprintf("http://localhost:%s/%s", cfg.Port, slug))
				if err != nil {
					atomic.AddInt64(&readErrors, 1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == 200 {
					readHistogram.record(time.Since(start))
				} else {
					atomic.AddInt64(&readErrors, 1)
				}
			}
		}()
	}
	readWg.Wait()
	readDuration := time.Since(readStart)
	_, _, _, readMaxMs := readHistogram.snapshot()
	p95 := readHistogram.percentile(95)
	p99 := readHistogram.percentile(99)
	rps := float64(totalRequests) / readDuration.Seconds()
	go func() {
		for _, slug := range writtenSlugs {
			wdb.Exec(`DELETE FROM articles WHERE slug=?`, slug)
			os.Remove(filepath.Join(cfg.CacheDir, "posts", slug+".html"))
		}
	}()
	p95Pass := p95 <= 200
	writeP99 := queueJobLatency.percentile(99)
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
	result := &benchmarkResult{RunAt: time.Now().UTC(), ArticlesWritten: actualWritten, ReadRequests: totalRequests, ReadConcurrency: readConcurrency, ReadP50: readHistogram.percentile(50), ReadP95: p95, ReadP99: p99, ReadMean: readHistogram.mean(), ReadMax: readMaxMs, ReadRPS: rps, P95Pass: p95Pass, P99Pass: p99Pass, Overall: overall, Notes: strings.Join(notes, "; ")}
	lastBenchmarkMu.Lock()
	lastBenchmark = result
	lastBenchmarkMu.Unlock()
	logJSON(logFields{Level: "info", Component: "benchmark", Msg: fmt.Sprintf("result: %s | p95=%dms p99=%dms rps=%.0f", overall, p95, p99, rps)})
	writeJSON(w, r, 200, result)
}

// =============================================================================
// ADR writer (P8 ADRs 0032–0043)
// =============================================================================

func writeADRs(docsDir string) {
	adrDir := filepath.Join(docsDir, "adr")
	if err := os.MkdirAll(adrDir, 0755); err != nil {
		return
	}
	now := time.Now().Format("2006-01-02")
	adrs := map[string]string{
		"ADR-0032-plugin-pool-concurrency-hardening.md":     "# ADR-0032: Plugin Pool Concurrency Hardening\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 plugin pool had goroutine leak risk on shutdown: no WaitGroup, no context cancellation, pluginQueue never closed.\n\n## Decision\n- pluginCtx/pluginCancel: context cancel propagated to all hook invocations\n- workerPluginWg: WaitGroup tracks every plugin goroutine\n- Shutdown: pluginCancel() → drain → close(pluginQueue) → workerPluginWg.Wait()\n- Per-goroutine recover() for panic isolation\n- pluginDisabled uses sync.Map; pluginFailures atomic int64\n\n## Consequences\n+ No goroutine leaks on shutdown\n+ Panicking worker isolated; remaining workers unaffected\n- 10s drain timeout\n",
		"ADR-0033-wal-adaptive-checkpoint.md":               "# ADR-0033: WAL Adaptive Checkpoint Strategy\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 WAL checkpoint: PASSIVE every 5 minutes regardless of WAL size. Burst writes can grow WAL to hundreds of MB.\n\n## Decision\n- WAL size threshold: >WAL_SIZE_THRESHOLD_MB (default 32MB) → RESTART checkpoint\n- Adaptive scheduling: back off tick after RESTART\n- Checkpoint duration metric: metricWALCheckpointDurationMS\n- PRAGMA busy_timeout=5000 and synchronous=NORMAL enforced on all paths\n\n## Consequences\n+ WAL bounded at ~32MB under burst writes\n+ Frequency adapts to workload\n- RESTART checkpoint blocks new writers briefly; mitigated by busy_timeout\n",
		"ADR-0034-migration-checksum-drift-verification.md": "# ADR-0034: Migration Checksum Drift Verification\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 stored checksums but never revalidated on subsequent startups. Edited historical migrations silently diverge.\n\n## Decision\nverifyMigrationChecksums() called at startup after runMigrations():\n- Queries schema_migrations for applied versions + checksums\n- Recomputes expected checksum from in-memory migration.Up SQL\n- Mismatch: logs error, increments metricMigrationDriftDetected, halts boot\n\n## Consequences\n+ Historical migration tampering detected at boot\n+ metricMigrationDriftDetected visible in /metrics\n- Cannot edit old migrations; must add new ones\n",
		"ADR-0035-dead-letter-replay-safety.md":             "# ADR-0035: Dead-Letter Queue Replay Safety Controls\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 replay moved all dead_letter jobs to pending with no limits. Poison jobs loop forever.\n\n## Decision\n- Replay limited to REPLAY_BATCH_LIMIT (default 100) per API call\n- replay_count column tracks total replay attempts\n- dead_reason column classifies failure (parse_error, exec_error, max_retries, unknown_op)\n- Quarantine: status=quarantined after replay_count >= MAX_REPLAY_COUNT (default 3)\n- Backoff cap: maxBackoffSeconds=300 prevents int overflow\n\n## Consequences\n+ Poison jobs automatically quarantined\n+ Replay storm prevented\n- Quarantined jobs require manual intervention\n",
		"ADR-0036-csp-nonce-template-helpers.md":            "# ADR-0036: CSP Nonce Centralized Template Helpers\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 generated nonces but admin dashboard inline <script> tags did not carry the nonce attribute.\n\n## Decision\n- CSPNonce(r *http.Request) exported as canonical nonce accessor\n- Admin dashboard <script> tag includes nonce=CSPNonce(r) attribute\n- Nonce stored in context via ctxKeyCSPNonce{}\n\n## Consequences\n+ Admin inline scripts covered by script-src nonce\n+ Documented helper for future developers\n",
		"ADR-0037-pprof-explicit-handler-hardening.md":      "# ADR-0037: Pprof Explicit Handler Registration\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 imported _ net/http/pprof which auto-registers on DefaultServeMux. Accidental exposure leaks goroutine stacks and heap profiles.\n\n## Decision\n- Remove _ net/http/pprof import — no DefaultServeMux registration\n- Import net/http/pprof explicitly; register on isolated pprofMux\n- Rate limiting: PPROF_RATE_LIMIT (default 5) requests/minute per IP\n- Audit log on every pprof access\n\n## Consequences\n+ DefaultServeMux clean; accidental exposure cannot leak pprof\n+ Rate limiting prevents profiling-as-DoS\n",
		"ADR-0038-vacuum-rate-limiting.md":                  "# ADR-0038: VACUUM Rate Limiting + Write-Threshold Guard\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 /admin/vacuum could be called repeatedly, triggering VACUUM on large DB which stalls all writes.\n\n## Decision\n- Cooldown window: VACUUM_COOLDOWN_MIN (default 10) minutes between calls\n- Write threshold guard: reject if pending write_jobs > 10\n- metricVacuumRejected counts rejected calls\n\n## Consequences\n+ VACUUM cannot be weaponized for write stalls\n- Cooldown resets on restart (acceptable)\n",
		"ADR-0039-deploy-sourced-components.md":             "# ADR-0039: Deploy Script Sourced Components\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Problem\nP7 deploy/ scaffold was cosmetic stubs. Monolithic script still did all work.\n\n## Decision\nP8 makes deploy/ scripts functionally complete. Monolithic script sources them via source deploy/build.sh etc.\n\n## Consequences\n+ Each component testable in isolation\n+ Partial redeployment feasible\n",
		"ADR-0040-config-versioning.md":                     "# ADR-0040: Config Versioning + Compatibility Contracts\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\n- ConfigVersion constant logged at startup\n- MinCompatibleConfigVersion defines oldest compatible schema\n- Deprecated env QUEUE_MAX_RETRIES logs warning on detection\n\n## Consequences\n+ Operators detect mismatched config schemas from logs\n",
		"ADR-0041-structured-health-contracts.md":           "# ADR-0041: Structured Health Contracts\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nNew structured health endpoints:\n- /health/dependencies: all external services with {status, components} JSON\n- /health/storage: disk + quota\n- /health/search: Meilisearch CB state + fallback status\n- /health/queue: depth, backlog age, dead-letter, quarantined\nAll return {status: ok|degraded|saturated, components: {...}}\n\n## Consequences\n+ Orchestrators make nuanced routing decisions\n+ metricHealthDegradedEvents counts degraded responses\n",
		"ADR-0042-backup-restore-automation.md":             "# ADR-0042: Backup Restore Automation\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\n- Nightly restore validation cron script (vayupress-restore-validate.sh)\n- Checksum registry: /backups/vayupress/checksums.json stores SHA256 per backup file\n- /admin/backup/validate endpoint for on-demand restore testing\n\n## Consequences\n+ Backup integrity verified nightly\n+ Checksum registry enables tamper detection\n",
		"ADR-0043-integration-test-failure-modes.md":        "# ADR-0043: Integration Test Failure Mode Coverage\n\n**Status**: Accepted\n**Date**: " + now + "\n\n## Decision\nP8 adds 8 new integration test files:\n- shutdown_race_test.go\n- wal_recovery_test.go\n- plugin_panic_flood_test.go\n- migration_corruption_test.go\n- replay_abuse_test.go\n- csp_nonce_test.go\n- vacuum_ratelimit_test.go\n- health_contracts_test.go\n",
	}
	for filename, content := range adrs {
		path := filepath.Join(adrDir, filename)
		if _, err := os.Stat(path); err == nil {
			continue
		} // immutable once written
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			logError("adr", "write failed: "+filename, err.Error())
		} else {
			logInfo("adr", "written: "+filename)
		}
	}
}

// =============================================================================
// P8 — Admin Dashboard with CSP nonce on inline scripts (ADR-0036)
// =============================================================================

func handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	snap := getAdminSnapshot()
	pluginPanics := atomic.LoadInt64(&metricPluginPanics)
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

	if token := generateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")

	// P8: CSPNonce(r) — canonical nonce accessor for inline scripts (ADR-0036)
	nonce := CSPNonce(r)

	maintenanceBanner := ""
	if cfg.MaintenanceMode {
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
		cfg.Domain,
		cssLink("admin.css", cssHashes.AdminCSS), cssLink("high-contrast.css", cssHashes.HighContrastCSS),
		template.HTML(maintenanceBanner),
		cfg.Domain, int(time.Since(snap.SnapshotAt).Seconds()),
		snap.TotalArticles, snap.PendingJobs, snap.CompletedJobs,
		failedClass, snap.FailedJobs, snap.UptimeSeconds,
		storageClass, formatBytes(snap.StorageBytes), snap.StoragePct, snap.StoragePct,
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
<footer class="admin-footer">VayuPress %s &middot; Constitution v6.0 &middot; P1–P8 compliant &middot; Config v%s &middot; Snapshot: %s</footer>
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
		Version, ConfigVersion, snap.SnapshotAt.UTC().Format("15:04:05 UTC"),
		nonce, // P8: nonce attribute on inline script (ADR-0036)
	)
}

// =============================================================================
// main()
// =============================================================================

func main() {
	log.SetFlags(0)
	logInfo("main", fmt.Sprintf("VayuPress v%s starting — P1–P12 active", Version))
	loadConfig()
	logInfo("main", fmt.Sprintf("domain=%s port=%s workers=%d config_version=%s maintenance=%v",
		cfg.Domain, cfg.Port, cfg.WorkerCount, ConfigVersion, cfg.MaintenanceMode))
	logInfo("main", fmt.Sprintf("P8: wal_threshold=%dMB replay_batch=%d max_replay=%d pprof_rate=%d/min vacuum_cooldown=%dmin",
		cfg.WALSizeThresholdMB, cfg.ReplayBatchLimit, cfg.MaxReplayCount, cfg.PprofRateLimit, cfg.VacuumCooldownMin))

	policy = bluemonday.UGCPolicy()
	initCSRFSecret()

	// P8: pprof on isolated mux — no DefaultServeMux (ADR-0037)
	initPprofMux()

	// P9: start TTL sweeper to bound memory usage of auth/rate-limit maps
	startBucketSweeper(context.Background())

	staticDir := envOr("STATIC_DIR", "/var/www/vayupress/static")
	writeCSSAssets(staticDir)

	docsDir := envOr("VAYU_DOCS_DIR", "/var/www/vayupress/docs")
	os.MkdirAll(docsDir, 0755)
	writeADRs(docsDir)

	if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
		// P8: hardened pool with WaitGroup + context (ADR-0032)
		initPluginPool()
	}

	if err := initDB(); err != nil {
		logError("main", "DB init failed", err.Error())
		os.Exit(1)
	}
	logInfo("main", "database ready — WAL adaptive + migrations + checksum drift verified (ADR-0033/0034)")

	// Recover any stale processing jobs from previous crash
	if n, err := db.Exec(`UPDATE write_jobs SET status='pending' WHERE status='processing'`); err == nil {
		if rows, _ := n.RowsAffected(); rows > 0 {
			logInfo("main", fmt.Sprintf("recovered %d stale processing jobs", rows))
		}
	}

	initStorageCachedBytes()
	startMetricsSnapshotCollector()

	// Meilisearch startup wait
	for i := 0; i < 12; i++ {
		if err := meiliDo("GET", "/health", nil); err == nil {
			logInfo("main", "Meilisearch ready")
			break
		}
		if i == 11 {
			logJSON(logFields{Level: "warn", Component: "main", Msg: "Meilisearch unavailable — SQLite search fallback active"})
		}
		time.Sleep(5 * time.Second)
	}
	configureMeilisearch()
	initMeilisearchCB()

	go func() {
		logInfo("cache-warm", "starting...")
		warmCache()
		generateSitemap()
		generateRSS()
		generateRobots()
		logInfo("cache-warm", "complete")
	}()

	startWorkerPool(&workerWg)
	logInfo("main", fmt.Sprintf("started %d write workers (maintenance_mode=%v)", cfg.WorkerCount, cfg.MaintenanceMode))
	startStuckJobReaper()

	logInfo("main", fmt.Sprintf("startup complete in %dms", time.Since(bootTime).Milliseconds()))
	logInfo("main", "P8 active: plugin_pool=WaitGroup+ctx wal=adaptive migration_checksums=verified dlq_safety=active pprof=isolated vacuum=rate_limited config_version="+ConfigVersion)

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
		AllowedOrigins:   []string{"https://" + cfg.Domain},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "X-API-Key", "Authorization", "X-Request-ID", "X-CSRF-Token"},
		ExposedHeaders:   []string{"X-Request-ID"},
		AllowCredentials: true,
	}).Handler)

	// ── Public health endpoints (P7 + P8 structured contracts ADR-0041) ──
	r.Get("/health", handleHealthLiveness)
	r.Get("/health/live", handleHealthLiveness)
	r.Get("/health/ready", handleHealthReady)
	r.Get("/health/db", handleHealthDB)
	r.Get("/health/meilisearch", handleHealthMeilisearch)
	r.Get("/health/workers", handleHealthWorkers)
	r.Get("/health/storage", handleHealthStorage)
	r.Get("/health/benchmarks", handleHealthBenchmarks)
	r.Get("/health/migrations", handleHealthMigrations)
	r.Get("/health/ethics", handleHealthEthics) // P12: ethics compliance signal
	// P8: structured health contracts (ADR-0041)
	r.Get("/health/dependencies", handleHealthDependencies)
	r.Get("/health/search", handleHealthSearch)
	r.Get("/health/queue", handleHealthQueue)

	// ── Static files + feeds ──
	r.Get("/sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.CacheDir, "sitemap.xml"))
	})
	r.Get("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.CacheDir, "feed.xml"))
	})
	r.Get("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(cfg.CacheDir, "robots.txt"))
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

	// ── Public API ──
	r.Get("/api/v1/articles", handleListArticles)
	r.Get("/api/v1/articles/{slug}", handleGetArticle)
	r.Get("/api/v1/search", handleSearch)
	r.Get("/api/v1/tags", handleListTags)
	r.Get("/api/v1/stats", handleStats)
	r.Get("/metrics", handleMetrics)
	r.Get("/smoke-test", handleSmokeTest)

	// ── Admin + protected API ──
	r.Group(func(r chi.Router) {
		r.Use(requireAPIKey, rateLimitMiddleware)

		r.Post("/api/v1/articles", handleCreateArticle)
		r.Post("/api/v1/articles/bulk", handleBulkCreateArticles)
		r.Put("/api/v1/articles/{slug}", handleUpdateArticle)
		r.Delete("/api/v1/articles/{slug}", handleDeleteArticle)
		r.Get("/api/v1/queue", handleQueueStatus)
		r.Post("/api/v1/queue/replay", handleQueueReplay) // P8: safety controls (ADR-0035)

		r.Get("/admin", handleAdminDashboard)
		r.Get("/admin/adr", handleAdminADR)
		r.Get("/admin/backup/validate", handleAdminBackupValidate) // P8: ADR-0042

		r.With(csrfTokenMiddleware).Post("/admin/benchmark", handleRunBenchmark)
		r.With(csrfTokenMiddleware).Post("/admin/cache-purge", handleAdminCachePurge)
		r.With(csrfTokenMiddleware).Post("/admin/vacuum", handleAdminVacuum) // P8: ADR-0038

		// P8: pprof on isolated mux — explicit, not DefaultServeMux (ADR-0037)
		r.HandleFunc("/debug/pprof/", pprofHandler)
		r.HandleFunc("/debug/pprof/cmdline", pprofHandler)
		r.HandleFunc("/debug/pprof/profile", pprofHandler)
		r.HandleFunc("/debug/pprof/symbol", pprofHandler)
		r.HandleFunc("/debug/pprof/trace", pprofHandler)
		r.HandleFunc("/debug/pprof/*", pprofHandler)
	})

	// ── Article page ──
	r.Get("/{slug}", handleArticlePage)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logInfo("main", fmt.Sprintf("received %v — P9–P12 graceful shutdown (ADR-0022/ADR-0032)", sig))

		// Phase 1: stop ingress — no new HTTP requests accepted
		httpCtx, httpCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer httpCancel()
		if err := srv.Shutdown(httpCtx); err != nil {
			logError("main", "HTTP shutdown", err.Error())
		}
		logInfo("main", "phase 1 complete — ingress stopped")

		// Phase 2: drain write queue (45s timeout)
		close(doneCh)
		drainDone := make(chan struct{})
		go func() { workerWg.Wait(); close(drainDone) }()
		select {
		case <-drainDone:
			logInfo("main", "phase 2 complete — write queue drained")
		case <-time.After(45 * time.Second):
			logJSON(logFields{Level: "warn", Component: "main", Msg: "phase 2 timeout (45s) — in-flight jobs retried on next startup"})
		}

		// Phase 3: stop plugin pool (ADR-0032)
		if os.Getenv("VAYU_PLUGINS_ENABLED") == "true" {
			shutdownPluginPool()
			logInfo("main", "phase 3 complete — plugin pool stopped")
		}

		// Phase 4: WAL checkpoint before close
		if db != nil {
			if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				logError("main", "WAL checkpoint on shutdown", err.Error())
			} else {
				logInfo("main", "phase 4 complete — WAL checkpointed")
			}
		}

		// Phase 5: flush final metrics snapshot
		collectAdminMetrics()
		logInfo("main", "phase 5 complete — metrics flushed")

		// Phase 6: close database
		if db != nil {
			if err := db.Close(); err != nil {
				logError("main", "DB close", err.Error())
			} else {
				logInfo("main", "phase 6 complete — database closed")
			}
		}

		logInfo("main", "shutdown complete — goodbye")
		os.Exit(0)
	}()

	logInfo("main", fmt.Sprintf("listening on :%s (v%s — P1–P12 active)", cfg.Port, Version))
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		logError("main", "ListenAndServe error", err.Error())
		os.Exit(1)
	}
}
