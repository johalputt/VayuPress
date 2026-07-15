package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/trace"
)

// =============================================================================
// SSRF protection (ADR-0009)
//
// The SSRF-safe outbound dialer now lives in internal/safefetch as the single
// source of truth (safefetch.SafeTransport). It pins the validated IP at dial
// time (closing the DNS-rebind window), never honours an environment proxy, and
// refuses the full set of private/reserved ranges. The weaker, re-resolving
// transport that previously lived here has been removed.
// =============================================================================

// internalServiceHosts are trusted, operator-configured loopback endpoints
// (Meilisearch, a local AI runtime) that the shared outbound client is allowed
// to reach even though they resolve to a private/loopback address. Webhook and
// update traffic uses the same client, so this is the *only* private
// destination any guarded outbound request may reach.
var internalServiceHosts = []string{"127.0.0.1", "localhost", "::1"}

// safeOutboundTransport builds the SSRF-hardened transport for the shared
// outbound HTTP client (webhooks, update checks, AI/Meili service calls).
func safeOutboundTransport() *http.Transport {
	return safefetch.SafeTransport(safefetch.TransportOptions{AllowHosts: internalServiceHosts})
}

// realIPMiddleware normalises r.RemoteAddr to the real client IP using the
// trusted-proxy-aware resolver (auth.ClientIP). It replaces chi's
// middleware.RealIP, which trusts X-Forwarded-For / X-Real-IP unconditionally
// and is therefore vulnerable to IP spoofing (GHSA-3fxj-6jh8-hvhx, audit F-3).
// Forwarding headers are honoured only when the immediate peer is a configured
// trusted proxy; otherwise RemoteAddr is left as the direct peer address.
func realIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = auth.ClientIP(r)
		next.ServeHTTP(w, r)
	})
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
		// Correlation ID: caller-supplied or derived from request ID.
		corrID := r.Header.Get("X-Correlation-ID")
		if corrID == "" {
			corrID = reqID
		}
		w.Header().Set("X-Request-ID", reqID)
		w.Header().Set("X-Correlation-ID", corrID)
		ctx := context.WithValue(r.Context(), ctxKeyRequestID{}, reqID)
		ctx = trace.WithCorrelationID(ctx, corrID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func getRequestID(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeyRequestID{}).(string); ok {
		return v
	}
	return ""
}

// streamLatencyCutoff separates a slow-but-real request from a long-lived stream.
// The global request timeout is 30s, so a handler that ran longer was a
// hijacked/streamed connection (WebSocket, SSE, a VayuTalk relay), whose lifetime
// must never be counted as request latency.
const streamLatencyCutoff = 30 * time.Second

func structuredLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Root HTTP span: wraps the entire request lifecycle. Kept deliberately
		// lean — this runs on EVERY request, so it avoids reflection-based
		// formatting (fmt.Sprintf) and the per-request runtime.NumGoroutine()
		// scheduler probe that used to be recorded as a span attribute (live
		// goroutine count is exposed as a gauge via /api/v1/admin/resource/stats
		// instead). Fewer per-request allocations = less GC pressure = a tighter
		// P95/P99 tail.
		ctx, span := trace.Start(r.Context(), "http."+r.Method+" "+r.URL.Path)
		span.SetAttribute("http.method", r.Method)
		span.SetAttribute("http.path", r.URL.Path)
		span.SetAttribute("http.remote_addr", r.RemoteAddr)

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(ctx))

		span.SetAttribute("http.status", strconv.Itoa(ww.Status()))
		if ww.Status() >= 500 {
			span.Status = trace.StatusError
		}
		span.End()

		dur := span.EndTime.Sub(span.StartTime)
		// Latency metrics measure page/request responsiveness, so exclude
		// long-lived connections — a streamed/hijacked response (e.g. a VayuTalk
		// relay held open for minutes) would otherwise record its ENTIRE lifetime
		// as a single "request latency", permanently pinning the P95 tail. Normal
		// requests can't exceed the 30s server timeout, so anything past it is a
		// stream, not a slow page. The windowed view drives the dashboards (recent
		// real latency); the cumulative one stays the Prometheus source.
		if dur <= streamLatencyCutoff {
			metrics.HTTPLatency.Record(dur)
			metrics.HTTPLatencyWindow.Record(dur)
		}
		logging.LogJSON(logging.LogFields{
			Level: "info", RequestID: getRequestID(r),
			CorrelationID: trace.CorrelationID(r.Context()),
			Method:        r.Method, Path: r.URL.Path,
			Status: ww.Status(), LatencyMS: dur.Milliseconds(),
			RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent(), Component: "http",
		})
	})
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		nonce := render.GenerateCSPNonce()
		// Strict baseline (no third-party frame-src). Pages with a click-to-load
		// video facade narrowly extend frame-src themselves via render.BuildCSP.
		csp := render.BuildCSP(nonce, nil)
		// Report-Only mode (CSP_REPORT_ONLY=true) reports violations without
		// blocking — useful in staging to surface frontend regressions via
		// /csp-report before flipping a stricter policy to enforcing.
		cspHeader := "Content-Security-Policy"
		if config.Cfg.CSPReportOnly {
			cspHeader = "Content-Security-Policy-Report-Only"
		}
		w.Header().Set(cspHeader, csp)
		ctx := render.WithCSPNonce(r.Context(), nonce)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// csrfCookieSecure delegates to the single auth-package implementation (audit
// F-7) so the Secure-attribute policy is defined in exactly one place.
func csrfCookieSecure() bool {
	return auth.CSRFCookieSecure()
}
