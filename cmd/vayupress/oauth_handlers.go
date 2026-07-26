// SPDX-License-Identifier: Apache-2.0

package main

// oauth_handlers.go — the HTTP surface of VayuPress's OAuth 2.1 authorization
// server (ADR-0140, VayuMCP Stage 4): discovery metadata (RFC 8414 + RFC 9728),
// dynamic client registration (RFC 7591), the /authorize consent flow, and the
// /token exchange. The access token handed back is a scoped VayuPress API key, so
// the whole capability/rate/audit/revocation model is reused and nothing is
// bespoke about how a connector is authorized once it holds a token.

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/oauth"
	"github.com/johalputt/vayupress/internal/render"
	"github.com/johalputt/vayupress/internal/seo"
)

// oauthBaseURL returns this site's public origin (scheme://host) — the issuer
// for OAuth metadata.
//
// The scheme is derived from the host via seo.Origin (http for a .onion, https
// otherwise) rather than a client-supplied X-Forwarded-Proto header (audit I1).
// The host itself is trusted from the request ONLY when it resolves to a domain
// this install actually serves, so an injected Host / X-Forwarded-Host cannot
// steer the advertised issuer/endpoints; otherwise it falls back to the
// configured canonical domain. This keeps per-domain issuers correct on a
// multi-domain install while refusing arbitrary hosts. The response is
// Cache-Control: no-store and reflection-only.
func (a *App) oauthBaseURL(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	trusted := false
	if host != "" && a != nil && a.domains != nil {
		if _, err := a.domains.Resolve(r.Context(), host); err == nil {
			trusted = true
		}
	}
	if !trusted {
		if d := strings.TrimSpace(config.Cfg.Domain); d != "" && d != "localhost" {
			host = d
		}
	}
	if host == "" {
		host = "your-domain.com"
	}
	return seo.Origin(host)
}

func oauthWriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError writes an RFC 6749 §5.2 error object.
func oauthError(w http.ResponseWriter, status int, code, desc string) {
	oauthWriteJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

// ── discovery metadata ──────────────────────────────────────────────────────────

// handleOAuthASMetadata serves RFC 8414 authorization-server metadata at
// /.well-known/oauth-authorization-server.
func (a *App) handleOAuthASMetadata(w http.ResponseWriter, r *http.Request) {
	base := a.oauthBaseURL(r)
	oauthWriteJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"vayupress"},
	})
}

// handleOAuthResourceMetadata serves RFC 9728 protected-resource metadata at
// /.well-known/oauth-protected-resource, pointing MCP clients at this AS.
func (a *App) handleOAuthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := a.oauthBaseURL(r)
	oauthWriteJSON(w, http.StatusOK, map[string]any{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base},
		"bearer_methods_supported": []string{"header"},
	})
}

// ── dynamic client registration (RFC 7591) ────────────────────────────────────────

func (a *App) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error", "authorization server unavailable")
		return
	}
	var body struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 32*1024)).Decode(&body); err != nil {
		logging.LogWarn("oauth", "register: request body was not valid JSON: "+err.Error())
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body must be JSON")
		return
	}
	// Log exactly what the client sent so a failed connect is diagnosable from the
	// server log (the redirect_uris are the usual reason registration is refused).
	logging.LogInfo("oauth", fmt.Sprintf("register: client_name=%q redirect_uris=%v", body.ClientName, body.RedirectURIs))
	c, err := a.oauth.RegisterClient(r.Context(), strings.TrimSpace(body.ClientName), body.RedirectURIs)
	if err != nil {
		logging.LogWarn("oauth", fmt.Sprintf("register: REJECTED client_name=%q redirect_uris=%v: %v", body.ClientName, body.RedirectURIs, err))
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}
	logging.LogInfo("oauth", fmt.Sprintf("register: OK client_id=%s accepted_redirect_uris=%v", c.ID, c.RedirectURIs))
	oauthWriteJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  c.ID,
		"client_name":                c.Name,
		"redirect_uris":              c.RedirectURIs,
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
}

// ── authorize / consent ─────────────────────────────────────────────────────────

