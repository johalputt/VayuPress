package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkObservabilityMiddleware measures the fixed per-request overhead of
// the request-ID + structured-logging/tracing middleware that runs on EVERY
// request. It is a regression guard for hot-path allocations: fewer allocs/op
// means less GC pressure, which is what tightens the P95/P99 tail. Log output is
// discarded so the benchmark measures the middleware work (span + JSON marshal),
// not terminal/journald write speed.
func BenchmarkObservabilityMiddleware(b *testing.B) {
	prev := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prev)

	final := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requestIDMiddleware(structuredLoggerMiddleware(final))

	req := httptest.NewRequest(http.MethodGet, "/example-post-slug", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/130.0 Safari/537.36")

	// A reusable, allocation-free ResponseWriter so the benchmark measures the
	// middleware's own per-request allocations, not httptest.NewRecorder().
	rw := &benchResponseWriter{h: make(http.Header)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(rw, req)
	}
}

// benchResponseWriter is a minimal, reusable http.ResponseWriter for benchmarks.
type benchResponseWriter struct{ h http.Header }

func (b *benchResponseWriter) Header() http.Header         { return b.h }
func (b *benchResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (b *benchResponseWriter) WriteHeader(int)             {}
