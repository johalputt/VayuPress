// SPDX-License-Identifier: Apache-2.0

package fingerprint

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// These tests measure what the fingerprint actually distinguishes in the
// STANDARD deployment — TLS terminated by nginx, `proxy_pass` to a plain HTTP
// listener — rather than what the derivation code is capable of.
//
// The distinction matters because the product described itself as
// fingerprint-based. A capability that is real in the code and unreachable in
// production is an honesty defect of its own: it makes an operator believe they
// have a defence they do not have, and it makes every downstream component
// (the adaptive signature database, the signature cache, the anti-poisoning
// guards) look better-founded than it is.

// productionSignals builds the Signals the shield actually sees behind a
// terminating reverse proxy: no ClientHello capture, so no cipher list, no
// extension order, no HTTP/2 SETTINGS, no TLS at all from crypto/tls's point of
// view.
func productionSignals(ua, accept, lang string, major, minor int) Signals {
	r := httptest.NewRequest(http.MethodGet, "http://example.test/article", nil)
	r.Header.Set("User-Agent", ua)
	r.Header.Set("Accept", accept)
	r.Header.Set("Accept-Language", lang)
	r.ProtoMajor, r.ProtoMinor = major, minor
	return Signals{}.ApplyRequest(r)
}

// TestFingerprintCardinalityBehindAProxy is the measurement, not an assertion.
//
// A wide corpus of genuinely different clients — different browsers, different
// header sets, different languages, different protocol versions — collapses to a
// handful of distinct composite hashes, because every transport-derived input is
// empty and the only surviving discriminators are a coarse UA family token and
// the HTTP major/minor.
func TestFingerprintCardinalityBehindAProxy(t *testing.T) {
	uas := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64; rv:133.0) Gecko/20100101 Firefox/133.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36 Edg/131.0.0.0",
		"curl/8.5.0",
		"python-requests/2.32.3",
		"Go-http-client/2.0",
		"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
		"SomethingEntirelyNovel/1.0",
	}
	accepts := []string{
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"text/html,application/xhtml+xml,*/*;q=0.8",
		"*/*",
		"application/json",
	}
	langs := []string{"en-GB,en;q=0.9", "de-DE,de;q=0.9,en;q=0.8", "ja-JP,ja;q=0.9", ""}
	protos := [][2]int{{1, 1}, {2, 0}}

	seen := map[string]bool{}
	total := 0
	for _, ua := range uas {
		for _, a := range accepts {
			for _, l := range langs {
				for _, p := range protos {
					total++
					seen[productionSignals(ua, a, l, p[0], p[1]).Fingerprint().FingerprintHash] = true
				}
			}
		}
	}

	// The bound is the honest one: UA families times protocol versions. Accept and
	// Accept-Language do not enter the composite at all, so 320 distinct clients
	// cannot produce more than that.
	const familyCount = 10 // none, edge, chrome, chromium, firefox, safari, curl, http-lib, bot, other
	max := familyCount * len(protos)
	if len(seen) > max {
		t.Errorf("cardinality %d exceeds the structural bound of %d — the composite gained an "+
			"input, which is good news this test should be updated for", len(seen), max)
	}
	if len(seen) > 32 {
		t.Errorf("cardinality is %d; this test exists because it is small. If transport capture "+
			"is now wired in production, update the operator-facing copy too.", len(seen))
	}
	t.Logf("%d genuinely distinct clients produced %d distinct fingerprints behind a "+
		"terminating proxy (structural ceiling %d)", total, len(seen), max)
}

// TestTransportDiscriminatorsAreEmptyBehindAProxy names the two specific scorer
// branches that cannot fire, so a reader of the scorer knows they are dead in the
// standard deployment rather than merely rare.
func TestTransportDiscriminatorsAreEmptyBehindAProxy(t *testing.T) {
	s := productionSignals("Mozilla/5.0 (X11; Linux x86_64) Chrome/131.0.0.0 Safari/537.36",
		"text/html", "en", 2, 0)

	if s.JA3() != "" {
		t.Errorf("JA3 = %q behind a terminating proxy — if this is now populated, the operator "+
			"copy retired in this change can come back", s.JA3())
	}
	if s.JA4() != "" {
		t.Errorf("JA4 = %q behind a terminating proxy", s.JA4())
	}
	if s.HTTP2InitialWindowSize != 0 {
		t.Error("HTTP2InitialWindowSize is set — the Go-default-SETTINGS contradiction branch " +
			"in the scorer is reachable again")
	}
	if s.PostQuantum() {
		t.Error("PostQuantum is true without a ClientHello — the key-share branch is reachable again")
	}
}

// TestCaptureStoreIsTheOnlyWayToGetTransportSignals — the derivation code is kept
// because it is correct and will run wherever TLS is terminated in-process. This
// pins the boundary: signals arrive through the capture store, and nothing else
// populates them.
func TestCaptureStoreIsTheOnlyWayToGetTransportSignals(t *testing.T) {
	st := NewStore(time.Minute)
	if st == nil {
		t.Fatal("NewStore returned nil")
	}
	// HasTLS lives on the SCORER's input, not on Signals — it is derived from
	// whether a capture was found at all. What the store carries is the raw
	// ClientHello material.
	captured := Signals{
		TLSVersion:             0x0304,
		CipherSuites:           []uint16{0x1301, 0x1302},
		Extensions:             []uint16{0, 43, 51},
		Curves:                 []uint16{0x11ec, 29}, // X25519MLKEM768, X25519
		HTTP2InitialWindowSize: 6291456,
	}
	st.Put("203.0.113.9:5555", captured)
	got, ok := st.Get("203.0.113.9:5555")
	if !ok {
		t.Fatal("a captured ClientHello did not come back out of the store")
	}
	if got.HTTP2InitialWindowSize == 0 || len(got.CipherSuites) == 0 {
		t.Errorf("the store lost the transport signals: %+v", got)
	}
	if !got.PostQuantum() {
		t.Error("a captured X25519MLKEM768 key share did not register as post-quantum — " +
			"the scorer branch that depends on it would stay dead even WITH capture wired")
	}
	// And with those present, the composite is genuinely distinguishing.
	withTLS := got.ApplyRequest(httptest.NewRequest(http.MethodGet, "http://example.test/", nil)).Fingerprint()
	without := productionSignals("", "", "", 1, 1).Fingerprint()
	if withTLS.FingerprintHash == without.FingerprintHash {
		t.Error("transport signals made no difference to the composite")
	}
}
