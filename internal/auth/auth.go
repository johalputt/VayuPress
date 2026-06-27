// Package auth provides API key middleware, CSRF, rate limiting, Argon2id helpers,
// and the TTL bucket sweeper that prevents unbounded memory growth (ADR-0021/P9).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"golang.org/x/crypto/argon2"
)

// ClientIP derives the real client IP from r, honouring X-Forwarded-For /
// X-Real-IP **only** when the immediate peer (r.RemoteAddr) is a configured
// trusted proxy (config.Cfg.TrustedProxies, default loopback). For a direct
// connection from any other address the forwarding headers are ignored
// entirely, so a client cannot spoof its IP to evade rate limiting / lockout or
// impersonate a TRUSTED_IPS entry (audit F-3, GHSA-3fxj-6jh8-hvhx).
//
// When the peer is trusted, X-Real-IP wins; otherwise the right-most
// X-Forwarded-For entry that is not itself a trusted proxy is used (the address
// the outermost trusted proxy actually saw).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	if peer == nil || !ipIsTrustedProxy(peer) {
		return host // direct / untrusted peer: never trust forwarding headers
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return xri
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		for i := len(parts) - 1; i >= 0; i-- {
			cand := strings.TrimSpace(parts[i])
			ip := net.ParseIP(cand)
			if ip == nil {
				continue
			}
			if ipIsTrustedProxy(ip) {
				continue // skip our own proxy hops, keep walking left
			}
			return cand
		}
	}
	return host
}

// ipIsTrustedProxy reports whether ip falls within any configured trusted-proxy
// CIDR range.
func ipIsTrustedProxy(ip net.IP) bool {
	for _, n := range config.Cfg.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ── Auth fail lockout (ADR-0021) ─────────────────────────────────────────────

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

// CheckAuthLockout returns whether the IP is currently locked out and until when.
func CheckAuthLockout(ip string) (bool, time.Time) {
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

// RecordAuthFailure increments the failure counter for the IP.
func RecordAuthFailure(ip string) {
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
		atomic.AddInt64(&metrics.MetricAuthLockouts, 1)
		logging.LogJSON(logging.LogFields{Level: "warn", Component: "auth-lockout", Msg: fmt.Sprintf("IP %s locked out for %s", ip, authLockDuration)})
	}
}

// RecordAuthSuccess clears failure state for the IP.
func RecordAuthSuccess(ip string) {
	b := getAuthFailBucket(ip)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.lockedUntil = time.Time{}
}

// ── Rate limiting ─────────────────────────────────────────────────────────────

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
	for _, ip := range strings.Split(config.EnvOr("TRUSTED_IPS", ""), ",") {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			m[ip] = true
		}
	}
	return m
}

// RateLimitMiddleware enforces 100 requests/hour per IP (trusted IPs bypassed).
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
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
			writeAuthError(w, 429, "rate_limit_exceeded", "rate limit exceeded (100 req/hour)", "https://docs.vayupress.com/api/rate-limiting")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ── Pprof rate limiter (ADR-0037) ────────────────────────────────────────────

type pprofBucket struct {
	count     int
	windowEnd time.Time
	mu        sync.Mutex
}

var pprofLimiters sync.Map

// AllowPprof returns true if the given IP is within the pprof rate limit.
func AllowPprof(ip string) bool {
	v, _ := pprofLimiters.LoadOrStore(ip, &pprofBucket{})
	b := v.(*pprofBucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.After(b.windowEnd) {
		b.count = 0
		b.windowEnd = now.Add(time.Minute)
	}
	if b.count >= config.Cfg.PprofRateLimit {
		return false
	}
	b.count++
	return true
}

// ── CSP report rate limiter ───────────────────────────────────────────────────

// cspReportLimitPerMin caps accepted CSP violation reports per IP per minute.
// The /csp-report endpoint is public and unauthenticated, so this bounds log
// spam, metric inflation, and ingestion abuse from a single source.
const cspReportLimitPerMin = 30

type cspBucket struct {
	count     int
	windowEnd time.Time
	mu        sync.Mutex
}

var cspLimiters sync.Map

// AllowCSPReport returns true if the given IP is within the CSP-report rate
// limit (fixed window, per minute). Over-limit reports are dropped entirely —
// neither counted nor logged — so abuse cannot inflate metrics or logs.
func AllowCSPReport(ip string) bool {
	v, _ := cspLimiters.LoadOrStore(ip, &cspBucket{})
	b := v.(*cspBucket)
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if now.After(b.windowEnd) {
		b.count = 0
		b.windowEnd = now.Add(time.Minute)
	}
	if b.count >= cspReportLimitPerMin {
		return false
	}
	b.count++
	return true
}

// ── Cache purge rate limiter ──────────────────────────────────────────────────

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

// AllowPurge returns true if the given IP may perform a cache purge (token bucket).
func AllowPurge(ip string) bool {
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

// ── Bucket sweeper (P9 memory safety) ────────────────────────────────────────

// StartBucketSweeper removes expired auth/rate-limit/pprof/purge buckets every 10 minutes
// to bound memory usage on long-running instances with rotating IPs (P9/ADR-0021).
func StartBucketSweeper(ctx context.Context) {
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
				cspLimiters.Range(func(k, v interface{}) bool {
					if b, ok := v.(*cspBucket); ok {
						b.mu.Lock()
						old := now.After(b.windowEnd)
						b.mu.Unlock()
						if old {
							cspLimiters.Delete(k)
						}
					}
					return true
				})
			}
		}
	}()
}

