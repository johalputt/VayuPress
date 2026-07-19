package main

// tor_talk_identity.go — the ANONYMOUS, rotatable VayuTalk identity for the Tor
// world (ADR-0141). In the clearnet world your chat identity is your mailbox
// address; in the Tor world that would link your chat to a mail account, so here
// the identity is a random handle (`<random>@<onion>`) with no mailbox behind it.
// "Rotate" mints a brand-new handle + keypair on demand, so you can hand out a
// throwaway code and drop it whenever you like. The relay keys purely on the
// opaque handle string, so no engine change is needed — only the identity source.

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/settings"
)

// newTalkAnonID returns a fresh random Talk handle local-part (base32, lower-case,
// prefixed so it is always a valid mail/PGP local-part). "" only on CSPRNG failure.
func newTalkAnonID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	s := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
	return "anon" + s
}

// talkAnonAddress returns the operator's anonymous VayuTalk address in the Tor
// world (`<random>@<onion>`), generating and persisting a handle on first use. It
// returns "" outside OnionMode (the clearnet world uses mailbox identities).
func (a *App) talkAnonAddress(ctx context.Context) string {
	if !config.Cfg.OnionMode || a.siteSettings == nil {
		return ""
	}
	onion := strings.ToLower(strings.TrimSpace(config.Cfg.Domain))
	if onion == "" {
		return ""
	}
	id := strings.TrimSpace(a.siteSettings.Get(ctx, settings.KeyTalkAnonID))
	if id == "" {
		if id = newTalkAnonID(); id == "" {
			return ""
		}
		_ = a.siteSettings.SetMany(ctx, map[string]string{settings.KeyTalkAnonID: id})
	}
	return id + "@" + onion
}

// handleVayuOSTalkRotate mints a brand-new anonymous Talk handle (and its keypair)
// for the Tor world. CSRF-checked + admin-only; the client reloads to show it.
func (a *App) handleVayuOSTalkRotate(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.OnionMode {
		writeAPIError(w, r, http.StatusBadRequest, "not-tor", "the anonymous chat code exists only in the Tor world", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "administrators only", "")
		return
	}
	if a.siteSettings == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "settings unavailable", "")
		return
	}
	id := newTalkAnonID()
	if id == "" {
		writeAPIError(w, r, http.StatusInternalServerError, "rng", "could not generate a code", "")
		return
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTalkAnonID: id}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "write_failed", "could not save the new code", "")
		return
	}
	addr := id + "@" + strings.ToLower(strings.TrimSpace(config.Cfg.Domain))
	a.ensureTalkKeypair(addr) // mint the key so the fresh code is usable immediately
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "id": addr})
}
