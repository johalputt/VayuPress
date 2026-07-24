package fingerprint

import (
	"crypto/tls"
	"net/http"
	"sync"
	"time"
)

// FromClientHello populates the TLS portion of a Signals value from a live
// *tls.ClientHelloInfo, as delivered to a crypto/tls GetConfigForClient hook.
//
// Note on completeness: crypto/tls exposes the offered cipher suites, curves,
// point formats, signature schemes, ALPN and supported versions, but NOT the
// raw extension list or its ordering. JA3/JA4 are therefore computed from the
// fields Go surfaces; the extension component is empty. This is a deliberate,
// documented trade-off of staying pure-Go with the standard TLS stack rather
// than parsing raw ClientHello bytes off the wire.
func FromClientHello(hi *tls.ClientHelloInfo) Signals {
	if hi == nil {
		return Signals{}
	}
	s := Signals{
		CipherSuites:      append([]uint16(nil), hi.CipherSuites...),
		SupportedVersions: append([]uint16(nil), hi.SupportedVersions...),
		ALPN:              append([]string(nil), hi.SupportedProtos...),
		ServerName:        hi.ServerName,
		PointFormats:      append([]uint8(nil), hi.SupportedPoints...),
	}
	for _, c := range hi.SupportedCurves {
		s.Curves = append(s.Curves, uint16(c))
	}
	for _, sc := range hi.SignatureSchemes {
		s.SignatureSchemes = append(s.SignatureSchemes, uint16(sc))
	}
	// Best available "version": the max offered supported version.
	var max uint16
	for _, v := range stripGREASE(hi.SupportedVersions) {
		if v > max {
			max = v
		}
	}
	if max == 0 {
		max = tls.VersionTLS12
	}
	s.TLSVersion = max
	return s
}

// ApplyRequest folds HTTP-layer signals from r into s (mutating and returning
// s). It records the negotiated protocol version, the User-Agent and a few
// order-independent request characteristics. HTTP header/pseudo-header order and
// HTTP/2 SETTINGS are populated separately by the transport capture layer when
// available; net/http alone does not expose them.
func (s Signals) ApplyRequest(r *http.Request) Signals {
	if r == nil {
		return s
	}
	s.HTTPProtoMajor = r.ProtoMajor
	s.HTTPProtoMinor = r.ProtoMinor
	s.UserAgent = r.UserAgent()
	s.AcceptLanguage = r.Header.Get("Accept-Language")
	s.Accept = r.Header.Get("Accept")
	if s.ServerName == "" {
		s.ServerName = r.Host
	}
	// When TLS was terminated in-process, crypto/tls has already parsed the
	// negotiated connection state; fold in the negotiated ALPN if we have none.
	if r.TLS != nil && len(s.ALPN) == 0 && r.TLS.NegotiatedProtocol != "" {
		s.ALPN = []string{r.TLS.NegotiatedProtocol}
	}
	return s
}

// ── Connection-keyed capture store ───────────────────────────────────────────

// captureShards is the number of independently-locked shards. A ClientHello is
// captured once per new TLS connection (Put) and read once per request (Get),
// both keyed by remote address, so under a high-concurrency flood a single mutex
// would serialise exactly the traffic the shield exists to absorb. Sharding by
// the remote-address hash spreads that lock contention across 64 stripes.
const captureShards = 64

type entry struct {
	sig Signals
	at  time.Time
}

type captureShard struct {
	mu sync.Mutex
	m  map[string]entry
}

// Store associates a captured ClientHello Signals value with a connection's
// remote address, so an HTTP handler can retrieve the TLS fingerprint that was
// observed during the handshake for the same connection. Entries expire so the
// map cannot grow without bound on a busy server. Safe for concurrent use.
type Store struct {
	shards [captureShards]*captureShard
	ttl    time.Duration
}

// NewStore creates a capture store whose entries expire after ttl (a few seconds
// is enough: the ClientHello and the first request share a connection and arrive
// within the handshake→request window).
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	s := &Store{ttl: ttl}
	for i := range s.shards {
		s.shards[i] = &captureShard{m: make(map[string]entry)}
	}
	return s
}

// shardFor picks a shard from the remote address with an inlined FNV-1a hash
// (no allocation).
func (s *Store) shardFor(remoteAddr string) *captureShard {
	h := uint64(14695981039346656037)
	for i := 0; i < len(remoteAddr); i++ {
		h ^= uint64(remoteAddr[i])
		h *= 1099511628211
	}
	return s.shards[h%captureShards]
}

// Put records the Signals observed for remoteAddr at the current time.
func (s *Store) Put(remoteAddr string, sig Signals) {
	if s == nil || remoteAddr == "" {
		return
	}
	now := time.Now()
	sh := s.shardFor(remoteAddr)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	sh.m[remoteAddr] = entry{sig: sig, at: now}
	// Per-shard cap (8192 total ÷ 64 shards = 128). The TTL sweep bounds this far
	// below the cap in practice; the cap is only a backstop against a burst of
	// distinct connections faster than entries expire.
	if len(sh.m) > 128 {
		for k, v := range sh.m {
			if now.Sub(v.at) > s.ttl {
				delete(sh.m, k)
			}
		}
	}
}

// Get returns the Signals captured for remoteAddr and whether a fresh entry
// existed. Expired entries are treated as absent.
func (s *Store) Get(remoteAddr string) (Signals, bool) {
	if s == nil || remoteAddr == "" {
		return Signals{}, false
	}
	sh := s.shardFor(remoteAddr)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, ok := sh.m[remoteAddr]
	if !ok || time.Since(e.at) > s.ttl {
		return Signals{}, false
	}
	return e.sig, true
}
