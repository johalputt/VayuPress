// Package pgp — WKD (Web Key Directory) server.
//
// Implements RFC 9579 Web Key Directory lookup for VayuPGP.
//
// WKD allows email clients (Thunderbird, GPG) to automatically discover
// public PGP keys by constructing a URL from the email address:
//
//	https://openpgpkey.domain/.well-known/openpgpkey/domain/hu/<hash>
//
// The hash is a z-base-32 encoded SHA-1 of the local-part, lowercased.
// VayuOS auto-configures WKD when a domain is added and publishes all
// user public keys automatically.
//
// References:
//   - RFC 9579: https://datatracker.ietf.org/doc/html/rfc9579
//   - WKD spec:  https://wiki.gnupg.org/WKD
package pgp

import (
	"crypto/sha1"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"strings"
)

// zBase32 alphabet from RFC 6189 Section 5.1.6 / RFC 9579 §4.
const zBase32 = "ybndrfg8ejkmcpqxot1uwisza345h769"

// WKDServer serves the WKD discovery endpoint.
type WKDServer struct {
	domain  string
	keys    map[string]*PublicKey // email -> key
	pubKeys map[string]string     // localPartHash -> armored key
}

// NewWKDServer creates a WKD server for the given domain.
func NewWKDServer(domain string) *WKDServer {
	return &WKDServer{
		domain:  domain,
		keys:    make(map[string]*PublicKey),
		pubKeys: make(map[string]string),
	}
}

// PublishKey stores a public key for WKD discovery.
func (w *WKDServer) PublishKey(key *PublicKey) {
	w.keys[key.Email] = key
	hash := wkdHash(key.Email)
	w.pubKeys[hash] = key.Armor
}

// RemoveKey removes a public key from WKD.
func (w *WKDServer) RemoveKey(email string) {
	delete(w.keys, email)
	hash := wkdHash(email)
	delete(w.pubKeys, hash)
}

// Handler returns an http.Handler for the WKD endpoint.
//
// Handles two request types:
//   - Direct lookup (RFC 9579 §4.1):
//     GET /.well-known/openpgpkey/<domain>/hu/<hash>?l=<local-part>
//   - Advanced method (RFC 9579 §4.2):
//     Proxied through the submission address lookup
func (w *WKDServer) Handler() http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		// Extract hash from URL path
		// Path format: /.well-known/openpgpkey/<domain>/hu/<hash>
		path := strings.TrimSuffix(req.URL.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) < 5 || parts[len(parts)-2] != "hu" {
			http.NotFound(rw, req)
			return
		}
		hash := parts[len(parts)-1]

		// Look up the key
		armor, ok := w.pubKeys[hash]
		if !ok {
			// RFC 9579 §4.1: Return 404 if no key found
			http.NotFound(rw, req)
			return
		}

		// Direct lookup — return binary (RFC 9579 §4.1)
		// The standard response is application/octet-stream with the armored key
		rw.Header().Set("Content-Type", "application/octet-stream")
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte(armor))
	})
}

// HealthPage returns an informational WKD status page for the VayuOS panel.
func (w *WKDServer) HealthPage() htmpl.HTML {
	keyCount := len(w.keys)
	keysHTML := ""
	for email, key := range w.keys {
		hash := wkdHash(email)
		keysHTML += `<tr><td>` + html.EscapeString(email) + `</td>
<td><code>` + html.EscapeString(key.Fingerprint) + `</code></td>
<td><code>` + html.EscapeString(hash) + `</code></td>
<td><span class="badge badge-green">published</span></td></tr>`
	}

	if keysHTML == "" {
		keysHTML = `<tr><td colspan="4" class="empty-state">No keys published yet</td></tr>`
	}

	return htmpl.HTML(fmt.Sprintf(`<div class="page-header"><h1>WKD Keyserver</h1>
<p>RFC 9579 Web Key Directory • %s • Endpoint: <code>https://openpgpkey.%s/.well-known/openpgpkey/%s/hu/&lt;hash&gt;</code></p>
</div>
<div class="card"><div class="card-title">Published Keys • %d key(s)</div>
<div class="table-wrap"><table class="table">
<thead><tr><th>Email</th><th>Fingerprint</th><th>WKD Hash</th><th>Status</th></tr></thead>
<tbody>%s</tbody></table></div></div>`,
		w.domain, w.domain, w.domain, keyCount, keysHTML))
}

// wkdHash computes the WKD hash for an email address.
// Per RFC 9579 §4: SHA-1 of the lowercased local part, z-base-32 encoded.
func wkdHash(email string) string {
	idx := strings.LastIndex(email, "@")
	if idx < 0 {
		return ""
	}
	localPart := strings.ToLower(email[:idx])
	h := sha1.Sum([]byte(localPart))
	// Encode first 160 bits (20 bytes) in z-base-32
	return zBase32Encode(h[:])
}

// zBase32Encode encodes data in z-base-32 per RFC 6189.
func zBase32Encode(data []byte) string {
	var result strings.Builder
	bits := 0
	val := 0
	for _, b := range data {
		val = (val << 8) | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			result.WriteByte(zBase32[(val>>bits)&31])
		}
	}
	if bits > 0 {
		result.WriteByte(zBase32[(val<<(5-bits))&31])
	}
	return result.String()
}