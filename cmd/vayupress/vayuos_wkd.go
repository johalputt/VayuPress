// vayuos_wkd.go — WKD (Web Key Directory) handler for VayuOS.
//
// Serves the RFC 9579 Web Key Directory endpoint for PGP key discovery.
// External clients (Thunderbird, GPG) query this endpoint to find
// public keys for VayuPress users automatically.
package main

import (
	"crypto/sha1"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// GET /.well-known/openpgpkey/{domain}/hu/{hash}
// Public endpoint — no authentication required per RFC 9579.
func (a *App) handleWKDLookup(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	if a.vayuPGP == nil || a.vayuPGP.Config() == nil || !a.vayuPGP.Config().WKDEnabled {
		http.NotFound(w, r)
		return
	}

	keys := a.vayuPGP.PublishedKeys()
	for email, key := range keys {
		localHash := wkdHash(email)
		if localHash == hash {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(key))
			return
		}
		_ = email // used in range
	}
	_ = keys // used in range
	http.NotFound(w, r)
}

// wkdHash computes the WKD hash (SHA-1 of lowercased local part, z-base32).
func wkdHash(email string) string {
	const zBase32 = "ybndrfg8ejkmcpqxot1uwisza345h769"
	idx := strings.LastIndex(email, "@")
	if idx < 0 {
		return ""
	}
	h := sha1.Sum([]byte(strings.ToLower(email[:idx])))
	var sb strings.Builder
	bits, val := 0, 0
	for _, b := range h {
		val = (val << 8) | int(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			sb.WriteByte(zBase32[(val>>bits)&31])
		}
	}
	if bits > 0 {
		sb.WriteByte(zBase32[(val<<(5-bits))&31])
	}
	return sb.String()
}