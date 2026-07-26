// SPDX-License-Identifier: Apache-2.0

// Package fingerprint derives transport- and protocol-level client fingerprints
// used by VayuShield to tell real browsers from bots without cookies, JavaScript
// or any third-party service.
//
// It is a sovereign, pure-Go reimplementation of the ideas behind TLS
// fingerprinting libraries (JA3/JA4) — no CGo, no external runtime dependency,
// no network calls — extended with the 2026-era signals that best separate
// modern browsers from spoofing scrapers:
//
//   - JA3  — the classic MD5 over (TLSVersion, Ciphers, Extensions, Curves,
//     PointFormats), GREASE-stripped.
//   - JA4  — the newer, human-readable TLS fingerprint (transport, version,
//     SNI presence, cipher/extension counts, first ALPN, plus truncated
//     SHA-256 digests of the sorted cipher and extension lists).
//   - Post-quantum key share detection — real 2026 browsers offer the
//     X25519MLKEM768 (0x11ec) hybrid key share in their ClientHello; most
//     User-Agent-spoofing bots do not. This single bit is a strong human signal.
//   - HTTP/2 SETTINGS fingerprint — Go's default INITIAL_WINDOW_SIZE is 65535;
//     Chrome uses 6291456. The SETTINGS values + order differentiate Go-based
//     scrapers from real browsers with near-certainty.
//   - HTTP header order fingerprint — browsers emit headers (and HTTP/2
//     pseudo-headers) in a stable order; many bots deviate.
//
// The package operates on a primitive Signals value rather than crypto/tls or
// net/http types directly, so every hash is deterministic and unit-testable in
// isolation. See capture.go for the adapters that populate Signals from a live
// *tls.ClientHelloInfo and *http.Request.
package fingerprint

import (
	"crypto/md5" //nolint:gosec // JA3 is defined as an MD5 digest; not used for security.
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
)

// pqMLKEM768 is the TLS CurveID for the X25519MLKEM768 post-quantum hybrid key
// share (RFC 9180 / draft-kwiatkowski-tls-ecdhe-mlkem). Go's crypto/tls uses
// this value from 1.24 onward. We compare the numeric value directly so the
// package does not hard-depend on the named constant across toolchains.
const pqMLKEM768 uint16 = 0x11ec

// pqKyber768Draft is the older X25519Kyber768Draft00 hybrid (0x6399), still seen
// from some 2024–2025 browser builds; also counts as a post-quantum signal.
const pqKyber768Draft uint16 = 0x6399

// chromeH2InitialWindow is Chrome's HTTP/2 SETTINGS_INITIAL_WINDOW_SIZE. Go's
// net/http2 default is 65535; a value at or near Chrome's is a real-browser tell.
const chromeH2InitialWindow uint32 = 6291456

// goH2InitialWindow is the Go net/http2 client default INITIAL_WINDOW_SIZE — the
// canonical signature of a Go-based scraper that has not tuned its transport.
const goH2InitialWindow uint32 = 65535

// Signals is the raw, primitive set of client characteristics extracted from the
// TLS ClientHello and the HTTP request. All fields are optional: an empty field
// simply contributes nothing to the derived fingerprints, so the same code path
// works whether TLS was captured directly or terminated at an upstream proxy.
type Signals struct {
	// ── TLS ClientHello ──
	TLSVersion        uint16   // negotiated/offered max version (e.g. 0x0303 = TLS1.2)
	SupportedVersions []uint16 // supported_versions extension contents
	CipherSuites      []uint16 // offered cipher suites, in order
	Extensions        []uint16 // extension IDs, in order (empty if unavailable)
	Curves            []uint16 // supported groups / named curves
	PointFormats      []uint8  // EC point formats
	SignatureSchemes  []uint16 // signature algorithms
	ALPN              []string // application-layer protocols (h2, http/1.1, …)
	ServerName        string   // SNI (presence matters for JA4, value is not stored)

	// ── HTTP layer ──
	HTTPProtoMajor         int      // 1 or 2
	HTTPProtoMinor         int      // 0, 1
	HeaderOrder            []string // received header names, in order
	PseudoHeaderOrder      []string // HTTP/2 pseudo-header order (:method :authority :scheme :path)
	HTTP2SettingsOrder     []uint16 // SETTINGS frame parameter IDs, in order
	HTTP2InitialWindowSize uint32   // SETTINGS_INITIAL_WINDOW_SIZE (0 = unknown)
	UserAgent              string
	AcceptLanguage         string
	Accept                 string
}

// isGREASE reports whether v is a GREASE value (RFC 8701). Browsers randomly
// insert GREASE cipher/extension/curve values; they must be stripped before
// fingerprinting or the hash would be unstable per-connection.
func isGREASE(v uint16) bool { return v&0x0f0f == 0x0a0a }