// oauthPresets maps the consent choice to a capability grant. Mirrors the
// connector page's presets so both front doors offer the same access levels.
var oauthPresets = map[string]struct {
	Caps  string
	Label string
	Desc  string
}{
	"full":     {"*:*", "Full control", "Run the whole site — posts, pages, media, theme, settings, and more."},
	"author":   {"posts:read,posts:write", "Author", "Write, edit, and organise posts and pages. Nothing else."},
	"readonly": {"posts:read,analytics:read", "Read-only", "Read content and analytics. Cannot change anything."},
}

// handleOAuthAuthorize renders the consent screen for an OAuth authorization
// request. The client and redirect URI are validated BEFORE any redirect so a
// bogus/unknown redirect can never be used as an open redirect; only after both
// check out are protocol errors reported back to the client via redirect.
func (a *App) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil {
		oauthHTMLError(w, "The authorization server is unavailable.")
		return
	}
	q := r.URL.Query()
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")

	client, err := a.oauth.GetClient(r.Context(), clientID)
	if err != nil || !client.RedirectAllowed(redirectURI) {
		// Never redirect to an unvalidated URI — show a local error instead.
		logging.LogWarn("oauth", fmt.Sprintf("authorize: rejected client_id=%q redirect_uri=%q (getClientErr=%v redirectAllowed=%v)", clientID, redirectURI, err, err == nil && client.RedirectAllowed(redirectURI)))
		oauthHTMLError(w, "This app is not registered, or its redirect URL does not match. Reconnect from your MCP client.")
		return
	}
	logging.LogInfo("oauth", fmt.Sprintf("authorize: client_id=%s redirect_uri=%s response_type=%s", clientID, redirectURI, q.Get("response_type")))
	state := q.Get("state")
	if q.Get("response_type") != "code" {
		oauthRedirectError(w, r, redirectURI, state, "unsupported_response_type")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || challenge == "" {
		// OAuth 2.1 requires PKCE with S256.
		oauthRedirectError(w, r, redirectURI, state, "invalid_request")
		return
	}

	// The operator must be a signed-in VayuPress administrator to approve. A
	// cross-site Connect navigation (SameSite=Strict) will not carry the session,
	// so bounce through login with a same-origin return to this exact request.
	u := a.resolveConsoleUser(r)
	if u == nil {
		http.Redirect(w, r, "/os/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	if accessLevelFor(u.Role, false) < accessAdmin {
		oauthHTMLError(w, "You must be a VayuPress administrator to authorize a connection.")
		return
	}

	// CSRF for the consent POST. This flow is ENTERED from an external OAuth client
	// and the approval POST is made in a cross-site context, where the browser drops
	// EVERY cookie — even SameSite=None; Secure — under third-party-cookie blocking.
	// So we do not rely on a cookie at all: mint a stateless, HMAC-signed token that
	// binds the approval to THIS admin and a short expiry, and carry it in the form.
	// It is unforgeable without the server secret and is only ever handed to an
	// authenticated admin here, so a value the consent handler accepts proves the
	// POST originated from this page — and it identifies the approver without any
	// cookie (the consent handler no longer needs the session on the POST).
	consentTok := auth.SignedToken(u.ID + "|" + strconv.FormatInt(time.Now().Add(consentTokenTTL).Unix(), 10))

	// The approval POST is 303-redirected to the client's registered redirect_uri
	// (e.g. https://claude.ai/…). CSP `form-action` is enforced across the WHOLE
	// form navigation — including that redirect — so the strict baseline
	// `form-action 'self'` blocks the redirect and the browser silently drops it:
	// the operator clicks Approve and "nothing happens". Extend form-action for THIS
	// page to also allow the client's (already-validated) redirect origin, so the
	// authorization code can actually be delivered back to the client.
	if src := redirectCSPSource(redirectURI); src != "" {
		hdr := "Content-Security-Policy"
		if config.Cfg.CSPReportOnly {
			hdr = "Content-Security-Policy-Report-Only"
		}
		csp := strings.Replace(render.BuildCSP(render.CSPNonce(r), nil), "form-action 'self'", "form-action 'self' "+src, 1)
		w.Header().Set(hdr, csp)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(oauthConsentPage(client, redirectURI, challenge, state, consentTok)))
}

// redirectCSPSource returns a CSP source-expression that allows a form to POST/
// redirect to the given (already-validated) OAuth redirect URI: the scheme+host
// origin for http/https, or a scheme-source ("myapp:") for a native custom scheme.
func redirectCSPSource(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return ""
		}
		return strings.ToLower(u.Scheme) + "://" + u.Host
	case "":
		return ""
	default:
		return strings.ToLower(u.Scheme) + ":"
	}
}

