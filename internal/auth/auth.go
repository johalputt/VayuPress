// SPDX-License-Identifier: Apache-2.0

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
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
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
	// Cloudflare / CDN: CF-Connecting-IP is the single real visitor IP the edge
	// observed; True-Client-IP is the Enterprise/Akamai equivalent. They are
	// honoured ONLY when the immediate peer is genuinely one of those edges.
	//
	// That peer check is the whole security of this branch, and it used to be
	// missing. "The peer is a trusted proxy" was treated as sufficient, but on
	// every ordinary install the peer is the LOCAL nginx — trusted because
	// TRUSTED_PROXIES defaults to loopback, and nginx forwards unknown request
	// headers untouched. So any visitor could send `CF-Connecting-IP: 1.2.3.4`
	// and become 1.2.3.4 for every purpose that keys on the client address: the
	// rate limiter, the reputation jail, auto-block, and the auth lockout. Rotate
	// the header per request and none of them can ever fire. It applied whether
	// or not the "behind a CDN" switch was on, because that switch only widened
	// which peers count as trusted — it never gated this read.
	//
	// With a local reverse proxy in front, nginx is the authority instead: it
	// overwrites X-Real-IP from the real connection (proxy_set_header X-Real-IP
	// $remote_addr), so a client cannot influence it. An operator behind a CDN
	// must therefore give nginx set_real_ip_from + real_ip_header for their edge
	// ranges, so nginx resolves the visitor before VayuPress ever sees it — see
	// deploy/nginx-vayupress.conf.
	if config.TrustCloudflareEnabled() && config.IsCloudflareIP(peer) {
		if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
			if net.ParseIP(cf) != nil {
				return cf
			}
		}
		if tc := strings.TrimSpace(r.Header.Get("True-Client-IP")); tc != "" {
			if net.ParseIP(tc) != nil {
				return tc
			}
		}
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

// PeerIsTrustedProxy reports whether r's immediate peer is a configured trusted
// proxy — the same test ClientIP applies before it will honour any forwarding
// header.
//
// It exists because the address is not the only thing a proxy asserts about a
// visitor. A CDN also states their COUNTRY (CF-IPCountry and its equivalents),
// and that header was being read straight off the request by the analytics
// ingest — which is public, unauthenticated and exempt from the shield — so any
// client could file itself under any country. ClientIP was hardened against
// exactly this for the address; the country needs the same gate rather than a
// second copy of the rule.
func PeerIsTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	return peer != nil && ipIsTrustedProxy(peer)
}

