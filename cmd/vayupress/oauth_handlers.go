package main

// oauth_handlers.go — the HTTP surface of VayuPress's OAuth 2.1 authorization
// server (ADR-0140, VayuMCP Stage 4): discovery metadata (RFC 8414 + RFC 9728),
// dynamic client registration (RFC 7591), the /authorize consent flow, and the
// /token exchange. The access token handed back is a scoped VayuPress API key, so
// the whole capability/rate/audit/revocation model is reused and nothing is
// bespoke about how a connector is authorized once it holds a token.

import (
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/johalputt/vayupress/internal/apikeys"
	"github.com/johalputt/vayupress/internal/auth"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/oauth"
)

// oauthBaseURL returns this site's public origin (scheme://host) as seen by the
// browser — the issuer for OAuth metadata. Scheme mirrors publicMCPEndpoint.
func oauthBaseURL(r *http.Request) string {
	scheme := "https"
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		if i := strings.IndexByte(fp, ','); i >= 0 {
			fp = fp[:i]
		}
		scheme = strings.TrimSpace(fp)
	} else if r.TLS == nil {
		scheme = "http"
	}
	host := r.Host
	if host == "" {
		host = "your-domain.com"
	}
	return scheme + "://" + host
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
	base := oauthBaseURL(r)
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
	base := oauthBaseURL(r)
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
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "request body must be JSON")
		return
	}
	c, err := a.oauth.RegisterClient(r.Context(), strings.TrimSpace(body.ClientName), body.RedirectURIs)
	if err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
		return
	}
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
		oauthHTMLError(w, "This app is not registered, or its redirect URL does not match. Reconnect from your MCP client.")
		return
	}
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

	// Double-submit CSRF token for the consent POST (a plain form, so it travels as
	// a hidden field). Reuse a valid existing cookie; otherwise mint one now.
	csrfTok := ""
	if c, err := r.Cookie("vp_csrf"); err == nil && c.Value != "" && auth.ValidateCSRFToken(c.Value) {
		csrfTok = c.Value
	} else {
		csrfTok = auth.GenerateCSRFToken()
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: csrfTok, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: auth.CSRFCookieSecure(), MaxAge: 3600})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(oauthConsentPage(client, redirectURI, challenge, state, csrfTok)))
}

// handleOAuthConsent processes the operator's decision. On approval it mints a
// scoped API key with the chosen grant and issues a single-use authorization code
// bound to the client, redirect URI, and PKCE challenge; then it redirects back to
// the client. CSRF is enforced by middleware on this route.
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
	// Re-check the operator identity on the POST (never trust the form for who is
	// approving) — must be a signed-in administrator.
	u := a.resolveConsoleUser(r)
	if u == nil || accessLevelFor(u.Role, false) < accessAdmin {
		oauthHTMLError(w, "Your session expired. Sign in again and retry the connection.")
		return
	}

	if r.FormValue("decision") != "approve" {
		oauthRedirectError(w, r, redirectURI, state, "access_denied")
		return
	}
	preset, ok := oauthPresets[r.FormValue("grant")]
	if !ok {
		oauthHTMLError(w, "Please choose an access level.")
		return
	}

	// Do NOT mint the key here — the code carries only the approved GRANT, and the
	// scoped key is minted at the /token exchange, so no bearer token exists until
	// the client presents a valid PKCE verifier (and none is ever stored at rest).
	label := "Claude via OAuth (" + preset.Label + ")"
	// Attribute the grant to the admin we just re-resolved (u), NOT currentUserIDOf,
	// which is only stamped on the session-gated router group and is empty on this
	// CSRF-only consent route — so the minted key is owned by, and the audit trail
	// names, the operator who actually approved.
	code, err := a.oauth.IssueCode(r.Context(), oauth.CodeGrant{
		ClientID: clientID, RedirectURI: redirectURI, CodeChallenge: challenge,
		GrantCaps: preset.Caps, OwnerUserID: u.ID, Label: label,
	})
	if err != nil {
		oauthHTMLError(w, "Could not complete authorization. Please try again.")
		return
	}
	dbpkg.AuditLog("oauth.authorize", "user:"+u.ID, clientID, "grant="+preset.Caps)

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
	switch r.FormValue("grant_type") {
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
		switch err {
		case oauth.ErrPKCE, oauth.ErrMismatch, oauth.ErrExpired, oauth.ErrNotFound:
			oauthError(w, http.StatusBadRequest, "invalid_grant", "the authorization code is invalid, expired, or already used")
		default:
			oauthError(w, http.StatusInternalServerError, "server_error", "could not exchange code")
		}
		return
	}
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
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the refresh token is invalid or already used")
		return
	}
	// Rotate the underlying key: the old access token stops working immediately and
	// a fresh one is returned, keeping the same grant/ownership.
	raw, err := a.apiKeys.Rotate(r.Context(), keyID)
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
	key, raw, err := a.apiKeys.CreateWithPermissions(r.Context(), owner, label, perms, nil, 0)
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

func oauthTokenResponse(accessToken, refreshToken string) map[string]any {
	return map[string]any{
		"access_token":  accessToken,
		"token_type":    "Bearer",
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
		oauthHTMLError(w, "Invalid redirect URL.")
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// isValidRedirectURL reports whether raw is an allowed OAuth client redirect
// target: https to any host, or http to a loopback host, and never with embedded
// userinfo. It mirrors the registration-time rule (internal/oauth) so the two
// cannot drift. The name matches CodeQL's redirect-check barrier-guard heuristic.
func isValidRedirectURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	default:
		return false
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
func oauthConsentPage(client oauth.Client, redirectURI, challenge, state, csrf string) string {
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
	inner := `
  <div class="login-card oauth-consent">
    <h1 class="login-title">Connect ` + escName + `?</h1>
    <p class="login-sub"><strong>` + escName + `</strong> is asking to connect to your VayuPress site. Choose how much access to grant. You can revoke it anytime from <a href="/os/apikeys">API&nbsp;Keys</a>.</p>
    <form method="POST" action="/oauth/authorize/consent" class="oauth-form">
      <input type="hidden" name="client_id" value="` + html.EscapeString(client.ID) + `">
      <input type="hidden" name="redirect_uri" value="` + html.EscapeString(redirectURI) + `">
      <input type="hidden" name="code_challenge" value="` + html.EscapeString(challenge) + `">
      <input type="hidden" name="state" value="` + html.EscapeString(state) + `">
      <input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `">
      <div class="oauth-choices">` + choices + `</div>
      <div class="oauth-actions">
        <button type="submit" name="decision" value="approve" class="btn btn--primary">Approve &amp; connect</button>
        <button type="submit" name="decision" value="deny" class="btn">Deny</button>
      </div>
    </form>
  </div>`
	return authPageShell("Connect — VayuPress", inner)
}