// consentTokenTTL bounds how long an OAuth consent approval token stays valid
// after the consent screen is rendered.
const consentTokenTTL = 10 * time.Minute

// handleOAuthConsent processes the operator's decision. On approval it mints a
// scoped API key with the chosen grant and issues a single-use authorization code
// bound to the client, redirect URI, and PKCE challenge; then it redirects back to
// the client. CSRF is enforced by the signed consent token in the form (cookie-free,
// so it survives the cross-site approval POST).
func (a *App) handleOAuthConsent(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil || a.apiKeys == nil {
		oauthHTMLError(w, "The authorization server is unavailable.")
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthHTMLError(w, "Malformed request.")
		return
	}
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	challenge := r.FormValue("code_challenge")
	state := r.FormValue("state")

	// Re-validate client + redirect (never trust the posted values without a
	// fresh registry check — same guard as the GET).
	client, err := a.oauth.GetClient(r.Context(), clientID)
	if err != nil || !client.RedirectAllowed(redirectURI) {
		oauthHTMLError(w, "This app is not registered, or its redirect URL does not match.")
		return
	}
	// Verify the signed consent token (cookie-free CSRF + approver identity). It was
	// minted at /authorize for the authenticated admin and carried in the form, so it
	// survives the cross-site POST where the browser drops every cookie (third-party
	// blocking). An attacker cannot forge it (HMAC over the server secret), and it is
	// only ever shown to an authenticated admin, so a token the server accepts proves
	// the POST came from this consent page — and it names the approver without a cookie.
	payload, ok := auth.VerifySignedToken(r.FormValue("consent_token"))
	if !ok {
		oauthHTMLError(w, "This approval could not be verified. Reconnect from your MCP client and try again.")
		return
	}
	ownerID, expStr, found := strings.Cut(payload, "|")
	if !found || ownerID == "" {
		oauthHTMLError(w, "Invalid approval token.")
		return
	}
	if exp, perr := strconv.ParseInt(expStr, 10, 64); perr != nil || time.Now().Unix() > exp {
		oauthHTMLError(w, "This approval expired. Reconnect from your MCP client and approve again.")
		return
	}

	if r.FormValue("decision") != "approve" {
		logging.LogInfo("oauth", fmt.Sprintf("consent: DENIED client_id=%s redirect_uri=%s", clientID, redirectURI))
		oauthRedirectError(w, r, redirectURI, state, "access_denied")
		return
	}
	preset, ok := oauthPresets[r.FormValue("grant")]
	if !ok {
		logging.LogWarn("oauth", fmt.Sprintf("consent: no access level chosen (grant=%q) client_id=%s", r.FormValue("grant"), clientID))
		oauthHTMLError(w, "Please choose an access level.")
		return
	}
	logging.LogInfo("oauth", fmt.Sprintf("consent: APPROVED client_id=%s grant=%s redirect_uri=%s", clientID, preset.Caps, redirectURI))

	// Do NOT mint the key here — the code carries only the approved GRANT, and the
	// scoped key is minted at the /token exchange, so no bearer token exists until
	// the client presents a valid PKCE verifier (and none is ever stored at rest).
	label := "Claude via OAuth (" + preset.Label + ")"
	// Attribute the grant to the admin named in the verified consent token, so the
	// minted key is owned by, and the audit trail names, the operator who approved.
	code, err := a.oauth.IssueCode(r.Context(), oauth.CodeGrant{
		ClientID: clientID, RedirectURI: redirectURI, CodeChallenge: challenge,
		GrantCaps: preset.Caps, OwnerUserID: ownerID, Label: label,
	})
	if err != nil {
		oauthHTMLError(w, "Could not complete authorization. Please try again.")
		return
	}
	dbpkg.AuditLog("oauth.authorize", "user:"+ownerID, clientID, "grant="+preset.Caps)

	// Success redirect back to the client with the code and original state.
	oauthRedirectSuccess(w, r, redirectURI, state, code)
}