// ipIsTrustedProxy reports whether ip falls within any configured trusted-proxy
// CIDR range.
func ipIsTrustedProxy(ip net.IP) bool {
	for _, n := range config.Cfg.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	// "Behind Cloudflare/CDN" mode: Cloudflare's published edge ranges are trusted
	// so CF-Connecting-IP is honoured, without the operator hand-maintaining every
	// Cloudflare CIDR in TRUSTED_PROXIES.
	if config.TrustCloudflareEnabled() && config.IsCloudflareIP(ip) {
		return true
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

	// Separate, more generous bucket set for the unauthenticated public
	// discovery endpoints (WKD, mail autoconfig). Kept apart from the API limiter
	// so the two never share a budget.
	discoveryMu      sync.Mutex
	discoveryBuckets = make(map[string]*ipBucket)
	// discoveryLimit is requests per 10-minute window per IP. The default is
	// deliberately high so ordinary mail-client key discovery is never throttled;
	// it exists to cap abusive enumeration / DoS of the O(keys) WKD scan.
	discoveryLimit = config.GetEnvAsInt("DISCOVERY_RATE_LIMIT", 240)
)

// PublicDiscoveryRateLimit throttles the unauthenticated .well-known discovery
// endpoints per client IP. The WKD handler scans the whole keystore on every
// request, so an unbounded query rate is a DoS amplifier. The limit is generous
// (default 240 / 10 min) so legitimate discovery is unaffected; trusted IPs
// bypass it entirely. Unlike the API limiter it answers with a bare
// 429 + Retry-After, since these endpoints serve binary/XML/JSON to arbitrary
// clients rather than the JSON API error envelope.
func PublicDiscoveryRateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		if trustedIPs[ip] {
			next.ServeHTTP(w, r)
			return
		}
		discoveryMu.Lock()
		b, ok := discoveryBuckets[ip]
		if !ok || time.Now().After(b.resetAt) {
			b = &ipBucket{1, time.Now().Add(10 * time.Minute)}
			discoveryBuckets[ip] = b
		} else {
			b.count++
		}
		allowed := b.count <= discoveryLimit
		resetAt := b.resetAt
		discoveryMu.Unlock()
		if !allowed {
			retry := int(time.Until(resetAt).Seconds()) + 1
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limit exceeded\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
			writeAuthError(w, 429, "rate_limit_exceeded", "rate limit exceeded (100 req/hour)", "/docs/api/rate-limiting")
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
				discoveryMu.Lock()
				for ip, b := range discoveryBuckets {
					if now.After(b.resetAt) {
						delete(discoveryBuckets, ip)
					}
				}
				discoveryMu.Unlock()
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
	// Hashing shares the verification ceiling: a password change and a sign-in
	// cost the same 64 MiB, and counting only one of them would leave the bound
	// describing half the work.
	var hash []byte
	if !withArgonSlot(func() {
		hash = argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	}) {
		return "", errors.New("the server is busy hashing credentials; try again shortly")
	}
	return fmt.Sprintf("%s$v=2$t=%d$%s$%s",
		argonV2Prefix, argonTime,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifySecretArgon2id performs a constant-time comparison of secret against an
// encoded hash. It accepts both the parameterised v2 form and the legacy
// "salt$hash" form (verified with the legacy time cost), so hashes created
// before the F-5 cost bump keep working.
// decoyArgon2Hash is a fixed valid encoded hash used to spend equivalent Argon2id
// time when a caller passes an EMPTY encoded hash — what a credential lookup
// returns for a non-existent or inactive account. Without it, the empty-hash path
// returns in microseconds while a real hash runs the full ~tens-of-ms KDF, a
// timing oracle that reveals whether an account/mailbox exists (audit M4).
var decoyArgon2Hash = func() string {
	h, err := HashSecretArgon2id("vayupress-timing-decoy")
	if err != nil {
		return ""
	}
	return h
}()

func VerifySecretArgon2id(secret, encoded string) bool {
	// Constant-time account existence (audit M4): verify an empty hash against the
	// decoy so the non-existent-account path spends the same Argon2id time as a
	// wrong password, then force failure so the decoy can never authenticate.
	forceFail := false
	if encoded == "" && decoyArgon2Hash != "" {
		encoded = decoyArgon2Hash
		forceFail = true
	}
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
	// The derivation itself runs under the process-wide ceiling (argon_ceiling.go).
	// Everything above is parsing and costs nothing; only this line allocates
	// 64 MiB and burns cores, so it is the only part worth bounding.
	var got []byte
	if !withArgonSlot(func() {
		got = argon2.IDKey([]byte(secret), salt, t, argonMemory, argonThreads, argonKeyLen)
	}) {
		return false
	}
	return hmac.Equal(got, want) && !forceFail
}

// ── CSRF (ADR-0013/0016/0017) ─────────────────────────────────────────────────

var csrfSecret []byte

// csrfSecretPath returns the file the CSRF secret is persisted to, or "" when no
// stable location is known (then the secret is per-process). Override with
// VAYU_CSRF_SECRET_FILE; otherwise it lives beside the database — a stable,
// service-owned, private location.
func csrfSecretPath() string {
	if p := strings.TrimSpace(os.Getenv("VAYU_CSRF_SECRET_FILE")); p != "" {
		return p
	}
	if db := strings.TrimSpace(config.Cfg.DBPath); db != "" {
		return filepath.Join(filepath.Dir(db), ".vayu-csrf-secret")
	}
	return ""
}

// InitCSRFSecret loads the persisted 32-byte CSRF secret, or generates and
// persists one on first run. Persisting is essential: without it the secret is
// regenerated on every process start, so every restart — including the one the
// in-app self-update performs — invalidates every outstanding CSRF token and the
// next admin action fails with "CSRF token missing or invalid". A stable secret
// keeps tokens valid across restarts. Falls back to a per-process secret if the
// file cannot be read or written (e.g. read-only dir), preserving old behaviour.
func InitCSRFSecret() {
	path := csrfSecretPath()
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) >= 32 {
			csrfSecret = b[:32]
			logging.LogInfo("csrf", "CSRF secret loaded from "+path+" (stable across restarts)")
			return
		}
	}
	csrfSecret = make([]byte, 32)
	if _, err := rand.Read(csrfSecret); err != nil {
		logging.LogError("csrf", "failed to generate CSRF secret", err.Error())
		os.Exit(1)
	}
	if path != "" {
		if err := os.WriteFile(path, csrfSecret, 0o600); err != nil {
			logging.LogError("csrf", "could not persist CSRF secret — tokens will reset on restart", err.Error())
		} else {
			logging.LogInfo("csrf", "CSRF secret generated and persisted to "+path)
		}
	} else {
		logging.LogInfo("csrf", "CSRF secret initialized (per-process; set VAYU_CSRF_SECRET_FILE to persist)")
	}
}

// CSRFCookieSecure reports whether auth/CSRF cookies should carry the Secure
// attribute. It is the single source of truth shared by the auth package and the
// cmd layer (audit F-7), and it is now unconditionally true.
//
// Rationale (corrects the earlier OnionMode exception): every transport VayuPress
// is served on to a real client is one where a Secure cookie is stored and sent —
//   - clearnet is HTTPS (TLS direct, or the browser↔proxy leg is HTTPS behind a
//     TLS-terminating reverse proxy), and
//   - a Tor Space is served only to Tor Browser (ADR-0141), which treats a
//     v3 `.onion` as a potentially-trustworthy origin
//     (`dom.securecontext.whitelist_onions`) and therefore stores/sends Secure
//     cookies over the plain-http `.onion`.
//
// There is therefore no transport on which a real client silently drops the
// cookie, so Secure is always on — the strongest posture, and it closes the
// CodeQL "Secure attribute is not set to true" finding with no runtime exception.
func CSRFCookieSecure() bool {
	return true
}

// WriteSecureCookie writes a session/auth cookie through the single hardened
// policy: HttpOnly is FORCED on (defense in depth — a session cookie routed here
// can never accidentally be script-readable) and Secure is FORCED on (see
// CSRFCookieSecure for why this is safe on every supported transport, including a
// Tor Browser `.onion`).
//
// Cookies that MUST be script-readable (the double-submit CSRF token) use
// WriteReadableCookie instead — they are never routed here.
func WriteSecureCookie(w http.ResponseWriter, c *http.Cookie) {
	c.HttpOnly = true
	c.Secure = true
	http.SetCookie(w, c)
}

// WriteReadableCookie writes a cookie the page script is MEANT to read — only
// the double-submit CSRF token, whose whole purpose is to be echoed back in the
// X-CSRF-Token header, so HttpOnly is deliberately left off. Secure is still
// FORCED on (safe on every supported transport, incl. a Tor Browser `.onion` —
// see CSRFCookieSecure). Kept distinct from WriteSecureCookie so the readable
// exception is explicit and auditable in one place.
func WriteReadableCookie(w http.ResponseWriter, c *http.Cookie) {
	c.HttpOnly = false // double-submit CSRF: the page script must read this token
	c.Secure = true
	http.SetCookie(w, c)
}

// csrfTokenTTL bounds how long a minted token stays acceptable. The cookie has
// carried MaxAge:3600 all along, but that lived only in the browser — a token
// copied out of it was good forever, because nothing server-side recorded when
// it was issued. Now it is inside the signature.
const csrfTokenTTL = time.Hour

// GenerateCSRFToken creates a signed CSRF token BOUND to one session and one
// moment.
//
// AUDIT FINDING (Section 1). The token used to be b64url(nonce + "." +
// HMAC(nonce)) and validation recomputed exactly that — no principal, no
// issued-at. A token minted for one person was byte-for-byte acceptable for
// another, and it never expired server-side. The middleware's own comment
// claimed the secret "rotates (every process restart)", which InitCSRFSecret
// made untrue years ago by persisting it to disk.
//
// The reported exploit chain does NOT stand in this codebase — it needs an
// attacker-controlled cookie write on a sibling origin of the panel, and the
// custom-site upload routes are admin-gated by a settled product decision, with
// SameSite=Strict blocking every cross-site POST regardless. This is defence in
// depth for the operator who later serves third-party content on a subdomain of
// the panel's registrable domain, or who has a plain-HTTP sibling host there: a
// network attacker can set a parent-domain cookie from an http sibling, and
// RFC 6265 §5.4 orders the longer Path first, so r.Cookie would return theirs.
//
// binding is an opaque per-principal string (the session token's hash). An empty
// binding is legitimate — the sign-in page has no session yet — and a token
// minted then is only valid for a request that also has no session, which is
// what makes this a binding rather than a formality.
func GenerateCSRFToken(binding string) string {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	// nonce | binding | issued-at. "|" cannot appear in a hex nonce or a hex
	// session hash, so the fields cannot be shifted into one another.
	payload := hex.EncodeToString(raw) + "|" + binding + "|" +
		strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	// RawURLEncoding (no '=' padding) so the token is safe to carry in a cookie
	// and read back in JS. base64.URLEncoding adds a trailing '=' which naive
	// `cookie.split('=')[1]` parsers strip, breaking the double-submit match.
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "." + sig))
}

