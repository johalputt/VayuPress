// SPDX-License-Identifier: Apache-2.0

package pgp

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/johalputt/vayupress/internal/config"
)

// wkdDomainRe matches a syntactically valid public DNS hostname (at least two
// labels). It deliberately excludes schemes, ports, paths and userinfo.
var wkdDomainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)+$`)

// validExternalWKDDomain reports whether domain is a public DNS hostname that is
// safe to fetch a WKD key from. It rejects IP literals, "localhost", numeric
// TLDs and malformed input so an attacker-supplied recipient domain cannot
// steer the outbound request at internal/loopback hosts (SSRF — CWE-918).
func validExternalWKDDomain(domain string) bool {
	if domain == "" || len(domain) > 253 {
		return false
	}
	if strings.EqualFold(domain, "localhost") || net.ParseIP(domain) != nil {
		return false
	}
	if !wkdDomainRe.MatchString(domain) {
		return false
	}
	labels := strings.Split(domain, ".")
	tld := labels[len(labels)-1]
	for _, r := range tld { // a non-numeric TLD rules out disguised IPs
		if r < '0' || r > '9' {
			return true
		}
	}
	return false
}

// zbase32Alphabet is the encoding used by WKD for the hashed local-part.
const zbase32Alphabet = "ybndrfg8ejkmcpqxot1uwisza345h769"

// zbase32 encodes data using the z-base-32 alphabet (RFC-style WKD hashing).
func zbase32(data []byte) string {
	var sb strings.Builder
	bits := 0
	var buf uint64
	for _, b := range data {
		buf = (buf << 8) | uint64(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(zbase32Alphabet[(buf>>uint(bits))&0x1f])
		}
	}
	if bits > 0 {
		sb.WriteByte(zbase32Alphabet[(buf<<uint(5-bits))&0x1f])
	}
	return sb.String()
}

// wkdLocalHash returns the z-base-32 SHA-1 hash of the lowercased local-part.
//
// SHA-1 here is REQUIRED by the Web Key Directory spec
// (draft-koch-openpgp-webkey-service): the local-part is hashed with SHA-1 and
// z-base-32 encoded to form the *public* directory key in the WKD URL. This is a
// URL-mapping digest of an already-public address — not a security primitive and
// not applied to any secret — so it is not a "weak hash on sensitive data".
// Substituting a stronger hash would simply break interoperability with every
// other WKD client (GnuPG, Thunderbird, …). Any static-analysis alert flagging
// SHA-1 here is therefore a known false positive.
func wkdLocalHash(localpart string) string {
	sum := sha1.Sum([]byte(strings.ToLower(localpart))) //nolint:gosec // WKD spec mandates SHA-1 for the public directory key (not sensitive data)
	return zbase32(sum[:])
}

// WKDURL returns the advanced-method Web Key Directory URL at which an external
// PGP client will look for this address's public key:
//
//	https://openpgpkey.<domain>/.well-known/openpgpkey/<domain>/hu/<hash>?l=<local>
//
// Exported so the admin panel shows the SAME URL the spec sends clients to,
// derived from the same hash function that serves it. A panel that displayed a
// hand-written URL could drift from the one ServeWKD answers on, and the operator
// would have no way to tell which was wrong.
//
// It returns "" for an address without exactly one "@", rather than emitting a
// URL that cannot resolve.
func WKDURL(email string) string {
	local, domain := splitEmail(email)
	if local == "" || domain == "" {
		return ""
	}
	return "https://openpgpkey." + domain + "/.well-known/openpgpkey/" + domain +
		"/hu/" + wkdLocalHash(local) + "?l=" + url.QueryEscape(local)
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func splitEmail(email string) (local, domain string) {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return email, ""
	}
	return email[:at], email[at+1:]
}

// dearmorPublic converts an armored public key to its binary form for WKD.
func dearmorPublic(armored string) ([]byte, error) {
	block, err := armor.Decode(strings.NewReader(armored))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(block.Body)
}

// wkdMarker is the path prefix both WKD methods share.
const wkdMarker = "/.well-known/openpgpkey/"

// wkdRequestDomain works out WHICH domain a request is asking about.
//
// The two spec methods carry it differently:
//
//	advanced: https://openpgpkey.<domain>/.well-known/openpgpkey/<domain>/hu/<hash>
//	direct:   https://<domain>/.well-known/openpgpkey/hu/<hash>
//
// The advanced form states the domain in the path; the direct form implies it
// from the host. Both must be honoured, because an install serving several
// domains answers on all of their hostnames.
func wkdRequestDomain(r *http.Request, fallback string) string {
	if i := strings.Index(r.URL.Path, wkdMarker); i >= 0 {
		rest := strings.TrimPrefix(r.URL.Path[i+len(wkdMarker):], "/")
		// A leading segment that is not "hu" or "policy" is the domain.
		if j := strings.IndexByte(rest, '/'); j > 0 {
			if seg := rest[:j]; seg != "hu" && seg != "policy" {
				return normalizeEmail(seg)
			}
		}
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = normalizeEmail(host)
	// The advanced host is openpgpkey.<domain>; the domain is what follows.
	host = strings.TrimPrefix(host, "openpgpkey.")
	// An IP literal or "localhost" is not a domain anyone holds mail at, so it
	// cannot scope a lookup. That is the local-development and behind-a-proxy
	// case, and the configured primary domain is the only sensible reading of
	// which directory was meant.
	if validExternalWKDDomain(host) {
		return host
	}
	return normalizeEmail(fallback)
}

// ServeWKD returns an http.Handler implementing both Web Key Directory methods —
// the advanced form at /.well-known/openpgpkey/<domain>/hu/<hash> and the direct
// form at /.well-known/openpgpkey/hu/<hash> — plus the policy file. fallback is
// used only when a request carries no usable host, which in practice means a
// synthetic request in a test.
//
// EVERY LOOKUP IS SCOPED TO THE DOMAIN THAT WAS ASKED FOR.
//
// That scoping is the security property, not a detail. This handler previously
// took a domain argument and never read it: it matched on the local-part hash
// alone, across every key in the keystore. On a single-domain install that is
// indistinguishable from correct, which is why it survived. On an install
// serving several domains it means a lookup for alice@one.example returns
// alice@two.example's key whenever the first address has no key of its own —
// because the hash of "alice" is the same either way, and the domain segment of
// the request was thrown away.
//
// The consequence is not a 404 in the wrong place. A correspondent's client
// accepts that key as the answer for alice@one.example and encrypts to it, so
// mail meant for one person is encrypted to a different person's key: the
// intended recipient cannot read it and someone else can. For a host serving
// unrelated domains those are unrelated people. WKD exists so encryption happens
// without the user doing anything, and this made that automatic step silently
// address the wrong party.
func (e *Engine) ServeWKD(fallbackDomain string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !e.cfg.WKDEnabled || e.ks == nil {
			http.NotFound(w, r)
			return
		}
		path := r.URL.Path
		// Policy file: presence signals WKD support for the domain.
		if strings.HasSuffix(path, "/policy") {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		idx := strings.Index(path, "/hu/")
		if idx < 0 {
			http.NotFound(w, r)
			return
		}
		hash := path[idx+len("/hu/"):]
		if i := strings.IndexByte(hash, '/'); i >= 0 {
			hash = hash[:i]
		}
		if hash == "" {
			http.NotFound(w, r)
			return
		}
		want := wkdRequestDomain(r, fallbackDomain)
		if want == "" {
			http.NotFound(w, r)
			return
		}
		recs, err := e.ks.list()
		if err != nil {
			http.Error(w, "wkd unavailable", http.StatusInternalServerError)
			return
		}
		for _, rec := range recs {
			local, dom := splitEmail(normalizeEmail(rec.Email))
			if dom != want {
				continue
			}
			if wkdLocalHash(local) != hash {
				continue
			}
			bin, err := dearmorPublic(rec.PublicArmor)
			if err != nil {
				http.Error(w, "wkd key error", http.StatusInternalServerError)
				return
			}
			// Strong ETag over the key bytes lets clients revalidate cheaply. It
			// stays correct across key rotation (ADR-0076): when the key changes the
			// bytes change, so the ETag changes and a revalidation returns the new
			// key rather than a stale one. A modest max-age caps how long a client
			// may serve a cached copy before revalidating.
			sum := sha256.Sum256(bin)
			etag := `"` + hex.EncodeToString(sum[:16]) + `"`
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write(bin)
			return
		}
		http.NotFound(w, r)
	})
}

// entityClaimsEmail reports whether any self-signed identity on the key
// carries exactly this email address (case-insensitive). A WKD response whose
// key claims nothing about the queried address is not proof of the recipient:
// auto-encrypting to it would hand the plaintext to whoever controls that
// WKD record (audit: unpinned TOFU without UID validation).
func entityClaimsEmail(ent *openpgp.Entity, email string) bool {
	if ent == nil {
		return false
	}
	want := strings.ToLower(strings.TrimSpace(email))
	if want == "" {
		return false
	}
	for _, id := range ent.Identities {
		if id.UserId == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(id.UserId.Email), want) {
			return true
		}
	}
	return false
}

// LookupExternalKey discovers a recipient's public key via WKD over HTTPS,
// trying the advanced method first and then the direct method. A fetched key is
// accepted only if it self-identifies with the queried address; anything else
// is treated as not-found rather than trusted for auto-encryption.
func (e *Engine) LookupExternalKey(email string) (*PublicKey, error) {
	// In a Tor Space (OnionMode, ADR-0141) a WKD discovery is an outbound HTTPS
	// call to the recipient's clearnet domain — a network leak that would correlate
	// the anonymous world. Never fetch external keys there; encryption uses only
	// keys already in the local keystore.
	if config.Cfg.OnionMode {
		return nil, ErrNotFound
	}
	local, domain := splitEmail(normalizeEmail(email))
	if domain == "" || !validExternalWKDDomain(domain) {
		return nil, ErrNotFound
	}
	hash := wkdLocalHash(local)
	lq := url.QueryEscape(local)
	urls := []string{
		"https://openpgpkey." + domain + "/.well-known/openpgpkey/" + domain + "/hu/" + hash + "?l=" + lq,
		"https://" + domain + "/.well-known/openpgpkey/hu/" + hash + "?l=" + lq,
	}
	for _, u := range urls {
		req, err := http.NewRequest(http.MethodGet, u, nil)
		if err != nil {
			continue
		}
		resp, err := e.wkdClient.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			continue
		}
		// Re-armor the binary key for uniform downstream handling.
		var buf bytes.Buffer
		aw, err := armor.Encode(&buf, "PGP PUBLIC KEY BLOCK", nil)
		if err != nil {
			continue
		}
		if _, err := aw.Write(body); err != nil {
			_ = aw.Close()
			continue
		}
		if err := aw.Close(); err != nil {
			continue
		}
		pk, err := e.ImportPublicKey(buf.Bytes())
		if err != nil {
			continue
		}
		if ent, entErr := entityFromArmor(buf.String()); entErr != nil || !entityClaimsEmail(ent, email) {
			// The server answered, but with a key that does not claim this
			// address: not the recipient's key, refuse it.
			continue
		}
		pk.Email = email
		pk.Source = "wkd"
		return pk, nil
	}
	return nil, ErrNotFound
}

// onionWKDHostRe matches a bare Tor v3 onion hostname exactly (56 base32 chars +
// ".onion") — the only host shape LookupOnionKey will open a WKD request to.
var onionWKDHostRe = regexp.MustCompile(`^[a-z2-7]{56}\.onion$`)

// LookupOnionKey discovers a recipient's public key via WKD served over the
// recipient's own .onion, using the caller-supplied HTTP client (which the caller
// routes through Tor and restricts to .onion hosts — ADR-0142). Unlike
// LookupExternalKey this is the intended path in a Tor Space: it only ever builds
// http://<onion>/… URLs and refuses any non-onion domain, so it introduces no
// clearnet leak. The armored public key is returned for local encryption; the
// signature it carries is what a later verification step checks.
func (e *Engine) LookupOnionKey(email string, client *http.Client) (*PublicKey, error) {
	if client == nil {
		return nil, ErrNotFound
	}
	local, domain := splitEmail(normalizeEmail(email))
	// Strict SSRF gate: the fetch URL is built from domain, so it must be an exact
	// bare v3 onion (56 base32 chars + ".onion") — a recipient address can never
	// steer the request at an arbitrary host, port or path.
	domain = strings.ToLower(strings.TrimSpace(domain))
	if local == "" || !onionWKDHostRe.MatchString(domain) {
		return nil, ErrNotFound
	}
	hash := wkdLocalHash(local)
	lq := url.QueryEscape(local)
	// Onion sites serve their own WKD (no openpgpkey subdomain); ServeWKD matches
	// any path containing /hu/<hash>, so the direct method suffices. http only —
	// there is no CA-TLS on an .onion.
	u := "http://" + domain + "/.well-known/openpgpkey/hu/" + hash + "?l=" + lq
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, ErrNotFound
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrNotFound
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		return nil, ErrNotFound
	}
	var buf bytes.Buffer
	aw, err := armor.Encode(&buf, "PGP PUBLIC KEY BLOCK", nil)
	if err != nil {
		return nil, ErrNotFound
	}
	if _, err := aw.Write(body); err != nil {
		_ = aw.Close()
		return nil, ErrNotFound
	}
	if err := aw.Close(); err != nil {
		return nil, ErrNotFound
	}
	pk, err := e.ImportPublicKey(buf.Bytes())
	if err != nil {
		return nil, ErrNotFound
	}
	if ent, entErr := entityFromArmor(buf.String()); entErr != nil || !entityClaimsEmail(ent, email) {
		// Same rule as clearnet WKD: a key that does not claim the queried
		// address is not the recipient's key, whoever serves it.
		return nil, ErrNotFound
	}
	pk.Email = email
	pk.Source = "wkd-onion"
	return pk, nil
}
