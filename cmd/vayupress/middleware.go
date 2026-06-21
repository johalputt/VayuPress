package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/metrics"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/resource"
	"github.com/johalputt/vayupress/internal/trace"
)

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

func structuredLoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Root HTTP span: wraps the entire request lifecycle.
		ctx, span := trace.Start(r.Context(), "http."+r.Method+" "+r.URL.Path)
		span.SetAttribute("http.method", r.Method)
		span.SetAttribute("http.path", r.URL.Path)
		span.SetAttribute("http.remote_addr", r.RemoteAddr)
		span.SetAttribute("runtime.goroutines", fmt.Sprintf("%d", resource.GoroutineCount()))

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r.WithContext(ctx))

		span.SetAttribute("http.status", fmt.Sprintf("%d", ww.Status()))
		if ww.Status() >= 500 {
			span.Status = trace.StatusError
		}
		span.End()

		dur := span.EndTime.Sub(span.StartTime)
		metrics.HTTPLatency.Record(dur)
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

func csrfCookieSecure() bool {
	if v := os.Getenv("CSRF_SECURE_COOKIE"); v != "" {
		return v == "true"
	}
	return config.Cfg.Domain != "localhost"
}
