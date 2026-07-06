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

// Store associates a captured ClientHello Signals value with a connection's
// remote address, so an HTTP handler can retrieve the TLS fingerprint that was
// observed during the handshake for the same connection. Entries expire so the
// map cannot grow without bound on a busy server. Safe for concurrent use.
type Store struct {
	mu  sync.Mutex
	m   map[string]entry
	ttl time.Duration
}

type entry struct {
	sig Signals
	at  time.Time
}

// NewStore creates a capture store whose entries expire after ttl (a few seconds
// is enough: the ClientHello and the first request share a connection and arrive
// within the handshake→request window).
func NewStore(ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Store{m: make(map[string]entry), ttl: ttl}
}

// Put records the Signals observed for remoteAddr at the current time.
func (s *Store) Put(remoteAddr string, sig Signals) {
	if s == nil || remoteAddr == "" {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[remoteAddr] = entry{sig: sig, at: now}
	if len(s.m) > 8192 {
		for k, v := range s.m {
			if now.Sub(v.at) > s.ttl {
				delete(s.m, k)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[remoteAddr]
	if !ok || time.Since(e.at) > s.ttl {
		return Signals{}, false
	}
	return e.sig, true
}

// Len returns the number of live (possibly expired) entries — exposed for tests
// and memory-pressure metrics.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.m)
}
