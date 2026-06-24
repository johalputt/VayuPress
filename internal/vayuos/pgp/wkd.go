package pgp

import (
	"bytes"
	"crypto/sha1"
	"io"
	"net/http"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp/armor"
)

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

// wkdLocalHash returns the z-base-32 SHA-1 hash of the lowercased local-part,
// as required for WKD lookups.
func wkdLocalHash(localpart string) string {
	sum := sha1.Sum([]byte(strings.ToLower(localpart)))
	return zbase32(sum[:])
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

// ServeWKD returns an http.Handler implementing the WKD advanced method for the
// given domain at /.well-known/openpgpkey/<domain>/hu/<hash> plus the policy
// file. It serves the binary public key for any locally-held address.
func (e *Engine) ServeWKD(domain string) http.Handler {
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
		recs, err := e.ks.list()
		if err != nil {
			http.Error(w, "wkd unavailable", http.StatusInternalServerError)
			return
		}
		for _, rec := range recs {
			local, _ := splitEmail(rec.Email)
			if wkdLocalHash(local) != hash {
				continue
			}
			bin, err := dearmorPublic(rec.PublicArmor)
			if err != nil {
				http.Error(w, "wkd key error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			_, _ = w.Write(bin)
			return
		}
		http.NotFound(w, r)
	})
}

// LookupExternalKey discovers a recipient's public key via WKD over HTTPS,
// trying the advanced method first and then the direct method.
func (e *Engine) LookupExternalKey(email string) (*PublicKey, error) {
	local, domain := splitEmail(normalizeEmail(email))
	if domain == "" {
		return nil, ErrNotFound
	}
	hash := wkdLocalHash(local)
	urls := []string{
		"https://openpgpkey." + domain + "/.well-known/openpgpkey/" + domain + "/hu/" + hash + "?l=" + local,
		"https://" + domain + "/.well-known/openpgpkey/hu/" + hash + "?l=" + local,
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
		pk.Email = email
		pk.Source = "wkd"
		return pk, nil
	}
	return nil, ErrNotFound
}