// ── Argon2id credential hashing (P9) ─────────────────────────────────────────

// Argon2id parameters (audit F-5). OWASP recommends a minimum of t=2 for a
// 64 MiB configuration; we use t=3 for additional offline-cracking cost.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32

	// legacyArgonTime is the time cost used before the F-5 bump. Encoded hashes
	// produced then carry no parameter metadata (plain "salt$hash"), so they are
	// verified with this value to remain valid without a migration.
	legacyArgonTime = 1
	// argonV2Prefix tags the parameterised encoding so future cost changes stay
	// backward compatible: "argon2id$v=2$t=<N>$<salt>$<hash>".
	argonV2Prefix = "argon2id"
)

// HashSecretArgon2id derives an Argon2id hash. The returned string embeds the
// time cost ("argon2id$v=2$t=<N>$<salt>$<hash>") so the parameters that
// produced it are always available at verification time, allowing the cost to
// be raised in future without invalidating stored hashes.
func HashSecretArgon2id(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("%s$v=2$t=%d$%s$%s",
		argonV2Prefix, argonTime,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifySecretArgon2id performs a constant-time comparison of secret against an
// encoded hash. It accepts both the parameterised v2 form and the legacy
// "salt$hash" form (verified with the legacy time cost), so hashes created
// before the F-5 cost bump keep working.
func VerifySecretArgon2id(secret, encoded string) bool {
	t := uint32(legacyArgonTime)
	saltB64, hashB64 := "", ""

	if strings.HasPrefix(encoded, argonV2Prefix+"$") {
		// argon2id$v=2$t=<N>$<salt>$<hash>
		parts := strings.Split(encoded, "$")
		if len(parts) != 5 || parts[1] != "v=2" || !strings.HasPrefix(parts[2], "t=") {
			return false
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(parts[2], "t="), 10, 32)
		if err != nil || n == 0 {
			return false
		}
		t = uint32(n)
		saltB64, hashB64 = parts[3], parts[4]
	} else {
		// Legacy: salt$hash
		parts := strings.SplitN(encoded, "$", 2)
		if len(parts) != 2 {
			return false
		}
		saltB64, hashB64 = parts[0], parts[1]
	}

	salt, err := base64.RawStdEncoding.DecodeString(saltB64)
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(hashB64)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, t, argonMemory, argonThreads, argonKeyLen)
	return hmac.Equal(got, want)
}

// ── CSRF (ADR-0013/0016/0017) ─────────────────────────────────────────────────

var csrfSecret []byte

// InitCSRFSecret generates a fresh 32-byte CSRF secret for this process run.
func InitCSRFSecret() {
	csrfSecret = make([]byte, 32)
	if _, err := rand.Read(csrfSecret); err != nil {
		logging.LogError("csrf", "failed to generate CSRF secret", err.Error())
		os.Exit(1)
	}
	logging.LogInfo("csrf", "CSRF secret initialized (32 bytes)")
}

// CSRFCookieSecure reports whether auth/CSRF cookies should carry the Secure
// attribute. It is the single source of truth shared by the auth package and
// the cmd layer (audit F-7). Override with CSRF_SECURE_COOKIE=true|false;
// otherwise Secure is set whenever the site is not served on localhost.
func CSRFCookieSecure() bool {
	if v := os.Getenv("CSRF_SECURE_COOKIE"); v != "" {
		return v == "true"
	}
	return config.Cfg.Domain != "localhost"
}

// GenerateCSRFToken creates a signed CSRF token.
func GenerateCSRFToken() string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	token := hex.EncodeToString(raw)
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(token))
	sig := hex.EncodeToString(mac.Sum(nil))
	// RawURLEncoding (no '=' padding) so the token is safe to carry in a cookie
	// and read back in JS. base64.URLEncoding adds a trailing '=' which naive
	// `cookie.split('=')[1]` parsers strip, breaking the double-submit match.
	return base64.RawURLEncoding.EncodeToString([]byte(token + "." + sig))
}