// ValidateCSRFToken verifies a token produced by GenerateCSRFToken, and that it
// belongs to THIS principal and is not older than csrfTokenTTL.
//
// A token in the pre-binding format fails here. That is deliberate and its whole
// cost is one reload: the GET branch of CSRFTokenMiddleware re-mints whenever
// validation fails, so an open panel tab recovers on its next page load rather
// than getting stuck in the "token expired" loop this codebase has already been
// through once.
func ValidateCSRFToken(token, binding string) bool {
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
	// SplitN from the RIGHT would be wrong: the payload itself contains no ".",
	// so the last "." separates payload from signature and the first one would
	// too. Kept as an explicit last-index split so a future payload field
	// containing "." cannot silently change what is verified.
	i := strings.LastIndex(string(decoded), ".")
	if i < 0 {
		return false
	}
	payload, sig := string(decoded)[:i], string(decoded)[i+1:]

	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(payload))
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return false
	}

	fields := strings.Split(payload, "|")
	if len(fields) != 3 {
		return false // pre-binding token, or a shape this build does not mint
	}
	if subtle.ConstantTimeCompare([]byte(fields[1]), []byte(binding)) != 1 {
		return false // minted for somebody else
	}
	issued, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(issued, 0))
	// Both directions. A token stamped in the future is either a clock that moved
	// or a value nobody here minted; neither is a reason to extend its life.
	return age >= 0 && age <= csrfTokenTTL
}

