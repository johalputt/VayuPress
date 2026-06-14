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
			}
		}
	}()
}

// ── Argon2id credential hashing (P9) ─────────────────────────────────────────

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

// HashSecretArgon2id derives an Argon2id hash, returning a "salt$hash" base64 string.
func HashSecretArgon2id(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash), nil
}

// VerifySecretArgon2id performs a constant-time comparison of secret against an encoded hash.
func VerifySecretArgon2id(secret, encoded string) bool {
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

func csrfCookieSecure() bool {
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
	return base64.URLEncoding.EncodeToString([]byte(token + "." + sig))
}

// ValidateCSRFToken verifies a token produced by GenerateCSRFToken.
func ValidateCSRFToken(token string) bool {
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

// CSRFTokenMiddleware issues CSRF tokens on GET and validates them on mutating requests.
func CSRFTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if c, err := r.Cookie("vp_csrf"); err != nil || c.Value == "" {
				if token := GenerateCSRFToken(); token != "" {
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
			if headerToken == "" || cookieToken == "" || headerToken != cookieToken || !ValidateCSRFToken(headerToken) {
				writeAuthError(w, 403, "csrf_invalid", "CSRF token missing or invalid", "https://docs.vayupress.com/api/csrf")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ── API key middleware ────────────────────────────────────────────────────────

// RequireAPIKey is HTTP middleware that validates X-API-Key / Authorization: Bearer headers.
func RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := r.RemoteAddr
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			ip = xri
		} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = strings.TrimSpace(strings.Split(xff, ",")[0])
		}
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
		// never a valid credential, even against an empty presented key.
		if config.Cfg.APIKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(config.Cfg.APIKey)) != 1 {
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
