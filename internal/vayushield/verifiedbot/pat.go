// SPDX-License-Identifier: Apache-2.0

package verifiedbot

// pat.go — PAT attestation (2025 plan Wave 4, item 8): a crawler presenting a
// valid Personal Access Token is attested by the credential itself, not by
// which network it calls from. The operator issues the token out-of-band to a
// partner crawler; Config.PATAttest resolves presented tokens against that
// registry. Verifier keeps no token material — only the resolver's verdict,
// cached briefly by token hash so hot crawlers don't re-resolve per request.

import (
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	patCacheTTL  = 5 * time.Minute
	maxPATLength = 256
)

type patEntry struct {
	vendor string
	class  Class
	expiry time.Time
}

// AttestByPAT returns (Verified, vendor, class) when the request presents a
// credential the configured resolver recognises; otherwise (Unknown, "", "").
// Tokens are read from "Authorization: Bearer <token>" or "X-Bot-PAT".
func (v *Verifier) AttestByPAT(r *http.Request) (Verdict, string, Class) {
	if v == nil || r == nil || v.cfg.PATAttest == nil {
		return Unknown, "", ""
	}
	token := bearerToken(r)
	if token == "" || len(token) > maxPATLength {
		return Unknown, "", ""
	}
	sum := sha256.Sum256([]byte(token))
	key := string(sum[:])

	v.patMu.Lock()
	e, ok := v.patCache[key]
	now := v.now()
	if ok && now.Before(e.expiry) {
		v.patMu.Unlock()
		if e.vendor != "" {
			v.note(e.vendor)
			return Verified, e.vendor, e.class
		}
		return Unknown, "", ""
	}
	v.patMu.Unlock()

	vendor, class, ok := v.cfg.PATAttest(token)
	if !ok {
		// Negative caching too: a flood of bad tokens must not turn the
		// resolver into the new hot path.
		v.patMu.Lock()
		v.patCache[key] = patEntry{expiry: now.Add(patCacheTTL)}
		v.patMu.Unlock()
		return Unknown, "", ""
	}
	v.patMu.Lock()
	v.patCache[key] = patEntry{vendor: vendor, class: class, expiry: now.Add(patCacheTTL)}
	v.patMu.Unlock()
	v.note(vendor)
	return Verified, vendor, class
}

// bearerToken extracts the presented credential.
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	return strings.TrimSpace(r.Header.Get("X-Bot-PAT"))
}

var _ = sync.Mutex{} // patMu guards patCache (declared on Verifier in verifier.go)