// CSRFBinding returns the opaque per-principal value a CSRF token is bound to:
// the hash of the caller's session token, or "" when there is no session (the
// sign-in page). Hashing rather than using the raw token keeps the session
// secret out of a cookie the page script is meant to read.
func CSRFBinding(r *http.Request) string {
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return hashToken(c.Value)
}

// SignedToken returns a stateless, HMAC-signed, URL-safe token over payload
// ("b64url(payload).b64url(HMAC(payload))") using the server CSRF secret. Unlike
// the double-submit CSRF cookie, this needs NO cookie to verify — it is used for
// flows entered cross-site by a third party (the OAuth consent step), where the
// browser may drop even a SameSite=None cookie under third-party-cookie blocking.
// The token is unforgeable without the secret, so a value that VerifySignedToken
// accepts can only have been minted by this server.
func SignedToken(payload string) string {
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// VerifySignedToken returns the original payload iff token's HMAC checks out.
func VerifySignedToken(token string) (string, bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", false
	}
	mac := hmac.New(sha256.New, csrfSecret)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	return string(payload), true
}

// CSRFTokenMiddleware issues CSRF tokens on GET and validates them on mutating requests.
func CSRFTokenMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Re-issue the cookie when it is missing, empty, OR no longer valid.
			// A token can become invalid after its 1h lifetime, after the CSRF
			// secret changes, or now that tokens are session-bound, after signing
			// in or out. (The secret does NOT rotate per restart — InitCSRFSecret
			// persists it; that claim lived here for years and was not true.) Without the
			// validity check a *stale* cookie is left untouched, so reloading
			// the page never recovers it and every subsequent POST 403s — the
			// "session token expired — reload" loop the operator can't escape.
			needsToken := true
			binding := CSRFBinding(r)
			if c, err := r.Cookie("vp_csrf"); err == nil && c.Value != "" && ValidateCSRFToken(c.Value, binding) {
				needsToken = false
			}
			if needsToken {
				if token := GenerateCSRFToken(binding); token != "" {
					WriteReadableCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, MaxAge: 3600})
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			// API-key / bearer requests are not cookie-authenticated, so CSRF (which
			// only defends cookie auth against cross-site forgery) does not apply — a
			// third-party site cannot obtain the bearer token. This lets trusted
			// server-to-server callers (e.g. the parent provisioning the Tor world
			// over its child API key, ADR-0141) POST without a browser CSRF token,
			// exactly as the /api/v1 surface already allows.
			if HasValidAPIKey(r) {
				next.ServeHTTP(w, r)
				return
			}
			headerToken := r.Header.Get("X-CSRF-Token")
			// Plain HTML forms cannot set headers: accept the token as a form
			// field too (same double-submit + HMAC checks apply). Only parsed for
			// form-encoded bodies, so JSON payloads are never consumed here.
			if headerToken == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				headerToken = r.PostFormValue("csrf_token")
			}
			cookieToken := ""
			if c, err := r.Cookie("vp_csrf"); err == nil {
				cookieToken = c.Value
			}
			// Constant-time double-submit comparison: avoid leaking how much of
			// the token matched via response timing. ValidateCSRFToken separately
			// verifies the HMAC so a forged-but-matching pair still fails.
			tokensMatch := subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) == 1
			if headerToken == "" || cookieToken == "" || !tokensMatch || !ValidateCSRFToken(headerToken, CSRFBinding(r)) {
				writeAuthError(w, 403, "csrf_invalid", "CSRF token missing or invalid", "/docs/api/csrf")
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

// RequireAPIKey is HTTP middleware that validates X-API-Key / Authorization: Bearer headers.
func RequireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ClientIP(r)
		if locked, until := CheckAuthLockout(ip); locked {
			retryAfter := int(time.Until(until).Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
			writeAuthError(w, 429, "auth_lockout", fmt.Sprintf("locked out for %ds", retryAfter), "/docs/api/auth#lockout")
			return
		}
		// Resolve the key to its enforcement identity (static bootstrap →
		// superuser; issued key → its grant set). The constant-time static
		// comparison lives inside ResolveValidAPIKey, the single shared path with
		// the /os session-or-key gate. The identity is stamped into the request
		// context so the permission middleware and audit can read it.
		ki, ok := ResolveValidAPIKey(r)
		if !ok {
			RecordAuthFailure(ip)
			writeAuthError(w, 401, "unauthorized", "invalid or missing API key", "/docs/api/auth")
			return
		}
		RecordAuthSuccess(ip)
		next.ServeHTTP(w, RequestWithKeyInfo(r, ki))
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