// ValidateCSRFToken verifies a token produced by GenerateCSRFToken.
func ValidateCSRFToken(token string) bool {
	if token == "" {
		return false
	}
	// Accept both the current padding-free form and any legacy padded token
	// still sitting in a browser cookie (they regenerate on the next page load).
	token = strings.TrimRight(token, "=")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
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

// CSRFTokenMiddleware issues CSRF tokens on GET and validates them on mutating requests.
func CSRFTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Re-issue the cookie when it is missing, empty, OR no longer valid.
			// A token can become invalid after the CSRF secret rotates (every
			// process restart) or after the 1h cookie lifetime. Without the
			// validity check a *stale* cookie is left untouched, so reloading
			// the page never recovers it and every subsequent POST 403s — the
			// "session token expired — reload" loop the operator can't escape.
			needsToken := true
			if c, err := r.Cookie("vp_csrf"); err == nil && c.Value != "" && ValidateCSRFToken(c.Value) {
				needsToken = false
			}
			if needsToken {
				if token := GenerateCSRFToken(); token != "" {
					http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: CSRFCookieSecure(), MaxAge: 3600})
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
			// Constant-time double-submit comparison: avoid leaking how much of
			// the token matched via response timing. ValidateCSRFToken separately
			// verifies the HMAC so a forged-but-matching pair still fails.
			tokensMatch := subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) == 1
			if headerToken == "" || cookieToken == "" || !tokensMatch || !ValidateCSRFToken(headerToken) {
				writeAuthError(w, 403, "csrf_invalid", "CSRF token missing or invalid", "https://docs.vayupress.com/api/csrf")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ── API key middleware ────────────────────────────────────────────────────────

// extraAPIKeyVerifier is an optional, runtime-registered verifier for
// database-backed API keys (issued/rotated from the VayuOS admin panel; see
// internal/apikeys). It is consulted in addition to the static bootstrap
// API_KEY so operators can mint and revoke keys without a restart. Guarded by a
// mutex so registration during startup is race-free against request handling.
var (
	extraAPIKeyMu       sync.RWMutex
	extraAPIKeyVerifier func(presentedKey string) bool
)

// SetExtraAPIKeyVerifier registers (or clears, with nil) the additional API-key
// verifier consulted by RequireAPIKey and HasValidAPIKey. Wired in main once the
// API-key store is ready.
func SetExtraAPIKeyVerifier(fn func(presentedKey string) bool) {
	extraAPIKeyMu.Lock()
	extraAPIKeyVerifier = fn
	extraAPIKeyMu.Unlock()
}

// verifyExtraAPIKey reports whether a registered verifier accepts the key.
func verifyExtraAPIKey(key string) bool {
	if key == "" {
		return false
	}
	extraAPIKeyMu.RLock()
	fn := extraAPIKeyVerifier
	extraAPIKeyMu.RUnlock()
	return fn != nil && fn(key)
}

// RequireAPIKey is HTTP middleware that validates X-API-Key / Authorization: Bearer headers.
func RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		if locked, until := CheckAuthLockout(ip); locked {
			retryAfter := int(time.Until(until).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeAuthError(w, 429, "auth_lockout", fmt.Sprintf("locked out for %ds", retryAfter), "https://docs.vayupress.com/api/auth#lockout")
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		// Constant-time comparison prevents timing attacks that could otherwise
		// leak the configured key one byte at a time. An empty configured key is
		// never a valid credential, even against an empty presented key. A
		// database-backed key (issued from VayuOS) is accepted as a fallback.
		staticOK := config.Cfg.APIKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(config.Cfg.APIKey)) == 1
		if !staticOK && !verifyExtraAPIKey(key) {
			RecordAuthFailure(ip)
			writeAuthError(w, 401, "unauthorized", "invalid or missing API key", "https://docs.vayupress.com/api/auth")
			return
		}
		RecordAuthSuccess(ip)
		next.ServeHTTP(w, r)
	})
}

// writeAuthError writes a minimal JSON error response from the auth package.
func writeAuthError(w http.ResponseWriter, code int, errCode, msg, docs string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	type authErr struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Docs    string `json:"docs"`
	}
	enc := json.NewEncoder(w)
	enc.Encode(map[string]authErr{"error": {Code: errCode, Message: msg, Docs: docs}})
}