// stripGREASE returns vs without GREASE values, preserving order.
func stripGREASE(vs []uint16) []uint16 {
	out := make([]uint16, 0, len(vs))
	for _, v := range vs {
		if !isGREASE(v) {
			out = append(out, v)
		}
	}
	return out
}

func joinUint16(vs []uint16, sep string) string {
	if len(vs) == 0 {
		return ""
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, sep)
}

func joinUint8(vs []uint8, sep string) string {
	if len(vs) == 0 {
		return ""
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.Itoa(int(v))
	}
	return strings.Join(parts, sep)
}

// JA3 returns the classic JA3 fingerprint: the lowercase hex MD5 of
// "SSLVersion,Ciphers,Extensions,EllipticCurves,ECPointFormats" with GREASE
// values removed. Returns "" when there is no TLS data to fingerprint.
func (s Signals) JA3() string {
	if s.TLSVersion == 0 && len(s.CipherSuites) == 0 {
		return ""
	}
	str := strings.Join([]string{
		strconv.Itoa(int(s.TLSVersion)),
		joinUint16(stripGREASE(s.CipherSuites), "-"),
		joinUint16(stripGREASE(s.Extensions), "-"),
		joinUint16(stripGREASE(s.Curves), "-"),
		joinUint8(s.PointFormats, "-"),
	}, ",")
	sum := md5.Sum([]byte(str)) //nolint:gosec // JA3 spec mandates MD5.
	return hex.EncodeToString(sum[:])
}

// tlsVersionLabel maps a TLS version to the two-digit label JA4 uses.
func tlsVersionLabel(v uint16, supported []uint16) string {
	max := v
	for _, sv := range stripGREASE(supported) {
		if sv > max {
			max = sv
		}
	}
	switch max {
	case 0x0304:
		return "13"
	case 0x0303:
		return "12"
	case 0x0302:
		return "11"
	case 0x0301:
		return "10"
	default:
		return "00"
	}
}

