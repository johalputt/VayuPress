package main

// api_usage_middleware.go — VayuAPI Stage 3 (ADR-0134): per-key rate limiting
// and per-call WORM audit for API-key callers.
//
// Where the per-IP RateLimitMiddleware protects the box from a single hostile
// address, this gate enforces each KEY's budget — keyed by KeyInfo.ID, so a
// caller cannot escape it by rotating IPs. Every audited call lands in the
// append-only audit_log as `api.call | key:<id> | METHOD /path | status`, giving
// the operator a per-key usage trail with no telemetry leaving the box.
//
// The internal system key is exempt: it drives in-process automation (plugins,
// background jobs) whose volume would only drown the log, and it is not an
// external credential to meter.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// apiKeyUsage is the shared fixed-window limiter for all external keys. The
// per-key budget is passed per call (allowLimit), so one limiter serves keys
// with individual rate_per_min overrides while sharing window state.
var apiKeyUsage = newIngestLimiter(0, time.Minute)

// apiKeyDefaultRatePerMin is the per-key request budget applied when a key has
// no explicit rate_per_min. Env-tunable, clamped to a sane band; the default
// (600/min) is generous for real automation while still bounding abuse.
func apiKeyDefaultRatePerMin() int {
	if n, err := strconv.Atoi(config.EnvOr("VAYU_API_RATE_PER_MIN", "600")); err == nil && n >= 1 && n <= 100000 {
		return n
	}
	return 600
}

// serveWithKeyUsage applies the caller key's per-minute budget, serves the
// request, then writes the WORM audit line. It is the single usage gate shared
// by the /api middleware and the /os session-or-key path, so a key's budget and
// audit trail cover both surfaces identically.
func (a *App) serveWithKeyUsage(w http.ResponseWriter, r *http.Request, ki apikeys.KeyInfo, next http.Handler) {
	if ki.Scope == apikeys.ScopeInternal {
		next.ServeHTTP(w, r)
		return
	}
	limit := apiKeyDefaultRatePerMin()
	if ki.RatePerMin > 0 {
		limit = ki.RatePerMin
	}
	if !apiKeyUsage.allowLimit(ki.ID, limit) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, r, http.StatusTooManyRequests, "rate_limited",
			"per-key request budget exhausted ("+strconv.Itoa(limit)+"/min) — retry after the window resets",
			"/docs/compatibility/vayuapi")
		return
	}
	ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
	next.ServeHTTP(ww, r)
	// Append-only audit trail, off the response path (mirrors apikeys' async
	// touch). Method+path identify the action; the status records its outcome.
	go dbpkg.AuditLog("api.call", "key:"+ki.ID, r.Method+" "+r.URL.Path, strconv.Itoa(ww.Status()))
}

// apiUsageMiddleware wires serveWithKeyUsage into the protected /api group.
// It runs after RequireAPIKey (identity stamped) and after the per-IP limiter,
// so only fully-authenticated, admitted requests consume a key's budget.
func (a *App) apiUsageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ki, ok := auth.KeyInfoFromContext(r.Context())
		if !ok {
			// Unreachable after RequireAPIKey; serve rather than break the chain
			// (enforcement already failed closed in requireAPIPermission).
			next.ServeHTTP(w, r)
			return
		}
		a.serveWithKeyUsage(w, r, ki, next)
	})
}