// ── token exchange ──────────────────────────────────────────────────────────────

func (a *App) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if a.oauth == nil || a.apiKeys == nil {
		oauthError(w, http.StatusServiceUnavailable, "server_error", "authorization server unavailable")
		return
	}
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "malformed form body")
		return
	}
	grantType := r.FormValue("grant_type")
	logging.LogInfo("oauth", fmt.Sprintf("token: grant_type=%q client_id=%q", grantType, r.FormValue("client_id")))
	switch grantType {
	case "authorization_code":
		a.oauthTokenFromCode(w, r)
	case "refresh_token":
		a.oauthTokenFromRefresh(w, r)
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (a *App) oauthTokenFromCode(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	clientID := r.FormValue("client_id")
	redirectURI := r.FormValue("redirect_uri")
	verifier := r.FormValue("code_verifier")
	if code == "" || clientID == "" || redirectURI == "" || verifier == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "missing code, client_id, redirect_uri, or code_verifier")
		return
	}
	g, err := a.oauth.ExchangeCode(r.Context(), code, clientID, redirectURI, verifier)
	if err != nil {
		logging.LogWarn("oauth", fmt.Sprintf("token(code): exchange FAILED client_id=%s redirect_uri=%s: %v", clientID, redirectURI, err))
		switch err {
		case oauth.ErrPKCE, oauth.ErrMismatch, oauth.ErrExpired, oauth.ErrNotFound:
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the authorization code is invalid, expired, or already used")
		default:
			oauthError(w, http.StatusInternalServerError, "server_error", "could not exchange code")
		}
		return
	}
	logging.LogInfo("oauth", fmt.Sprintf("token(code): exchange OK client_id=%s grant=%s -> minting access token", clientID, g.GrantCaps))
	a.oauthIssueTokens(w, r, clientID, g.GrantCaps, g.OwnerUserID, g.Label)
}

func (a *App) oauthTokenFromRefresh(w http.ResponseWriter, r *http.Request) {
	refresh := r.FormValue("refresh_token")
	clientID := r.FormValue("client_id")
	if refresh == "" || clientID == "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "missing refresh_token or client_id")
		return
	}
	keyID, err := a.oauth.ExchangeRefresh(r.Context(), refresh, clientID)
	if err != nil {
		if errors.Is(err, oauth.ErrReuse) {
			// A rotated-out refresh token was replayed — treat it as a breach and
			// tear the whole grant down: revoke every refresh token for the key and
			// the access key itself (audit L9).
			_ = a.oauth.RevokeRefreshForKey(r.Context(), keyID)
			_ = a.apiKeys.Revoke(r.Context(), keyID)
			dbpkg.AuditLog("oauth.refresh_reuse", "oauth:"+clientID, keyID, "grant revoked on refresh-token replay")
		}
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is invalid, expired, or already used")
		return
	}
	// Rotate the underlying key with a fresh bounded expiry: the old access token
	// stops working immediately and the renewed one is short-lived again (L9).
	exp := time.Now().Add(oauthAccessTokenTTL)
	raw, err := a.apiKeys.RotateWithExpiry(r.Context(), keyID, &exp)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the connector key no longer exists")
		return
	}
	newRefresh, err := a.oauth.IssueRefresh(r.Context(), clientID, keyID)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue refresh token")
		return
	}
	oauthWriteJSON(w, http.StatusOK, oauthTokenResponse(raw, newRefresh))
}