func truncSHA256(s string) string {
	if s == "" {
		return "000000000000"
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// JA4 returns the JA4 TLS fingerprint in its canonical "a_b_c" form:
//
//	<t|q><ver><sni d|i><nn ciphers><nn ext><first-ALPN>_<sha256(ciphers)[:12]>_<sha256(ext+sigalgs)[:12]>
//
// It is computed from GREASE-stripped, sorted cipher and extension lists per the
// JA4 specification. Returns "" when there is no TLS data.
func (s Signals) JA4() string {
	if s.TLSVersion == 0 && len(s.CipherSuites) == 0 {
		return ""
	}
	transport := "t" // TCP; QUIC ("q") is not distinguished at this layer
	ver := tlsVersionLabel(s.TLSVersion, s.SupportedVersions)
	sni := "i"
	if s.ServerName != "" {
		sni = "d"
	}
	ciphers := stripGREASE(s.CipherSuites)
	exts := stripGREASE(s.Extensions)
	nc := len(ciphers)
	if nc > 99 {
		nc = 99
	}
	ne := len(exts)
	if ne > 99 {
		ne = 99
	}
	alpn := "00"
	if len(s.ALPN) > 0 && len(s.ALPN[0]) >= 2 {
		a := s.ALPN[0]
		alpn = string(a[0]) + string(a[len(a)-1])
	}
	a := transport + ver + sni +
		leftPad2(nc) + leftPad2(ne) + alpn

	// JA4 sorts ciphers and extensions (sig algs appended, not sorted) before hashing.
	sortedCiphers := append([]uint16(nil), ciphers...)
	sort.Slice(sortedCiphers, func(i, j int) bool { return sortedCiphers[i] < sortedCiphers[j] })
	sortedExts := append([]uint16(nil), exts...)
	sort.Slice(sortedExts, func(i, j int) bool { return sortedExts[i] < sortedExts[j] })

	b := truncSHA256(joinUint16Hex(sortedCiphers, ","))
	extStr := joinUint16Hex(sortedExts, ",")
	if len(s.SignatureSchemes) > 0 {
		extStr += "_" + joinUint16Hex(stripGREASE(s.SignatureSchemes), ",")
	}
	c := truncSHA256(extStr)
	return a + "_" + b + "_" + c
}

func joinUint16Hex(vs []uint16, sep string) string {
	if len(vs) == 0 {
		return ""
	}
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = strconv.FormatUint(uint64(v), 16)
	}
	return strings.Join(parts, sep)
}

func leftPad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// PostQuantum reports whether the ClientHello offered a post-quantum hybrid key
// share (X25519MLKEM768 or the older X25519Kyber768Draft00). Real 2026 browsers
// send this; UA-spoofing bots typically do not, making it a strong human signal.
func (s Signals) PostQuantum() bool {
	for _, c := range s.Curves {
		if c == pqMLKEM768 || c == pqKyber768Draft {
			return true
		}
	}
	return false
}

// HTTP2SettingsHash returns a stable 12-hex-char digest of the HTTP/2 SETTINGS
// frame (parameter order + the initial window size). Returns "" when no HTTP/2
// SETTINGS were captured.
func (s Signals) HTTP2SettingsHash() string {
	if len(s.HTTP2SettingsOrder) == 0 && s.HTTP2InitialWindowSize == 0 {
		return ""
	}
	return truncSHA256(joinUint16(s.HTTP2SettingsOrder, "-") + ";iw=" + strconv.FormatUint(uint64(s.HTTP2InitialWindowSize), 10))
}

// HeaderOrderHash returns a stable 12-hex-char digest of the request header
// order (pseudo-headers first, then normal headers, lowercased). Returns "" when
// no header order was captured.
func (s Signals) HeaderOrderHash() string {
	if len(s.HeaderOrder) == 0 && len(s.PseudoHeaderOrder) == 0 {
		return ""
	}
	names := make([]string, 0, len(s.PseudoHeaderOrder)+len(s.HeaderOrder))
	names = append(names, s.PseudoHeaderOrder...)
	for _, h := range s.HeaderOrder {
		names = append(names, strings.ToLower(h))
	}
	return truncSHA256(strings.Join(names, ","))
}

// LooksLikeGoDefaultHTTP2 reports whether the HTTP/2 SETTINGS match the Go
// net/http2 client default (INITIAL_WINDOW_SIZE == 65535) — the canonical tell
// of an untuned Go scraper. Only meaningful when SETTINGS were captured.
func (s Signals) LooksLikeGoDefaultHTTP2() bool {
	return s.HTTP2InitialWindowSize == goH2InitialWindow
}

// LooksLikeBrowserHTTP2 reports whether the HTTP/2 initial window size is at or
// above Chrome's large value — a real-browser characteristic.
func (s Signals) LooksLikeBrowserHTTP2() bool {
	return s.HTTP2InitialWindowSize >= chromeH2InitialWindow
}

// Composite is the bundle of derived sub-fingerprints for one client.
type Composite struct {
	FingerprintHash   string `json:"fingerprint_hash"`
	JA3               string `json:"ja3"`
	JA4               string `json:"ja4"`
	HTTP2SettingsHash string `json:"http2_settings_hash"`
	HeaderOrderHash   string `json:"header_order_hash"`
	PostQuantum       bool   `json:"post_quantum"`
}

// Fingerprint computes every sub-fingerprint and a single composite hash that
// uniquely and stably identifies this client shape. The composite hash folds in
// the JA3, JA4, HTTP/2 SETTINGS, header order and PQ bit, plus a coarse
// User-Agent token, so two clients with an identical transport shape but a
// different UA family are still distinguishable.
func (s Signals) Fingerprint() Composite {
	c := Composite{
		JA3:               s.JA3(),
		JA4:               s.JA4(),
		HTTP2SettingsHash: s.HTTP2SettingsHash(),
		HeaderOrderHash:   s.HeaderOrderHash(),
		PostQuantum:       s.PostQuantum(),
	}
	pq := "0"
	if c.PostQuantum {
		pq = "1"
	}
	composite := strings.Join([]string{
		c.JA3, c.JA4, c.HTTP2SettingsHash, c.HeaderOrderHash, pq,
		uaFamily(s.UserAgent),
		strconv.Itoa(s.HTTPProtoMajor) + "." + strconv.Itoa(s.HTTPProtoMinor),
	}, "|")
	sum := sha256.Sum256([]byte(composite))
	c.FingerprintHash = hex.EncodeToString(sum[:])
	return c
}

// uaFamily reduces a User-Agent to a coarse family token so the composite hash
// stays stable across minor version churn while still separating browser
// families. It never stores the full UA — only this coarse bucket.
func uaFamily(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case l == "":
		return "none"
	case strings.Contains(l, "edg/"):
		return "edge"
	case strings.Contains(l, "chrome/") && !strings.Contains(l, "chromium"):
		return "chrome"
	case strings.Contains(l, "chromium"):
		return "chromium"
	case strings.Contains(l, "firefox/"):
		return "firefox"
	case strings.Contains(l, "safari/") && !strings.Contains(l, "chrome"):
		return "safari"
	case strings.Contains(l, "curl/"):
		return "curl"
	case strings.Contains(l, "python") || strings.Contains(l, "go-http") || strings.Contains(l, "okhttp") || strings.Contains(l, "java/"):
		return "http-lib"
	case strings.Contains(l, "bot") || strings.Contains(l, "crawler") || strings.Contains(l, "spider"):
		return "bot"
	default:
		return "other"
	}
}
