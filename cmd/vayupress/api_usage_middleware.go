// SPDX-License-Identifier: Apache-2.0

package main

// api_usage_middleware.go — VayuAPI Stage 3 (ADR-0134): per-key rate limiting
// and per-call WORM audit for API-key callers.
//
// This gate enforces each KEY's budget — keyed by KeyInfo.ID, so a caller
// cannot escape it by rotating IPs — the one the operator set for the key,
// the API keys page's default, or none when either says unlimited. Every audited call lands in the
// append-only audit_log as `api.call | key:<id> | METHOD /path | status`, giving
// the operator a per-key usage trail with no telemetry leaving the box.
//
// The internal system key is exempt: it drives in-process automation (plugins,
// background jobs) whose volume would only drown the log, and it is not an
// external credential to meter.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// apiKeyUsage is the shared fixed-window limiter for all external keys. The
// per-key budget is passed per call (allowLimit), so one limiter serves keys
// with individual rate_per_min overrides while sharing window state.
var apiKeyUsage = newIngestLimiter(0, time.Minute)

// apiKeyBuiltInRatePerMin is the per-key budget when neither the key nor the
// API keys page sets one: VAYU_API_RATE_PER_MIN, clamped to a sane band, or
// 600, which is generous for real automation while still bounding abuse.
func apiKeyBuiltInRatePerMin() int {
	if n, err := strconv.Atoi(config.EnvOr("VAYU_API_RATE_PER_MIN", "600")); err == nil && n >= 1 && n <= apikeys.MaxRatePerMin {
		return n
	}
	return 600
}

// apiDefaultRatePerMin is the budget of a key that sets none: the API keys
// page's default (settings.KeyAPIRatePerMin), a number or apikeys.Unlimited,
// or the built-in budget when the page sets none either.
func (a *App) apiDefaultRatePerMin(ctx context.Context) int {
	if a.siteSettings != nil {
		v := strings.TrimSpace(a.siteSettings.Get(ctx, settings.ForPrimary(), settings.KeyAPIRatePerMin))
		if v == "unlimited" {
			return apikeys.Unlimited
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= apikeys.MaxRatePerMin {
			return n
		}
	}
	return apiKeyBuiltInRatePerMin()
}

// keyRatePerMin is ki's budget: its own, or the default. apikeys.Unlimited
// means its calls are not counted.
func (a *App) keyRatePerMin(ctx context.Context, ki apikeys.KeyInfo) int {
	if ki.RatePerMin != 0 {
		return ki.RatePerMin
	}
	return a.apiDefaultRatePerMin(ctx)
}

// rateLabel says a budget as the console shows it.
func rateLabel(rate int) string {
	if rate == apikeys.Unlimited {
		return "unlimited"
	}
	return strconv.Itoa(rate) + " a minute"
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
	limit := a.keyRatePerMin(r.Context(), ki)
	if limit != apikeys.Unlimited && !apiKeyUsage.allowLimit(ki.ID, limit) {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, r, http.StatusTooManyRequests, "rate_limited",
			"per-key request budget exhausted ("+strconv.Itoa(limit)+"/min) — retry after the window resets",
			"/docs/compatibility/vayuapi")
		return
	}
	ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
	next.ServeHTTP(ww, r)
	// Append-only audit trail, written after the response body is flushed to ww.
	// Synchronous like every other AuditLog call site: the record is guaranteed
	// durable before the handler returns (no detached goroutine that could race
	// a DB re-open or be lost on shutdown), and with SQLite's single writer
	// connection an async write would only contend on the same conn anyway.
	// Method+path identify the action; the status records its outcome.
	dbpkg.AuditLog("api.call", "key:"+ki.ID, r.Method+" "+r.URL.Path, strconv.Itoa(ww.Status()))
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