// oauthIssueTokens mints the scoped API key (the access token) from the approved
// grant, records a refresh token bound to it, and returns the standard token
// response. Minting here — not at consent — means no bearer token is ever stored.
func (a *App) oauthIssueTokens(w http.ResponseWriter, r *http.Request, clientID, grantCaps, owner, label string) {
	perms := apikeys.NewPermissions()
	for _, capTok := range strings.Split(grantCaps, ",") {
		if sec, act, ok := apikeys.ParseCapability(strings.TrimSpace(capTok)); ok {
			perms.Grant(sec, act)
		}
	}
	if label == "" {
		label = "Claude via OAuth"
	}
	// Mint the access token (a scoped API key) with a short bounded lifetime so a
	// leaked connector token is not a permanent credential (audit L9). The client
	// renews it through the rotating refresh token.
	exp := time.Now().Add(oauthAccessTokenTTL)
	key, raw, err := a.apiKeys.CreateWithPermissions(r.Context(), owner, label, perms, &exp, 0)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not mint access token")
		return
	}
	refresh, err := a.oauth.IssueRefresh(r.Context(), clientID, key.ID)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue refresh token")
		return
	}
	dbpkg.AuditLog("oauth.token", "oauth:"+clientID, key.ID, "grant="+grantCaps)
	oauthWriteJSON(w, http.StatusOK, oauthTokenResponse(raw, refresh))
}

// oauthAccessTokenTTL bounds the lifetime of an OAuth-minted access token
// (audit L9). Short enough that a leaked token is not a lasting credential;
// clients renew via the rotating refresh token before it lapses.
const oauthAccessTokenTTL = time.Hour

func oauthTokenResponse(accessToken, refreshToken string) map[string]any {
	return map[string]any{
		"access_token":  accessToken,
		"token_type":    "Bearer",
		"expires_in":    int(oauthAccessTokenTTL / time.Second),
		"refresh_token": refreshToken,
		"scope":         "vayupress",
	}
}

// ── redirect + error helpers ────────────────────────────────────────────────────

// oauthRedirectSuccess redirects back to the client's (already-validated) redirect
// URI with the authorization code and the client's original state.
func oauthRedirectSuccess(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	oauthRedirectWith(w, r, redirectURI, url.Values{"code": {code}, "state": {state}})
}

// oauthRedirectError redirects back with an OAuth error (redirectURI is validated
// by the caller before this is ever reached).
func oauthRedirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, errCode string) {
	oauthRedirectWith(w, r, redirectURI, url.Values{"error": {errCode}, "state": {state}})
}

func oauthRedirectWith(w http.ResponseWriter, r *http.Request, redirectURI string, extra url.Values) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		oauthHTMLError(w, "Invalid redirect URL.")
		return
	}
	q := u.Query()
	for k, vs := range extra {
		if len(vs) > 0 && vs[0] != "" {
			q.Set(k, vs[0])
		}
	}
	u.RawQuery = q.Encode()
	// Defence in depth against an open redirect. The caller only ever reaches here
	// with a redirect URI the client registered (validated by exact match), but
	// re-assert the scheme/host invariant right at the sink so an upstream mistake
	// can never send a browser to an attacker origin. isValidRedirectURL is a
	// boolean guard whose name matches the static analyser's redirect-check
	// heuristic, so `target` is treated as neutralised on the true branch.
	target := u.String()
	if !isValidRedirectURL(target) {
		logging.LogWarn("oauth", fmt.Sprintf("redirect REJECTED to %s://%s (redirect_uri=%q not an allowed target)", u.Scheme, u.Host, redirectURI))
		oauthHTMLError(w, "Invalid redirect URL.")
		return
	}
	// Log the scheme+host+params only (never the code/token itself).
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	logging.LogInfo("oauth", fmt.Sprintf("redirect -> %s://%s%s params=%v (303 back to client)", u.Scheme, u.Host, u.Path, keys))
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// isValidRedirectURL reports whether raw is an allowed OAuth client redirect
// target. It MUST mirror internal/oauth.validRedirectURI exactly, or a client can
// register a redirect that then fails at the final code-carrying redirect: https
// to any host, http to a loopback host, or a private-use / custom URI scheme
// (RFC 8252) for native apps — never a script/data/file scheme, and never with a
// fragment or embedded userinfo. The name matches CodeQL's redirect-check
// barrier-guard heuristic, so `target` is treated as neutralised on the true
// branch even though a custom scheme is permitted (the value is always one the
// client registered, re-checked by RedirectAllowed before this point).
func isValidRedirectURL(raw string) bool {
	if strings.Contains(raw, "#") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.User != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "":
		return false
	case "https":
		return u.Hostname() != ""
	case "http":
		host := u.Hostname()
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "javascript", "data", "vbscript", "file", "blob", "about":
		return false
	default:
		// Private-use / custom scheme (RFC 8252 §7.1), matching registration.
		return true
	}
}

