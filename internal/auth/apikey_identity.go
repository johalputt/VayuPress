// SPDX-License-Identifier: Apache-2.0

package auth

// apikey_identity.go — the enforcement-time identity of an API-key caller.
//
// Stage 2 of VayuAPI (ADR-0134): authentication tells us a key is genuine;
// enforcement needs to know WHICH key it is and what it may do. This file adds
// the resolver hook (the rich sibling of the boolean verifier), stamps the
// resolved apikeys.KeyInfo into the request context, and exposes it to the
// permission middleware in cmd. The static bootstrap API_KEY resolves to an
// implicit superuser identity so existing automation is unaffected.

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/config"
)

// BootstrapKeyID is the stable identity stamped for callers of the static
// bootstrap API_KEY (config.Cfg.APIKey). It is one of exactly two implicit
// superusers (the other is the internal system key).
const BootstrapKeyID = "bootstrap-static"

// extraAPIKeyResolver is the optional, runtime-registered rich resolver for
// database-backed keys. Where the verifier answers "is this key valid?", the
// resolver answers "whose key is this and what may it do?". Registered in main
// alongside the verifier once the key store is ready.
var extraAPIKeyResolver func(presentedKey string) (apikeys.KeyInfo, bool)

// SetExtraAPIKeyResolver registers (or clears, with nil) the rich API-key
// resolver consulted by RequireAPIKey and ResolveValidAPIKey. Shares the
// verifier's mutex so registration is race-free against request handling.
func SetExtraAPIKeyResolver(fn func(presentedKey string) (apikeys.KeyInfo, bool)) {
	extraAPIKeyMu.Lock()
	extraAPIKeyResolver = fn
	extraAPIKeyMu.Unlock()
}

// resolveExtraAPIKey resolves a presented key to its enforcement identity. When
// only the legacy boolean verifier is registered (no resolver), a passing key is
// stamped superuser for backward compatibility — exactly the pre-Stage-2
// behaviour where any issued key had full access.
func resolveExtraAPIKey(key string) (apikeys.KeyInfo, bool) {
	if key == "" {
		return apikeys.KeyInfo{}, false
	}
	extraAPIKeyMu.RLock()
	resolve := extraAPIKeyResolver
	verify := extraAPIKeyVerifier
	extraAPIKeyMu.RUnlock()
	if resolve != nil {
		return resolve(key)
	}
	if verify != nil && verify(key) {
		return apikeys.SuperuserKeyInfo("legacy-verified", "Issued key (legacy verifier)", apikeys.ScopeExternal), true
	}
	return apikeys.KeyInfo{}, false
}

// presentedAPIKey extracts the raw API key from the request headers
// (X-API-Key, or Authorization: Bearer). Empty when none is presented.
func presentedAPIKey(r *http.Request) string {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	return key
}

// ResolveValidAPIKey authenticates the request's API key (static bootstrap or
// issued) and returns its enforcement identity. This is the single shared path
// behind both RequireAPIKey (the /api middleware) and HasValidAPIKey (the
// session-or-key gate on /os), so the two surfaces can never disagree about
// which keys are valid or what identity they carry.
func ResolveValidAPIKey(r *http.Request) (apikeys.KeyInfo, bool) {
	key := presentedAPIKey(r)
	if key == "" {
		return apikeys.KeyInfo{}, false
	}
	// Constant-time comparison for the static key (prevents byte-at-a-time
	// timing leaks); an empty configured key is never a valid credential.
	if config.Cfg.APIKey != "" && subtle.ConstantTimeCompare([]byte(key), []byte(config.Cfg.APIKey)) == 1 {
		return apikeys.SuperuserKeyInfo(BootstrapKeyID, "Bootstrap API_KEY", apikeys.ScopeExternal), true
	}
	return resolveExtraAPIKey(key)
}

// keyInfoCtxKey is the private context key carrying the caller's KeyInfo.
type keyInfoCtxKey struct{}

// RequestWithKeyInfo returns r with the caller's resolved key identity attached,
// so downstream permission middleware and audit logging can read it.
func RequestWithKeyInfo(r *http.Request, ki apikeys.KeyInfo) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), keyInfoCtxKey{}, ki))
}

// KeyInfoFromContext returns the API-key identity stamped by RequireAPIKey (or
// the session-or-key gate), and whether the caller authenticated with a key at
// all. Session-authenticated operators carry no KeyInfo — their access is
// governed by session RBAC, not key grants.
func KeyInfoFromContext(ctx context.Context) (apikeys.KeyInfo, bool) {
	ki, ok := ctx.Value(keyInfoCtxKey{}).(apikeys.KeyInfo)
	return ki, ok
}
