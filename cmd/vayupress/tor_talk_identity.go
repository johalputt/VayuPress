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
	"regexp"
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

// anonTalkHandleRe matches an anonymous VayuTalk handle minted by newTalkAnonID
// ("anon" + base32-no-padding of 8 random bytes = 13 chars). These are throwaway
// Tor-world chat identities, not mailboxes, so we key off this exact shape to keep
// them out of the mailbox PGP manager (a rotated handle would otherwise pile up
// there — every rotate mints a fresh keypair and the old one is never a mailbox).
var anonTalkHandleRe = regexp.MustCompile(`^anon[a-z2-7]{13}@`)

// isAnonTalkHandle reports whether email is an anonymous VayuTalk chat handle
// rather than a real mailbox address.
func isAnonTalkHandle(email string) bool {
	return anonTalkHandleRe.MatchString(strings.ToLower(strings.TrimSpace(email)))
}

// hostPart returns the host (after "@") of a chat address, lower-cased. "" when
// the address has no host.
func hostPart(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if i := strings.LastIndex(addr, "@"); i >= 0 && i+1 < len(addr) {
		return addr[i+1:]
	}
	return ""
}

// talkHostIsOnion reports whether host is a Tor onion hostname (a non-empty label
// followed by ".onion"). Used to classify a chat recipient as living on an onion.
// Distinct from isOnionHost (request-Host middleware): this takes a bare host
// string and requires a real label before ".onion".
func talkHostIsOnion(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return len(host) > len(".onion") && strings.HasSuffix(host, ".onion")
}

// talkOnionFederationEnabled reports whether the operator has opted into
// experimental onion-to-onion VayuTalk delivery (ADR-0142). Always false outside
// the Tor world — federation only has meaning there.
func (a *App) talkOnionFederationEnabled(ctx context.Context) bool {
	if !config.Cfg.OnionMode || a.siteSettings == nil {
		return false
	}
	return a.siteSettings.FeatureEnabled(ctx, settings.KeyTalkOnionFederation)
}

// talkRecipientRemoteOnion reports whether recipient `to` is a chat code hosted
// on a DIFFERENT .onion than our own identity `self` — i.e. a message that can
// only be delivered by onion-to-onion federation, not the local in-process hub.
func talkRecipientRemoteOnion(self, to string) bool {
	toHost := hostPart(to)
	if !talkHostIsOnion(toHost) {
		return false
	}
	return toHost != hostPart(self)
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

// handleVayuOSTalkFederationToggle flips the experimental onion-to-onion delivery
// opt-in (ADR-0142). CSRF-checked + admin-only + Tor-world-only. The client
// reloads to reflect the new state.
func (a *App) handleVayuOSTalkFederationToggle(w http.ResponseWriter, r *http.Request) {
	if !config.Cfg.OnionMode {
		writeAPIError(w, r, http.StatusBadRequest, "not-tor", "onion-to-onion delivery exists only in the Tor world", "")
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
	// Flip relative to the current effective state.
	next := "on"
	if a.talkOnionFederationEnabled(r.Context()) {
		next = "off"
	}
	if err := a.siteSettings.SetMany(r.Context(), map[string]string{settings.KeyTalkOnionFederation: next}); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "write_failed", "could not save the setting", "")
		return
	}
	// Nudge the Tor engine so the managed tor opens/closes its loopback SOCKS port
	// promptly rather than at the next 60s reconcile tick (ADR-0142).
	if a.vayuTor != nil {
		a.vayuTor.Kick()
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "enabled": next == "on"})
}
