package fingerprint

import (
	"net/http/httptest"
	"testing"
	"time"
)

func chromeLikeSignals() Signals {
	return Signals{
		TLSVersion:        0x0303,
		SupportedVersions: []uint16{0x0a0a, 0x0304, 0x0303}, // includes GREASE
		CipherSuites:      []uint16{0x1a1a, 0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f},
		Extensions:        []uint16{0x0a0a, 0, 23, 65281, 10, 11, 16, 5, 13, 18, 51, 45, 43, 27, 17513},
		Curves:            []uint16{0x1a1a, pqMLKEM768, 29, 23, 24},
		PointFormats:      []uint8{0},
		SignatureSchemes:  []uint16{0x0403, 0x0804, 0x0401},
		ALPN:              []string{"h2", "http/1.1"},
		ServerName:        "example.com",
		HTTPProtoMajor:    2, HTTPProtoMinor: 0,
		HTTP2InitialWindowSize: chromeH2InitialWindow,
		HTTP2SettingsOrder:     []uint16{1, 2, 3, 4, 6},
		HeaderOrder:            []string{"Host", "User-Agent", "Accept"},
		PseudoHeaderOrder:      []string{":method", ":authority", ":scheme", ":path"},
		UserAgent:              "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130.0.0.0 Safari/537.36",
	}
}

func TestJA3StableAndGREASEStripped(t *testing.T) {
	s := chromeLikeSignals()
	ja3 := s.JA3()
	if len(ja3) != 32 {
		t.Fatalf("JA3 should be 32 hex chars, got %q", ja3)
	}
	// Reordering only GREASE presence must not change the hash: build an
	// identical signal set and confirm determinism.
	if s.JA3() != ja3 {
		t.Fatal("JA3 not deterministic")
	}
	// A signals value with GREASE removed by hand must match.
	s2 := s
	s2.CipherSuites = []uint16{0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f}
	s2.Extensions = []uint16{0, 23, 65281, 10, 11, 16, 5, 13, 18, 51, 45, 43, 27, 17513}
	s2.Curves = []uint16{pqMLKEM768, 29, 23, 24}
	if s2.JA3() != ja3 {
		t.Fatalf("GREASE stripping mismatch: %q vs %q", s2.JA3(), ja3)
	}
}

func TestJA3EmptyWhenNoTLS(t *testing.T) {
	if (Signals{}).JA3() != "" {
		t.Fatal("expected empty JA3 for empty signals")
	}
}

func TestJA4Format(t *testing.T) {
	s := chromeLikeSignals()
	ja4 := s.JA4()
	// a-section is 10 chars: t + 2 ver + 1 sni + 2 cipher + 2 ext + 2 alpn.
	parts := splitN(ja4, '_')
	if len(parts) != 3 {
		t.Fatalf("JA4 must have 3 underscore-separated parts, got %q", ja4)
	}
	if len(parts[0]) != 10 {
		t.Fatalf("JA4 a-section must be 10 chars, got %q", parts[0])
	}
	if parts[0][0] != 't' {
		t.Fatalf("JA4 transport must be t, got %q", parts[0])
	}
	if parts[0][1:3] != "13" {
		t.Fatalf("JA4 version should be 13 (TLS1.3 offered), got %q", parts[0][1:3])
	}
	if parts[0][3] != 'd' {
		t.Fatalf("JA4 SNI flag should be d (SNI present), got %q", string(parts[0][3]))
	}
	if len(parts[1]) != 12 || len(parts[2]) != 12 {
		t.Fatalf("JA4 hash sections must be 12 chars, got %q / %q", parts[1], parts[2])
	}
}

func TestPostQuantumDetection(t *testing.T) {
	s := chromeLikeSignals()
	if !s.PostQuantum() {
		t.Fatal("expected PQ true (X25519MLKEM768 present)")
	}
	s.Curves = []uint16{29, 23, 24}
	if s.PostQuantum() {
		t.Fatal("expected PQ false without hybrid curve")
	}
	s.Curves = []uint16{pqKyber768Draft, 29}
	if !s.PostQuantum() {
		t.Fatal("expected PQ true for Kyber768 draft")
	}
}

func TestHTTP2Signals(t *testing.T) {
	s := chromeLikeSignals()
	if !s.LooksLikeBrowserHTTP2() {
		t.Fatal("Chrome window size should look like browser")
	}
	if s.LooksLikeGoDefaultHTTP2() {
		t.Fatal("Chrome window size must not equal Go default")
	}
	s.HTTP2InitialWindowSize = goH2InitialWindow
	if !s.LooksLikeGoDefaultHTTP2() {
		t.Fatal("Go default window size not detected")
	}
	if s.HTTP2SettingsHash() == "" {
		t.Fatal("expected non-empty settings hash")
	}
}

func TestHeaderOrderHashDiffers(t *testing.T) {
	a := chromeLikeSignals()
	b := chromeLikeSignals()
	b.HeaderOrder = []string{"Accept", "User-Agent", "Host"} // reordered
	if a.HeaderOrderHash() == b.HeaderOrderHash() {
		t.Fatal("different header order must produce different hash")
	}
}

func TestCompositeStableAndDistinct(t *testing.T) {
	a := chromeLikeSignals().Fingerprint()
	if len(a.FingerprintHash) != 64 {
		t.Fatalf("composite hash must be sha256 hex, got %d chars", len(a.FingerprintHash))
	}
	if a.FingerprintHash != chromeLikeSignals().Fingerprint().FingerprintHash {
		t.Fatal("composite hash not deterministic")
	}
	// A bot with a different UA family must get a different composite hash.
	bot := chromeLikeSignals()
	bot.UserAgent = "python-requests/2.31.0"
	bot.Curves = []uint16{29, 23} // no PQ
	if bot.Fingerprint().FingerprintHash == a.FingerprintHash {
		t.Fatal("distinct client shape produced identical composite hash")
	}
}

func TestApplyRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "https://example.com/x", nil)
	r.Header.Set("User-Agent", "curl/8.4.0")
	r.Header.Set("Accept-Language", "en-US")
	s := Signals{}.ApplyRequest(r)
	if s.UserAgent != "curl/8.4.0" {
		t.Fatalf("UA not applied: %q", s.UserAgent)
	}
	if s.AcceptLanguage != "en-US" {
		t.Fatalf("Accept-Language not applied: %q", s.AcceptLanguage)
	}
	if s.HTTPProtoMajor != 1 {
		t.Fatalf("proto major want 1 got %d", s.HTTPProtoMajor)
	}
}

func TestStorePutGetExpiry(t *testing.T) {
	st := NewStore(50 * time.Millisecond)
	st.Put("1.2.3.4:1234", chromeLikeSignals())
	if _, ok := st.Get("1.2.3.4:1234"); !ok {
		t.Fatal("expected fresh entry")
	}
	time.Sleep(70 * time.Millisecond)
	if _, ok := st.Get("1.2.3.4:1234"); ok {
		t.Fatal("expected expired entry to be absent")
	}
	if _, ok := st.Get(""); ok {
		t.Fatal("empty key must not resolve")
	}
}

// splitN splits s on sep into all fields (small helper to avoid importing strings in asserts).
func splitN(s string, sep byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
