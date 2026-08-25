// SPDX-License-Identifier: Apache-2.0

package vayushield

// bench_test.go — hot-path benchmarks (2025 plan Wave 0).
//
// The prime directive for every optimisation wave was zero speed regression.
// These benchmarks make that MEASURABLE rather than asserted: each covers one
// component on the per-request classification path, and `go test -bench .`
// before/after any change shows exactly what moved. Run them against the base
// commit and the change; a double-digit regression without a compensating win
// elsewhere should stop the change.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johalputt/vayupress/internal/vayushield/behaviour"
	"github.com/johalputt/vayupress/internal/vayushield/botdb"
	"github.com/johalputt/vayupress/internal/vayushield/challenge"
	"github.com/johalputt/vayupress/internal/vayushield/inspect"
)

const benchBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// BenchmarkStaticMatchUA runs on EVERY request with a User-Agent header — the
// single hottest lookup in the shield.
func BenchmarkStaticMatchUA(b *testing.B) {
	db := botdb.NewStaticDB()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = db.MatchUA(benchBrowserUA)
	}
}

// BenchmarkInspectScan runs the compiled-in probe heuristics over path+query,
// once per classified request since the duplicate-scan removal.
func BenchmarkInspectScan(b *testing.B) {
	target := "/search?q=wp-login.php+admin+etc+passwd"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = inspect.Scan(target, "")
	}
}

// BenchmarkBehaviourObserveScore measures the NAT-split activity sketch: one
// key hash, one slot observation, one score read — per classified request.
func BenchmarkBehaviourObserveScore(b *testing.B) {
	tr := behaviour.New()
	key := "198.51.100.7|chrome"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := tr.Observe(key, "/some/page", 0)
		_, _ = s.Score()
	}
}

// BenchmarkSolutionValid anchors proof verification cost (the server side of
// the silent PoW): one SHA-256 per check.
func BenchmarkSolutionValid(b *testing.B) {
	const salt = "benchmark-salt"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = challenge.SolutionValid(salt, "123456", 1)
	}
}

// BenchmarkIssuePoW covers challenge issuance including HMAC signing.
func BenchmarkIssuePoW(b *testing.B) {
	s := challenge.NewSigner([]byte("benchmark-secret"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.IssuePoW(challenge.DefaultDifficulty, time.Minute); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkClassify end-to-end: fingerprint → static → score pipeline through
// a real Manager over a synthetic browser GET. This is the number the whole
// "no speed loss" directive is about.
func BenchmarkClassify(b *testing.B) {
	m := newTestManager(true)
	r := httptest.NewRequest(http.MethodGet, "http://example.com/some/article?page=2", nil)
	r.Header.Set("User-Agent", benchBrowserUA)
	r.RemoteAddr = "203.0.113.7:5555"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Classify(r)
	}
}

// BenchmarkClassifyWithKnownIntel mirrors the middleware's hot path after the
// single-intel-lookup fix: no feed function is consulted at all (none is set),
// and the request still walks the full pipeline.
func BenchmarkClassifyWithKnownIntel(b *testing.B) {
	m := newTestManager(true)
	r := httptest.NewRequest(http.MethodGet, "http://example.com/some/article?page=2", nil)
	r.Header.Set("User-Agent", benchBrowserUA)
	r.RemoteAddr = "203.0.113.7:5555"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.ClassifyWithIntel(r, 0)
	}
}