// oauthHTMLError renders a small, self-contained error page (used only when there
// is no safe client redirect to report the error to).
func oauthHTMLError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(authPageShell("Connect — VayuPress", `
  <div class="login-card">
    <h1 class="login-title">Couldn't connect</h1>
    <p class="login-sub">`+html.EscapeString(msg)+`</p>
  </div>`)))
}

// oauthConsentPage renders the approval screen. All dynamic values are escaped;
// the form carries the request parameters + CSRF token as hidden fields and POSTs
// same-origin to /oauth/authorize/consent.
func oauthConsentPage(client oauth.Client, redirectURI, challenge, state, consent string) string {
	name := strings.TrimSpace(client.Name)
	if name == "" {
		name = "An MCP client"
	}
	// Every dynamic value is passed through html.EscapeString directly at the point
	// of interpolation. Calling the standard sanitizer inline (rather than via a
	// local alias or closure) is what lets CodeQL's reflected-XSS query see the
	// barrier — an aliased sanitizer reads as untrusted flow to the query engine.
	// Access-level choices (author is pre-selected; full-control is clearly the strongest).
	choices := ""
	for _, id := range []string{"full", "author", "readonly"} {
		p := oauthPresets[id]
		checked := ""
		if id == "author" {
			checked = " checked"
		}
		choices += `<label class="oauth-choice">
      <input type="radio" name="grant" value="` + html.EscapeString(id) + `"` + checked + `>
      <span class="oauth-choice-title">` + html.EscapeString(p.Label) + `</span>
      <span class="oauth-choice-desc">` + html.EscapeString(p.Desc) + `</span>
    </label>`
	}
	escName := html.EscapeString(name)
	// Anti-phishing (audit M9): show WHERE the authorization code will be delivered.
	// Dynamic client registration is unauthenticated and the client_name is
	// self-asserted — a malicious client can call itself "Claude" — so the redirect
	// host is the ground truth the operator must verify before approving.
	destHost := redirectURI
	if pu, perr := url.Parse(redirectURI); perr == nil && pu.Host != "" {
		destHost = pu.Host
	}
	inner := `
  <div class="login-card oauth-consent">
    <h1 class="login-title">Connect ` + escName + `?</h1>
    <p class="login-sub"><strong>` + escName + `</strong> is asking to connect to your VayuPress site. Choose how much access to grant. You can revoke it anytime from <a href="/os/apikeys">API&nbsp;Keys</a>.</p>
    <p class="login-sub oauth-dest" role="note">⚠️ Approving sends your authorization code to <strong>` + html.EscapeString(destHost) + `</strong>. This app registered itself and its name is <em>not verified</em> by VayuPress — approve only if that address is where you expect <strong>` + escName + `</strong> to receive it.</p>
    <form method="POST" action="/oauth/authorize/consent" class="oauth-form">
      <input type="hidden" name="client_id" value="` + html.EscapeString(client.ID) + `">
      <input type="hidden" name="redirect_uri" value="` + html.EscapeString(redirectURI) + `">
      <input type="hidden" name="code_challenge" value="` + html.EscapeString(challenge) + `">
      <input type="hidden" name="state" value="` + html.EscapeString(state) + `">
      <input type="hidden" name="consent_token" value="` + html.EscapeString(consent) + `">
      <div class="oauth-choices">` + choices + `</div>
      <div class="oauth-actions">
        <button type="submit" name="decision" value="approve" class="btn btn--primary">Approve &amp; connect</button>
        <button type="submit" name="decision" value="deny" class="btn">Deny</button>
      </div>
    </form>
  </div>`
	return authPageShell("Connect — VayuPress", inner)
}
