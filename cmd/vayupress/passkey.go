// SPDX-License-Identifier: Apache-2.0

package main

// passkey.go — passkeys (internal/passkey) at the app's edges: the relying
// party, adding a passkey to the signed-in account or mailbox, signing in
// with one at /os/login, and the block that lists and removes them.

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/johalputt/vayupress/internal/auth"
	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/passkey"
	"github.com/johalputt/vayupress/internal/signin"
)

// relyingParty is the install as WebAuthn knows it: its domain, and the one
// origin a passkey is used from. An install on localhost (a developer's, or
// the tests') also takes the port the browser came from, which only a
// browser on the same machine can name.
func relyingParty(r *http.Request) (*webauthn.WebAuthn, error) {
	host := strings.ToLower(strings.TrimSpace(config.Cfg.Domain))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if host == "" {
		return nil, errors.New("passkeys need the install's domain set")
	}
	origins := []string{"https://" + host}
	if host == "localhost" {
		if o, err := url.Parse(r.Header.Get("Origin")); err == nil && o.Hostname() == "localhost" && (o.Scheme == "http" || o.Scheme == "https") {
			origins = append(origins, o.Scheme+"://"+o.Host)
		}
	}
	return webauthn.New(&webauthn.Config{RPID: host, RPDisplayName: "VayuOS", RPOrigins: origins})
}

// passkeyPrincipal is who the signed-in session is, as a passkey belongs to
// it: its mailbox, for a mailbox's session, or its install account. An
// API-key session has no one to give a passkey to.
func passkeyPrincipal(r *http.Request) (principal, account string, ok bool) {
	u := currentUser(r)
	if u == nil || u.ID == "" {
		return "", "", false
	}
	if addr, isMail := strings.CutPrefix(u.ID, "vmail:"); isMail {
		return "vmail:" + strings.ToLower(addr), strings.ToLower(addr), true
	}
	return "user:" + u.ID, strings.ToLower(u.Email), true
}

// handlePasskeyAddBegin starts adding a passkey to the session's principal.
func (a *App) handlePasskeyAddBegin(w http.ResponseWriter, r *http.Request) {
	principal, account, ok := passkeyPrincipal(r)
	if !ok || a.passkeys == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-account", "Passkeys are for a signed-in account or mailbox.", "")
		return
	}
	rp, err := relyingParty(r)
	if err != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "no-domain", err.Error(), "")
		return
	}
	u, err := a.passkeys.UserFor(r.Context(), principal, account)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "store", err.Error(), "")
		return
	}
	// A passkey that lives on the device and checks the person (a PIN, a
	// fingerprint), so it signs in alone; the ones already held are
	// excluded so a device is not added twice.
	creation, data, err := rp.BeginRegistration(u,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired}),
		webauthn.WithExclusions(webauthn.Credentials(u.WebAuthnCredentials()).CredentialDescriptors()))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "begin", err.Error(), "")
		return
	}
	token, err := a.passkeys.Hold(principal, data)
	if err != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "busy", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ceremony": token, "options": creation})
}

// handlePasskeyAddFinish keeps the passkey the browser made, when it answers
// the ceremony this session started.
func (a *App) handlePasskeyAddFinish(w http.ResponseWriter, r *http.Request) {
	principal, account, ok := passkeyPrincipal(r)
	if !ok || a.passkeys == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-account", "Passkeys are for a signed-in account or mailbox.", "")
		return
	}
	data, ok := a.passkeys.Take(r.URL.Query().Get("ceremony"), principal)
	if !ok {
		writeAPIError(w, r, http.StatusBadRequest, "expired", "That took too long, or was started elsewhere. Try again.", "")
		return
	}
	rp, err := relyingParty(r)
	if err != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "no-domain", err.Error(), "")
		return
	}
	u, err := a.passkeys.UserFor(r.Context(), principal, account)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "store", err.Error(), "")
		return
	}
	u.Adopt(data.UserID)
	cred, err := rp.FinishRegistration(u, data, r)
	if err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "refused", "The passkey could not be checked, so it was not added.", "")
		return
	}
	if err := a.passkeys.Add(r.Context(), u, cred, r.URL.Query().Get("name")); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "store", err.Error(), "")
		return
	}
	dbpkg.AuditLog("passkey.add", dbpkg.AuditActor(r), account, principal)
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePasskeyDelete removes one of the session's own passkeys.
func (a *App) handlePasskeyDelete(w http.ResponseWriter, r *http.Request) {
	principal, account, ok := passkeyPrincipal(r)
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if !ok || a.passkeys == nil || err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "Name the passkey to remove.", "")
		return
	}
	if err := a.passkeys.Delete(r.Context(), principal, id); err != nil {
		writeAPIError(w, r, http.StatusNotFound, "not-found", "That passkey is not yours, or is gone.", "")
		return
	}
	dbpkg.AuditLog("passkey.remove", dbpkg.AuditActor(r), account, strconv.FormatInt(id, 10))
	w.Header().Set("HX-Refresh", "true")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// handlePasskeySignInBegin starts a sign-in that names no one: the browser
// offers the passkeys it holds for this install.
func (a *App) handlePasskeySignInBegin(w http.ResponseWriter, r *http.Request) {
	setAuthPageNoCache(w)
	rp, err := relyingParty(r)
	if err != nil || a.passkeys == nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "Passkeys are not available on this install.", "")
		return
	}
	assertion, data, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "begin", err.Error(), "")
		return
	}
	token, err := a.passkeys.Hold("", data)
	if err != nil {
		writeAPIError(w, r, http.StatusServiceUnavailable, "busy", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ceremony": token, "options": assertion})
}

// handlePasskeySignInFinish signs in as the principal whose passkey answered.
// The source's blocks and the account's fence still refuse it; the account's
// pause does not, since a passkey cannot be guessed, and it is how a holder
// gets in while their address is under attack.
func (a *App) handlePasskeySignInFinish(w http.ResponseWriter, r *http.Request) {
	setAuthPageNoCache(w)
	ip := loginClientIP(r)
	refuse := func(msg string) {
		writeAPIError(w, r, http.StatusUnauthorized, "refused", msg, "")
	}
	if a.passkeys == nil || a.sessions == nil {
		refuse("Passkeys are not available on this install.")
		return
	}
	data, ok := a.passkeys.Take(r.URL.Query().Get("ceremony"), "")
	if !ok {
		refuse("That took too long. Try again.")
		return
	}
	rp, err := relyingParty(r)
	if err != nil {
		refuse(err.Error())
		return
	}
	var who *passkey.User
	_, cred, err := rp.FinishPasskeyLogin(func(_, handle []byte) (webauthn.User, error) {
		u, err := a.passkeys.ByHandle(r.Context(), handle)
		who = u
		return u, err
	}, data, r)
	if err != nil || who == nil {
		refuse("That passkey is not one this install knows.")
		return
	}
	if a.signinRefused(who.Account, ip) {
		writeAPIError(w, r, http.StatusForbidden, "signin-refused", signinRefusedMessage, "")
		return
	}
	// A sign count that went backwards is a copied authenticator.
	if cred.Authenticator.CloneWarning {
		dbpkg.AuditLog("passkey.clone_warning", "system", who.Account, who.Principal)
		refuse("This passkey looks copied, so it was refused. Sign in another way and remove it.")
		return
	}
	var session string
	if addr, isMail := strings.CutPrefix(who.Principal, "vmail:"); isMail {
		if a.vayuMail == nil || a.vayuMail.Accounts() == nil || a.vayuMail.Accounts().HashFor(r.Context(), addr) == "" {
			refuse("That mailbox is not open for sign-in.")
			return
		}
		session = "vmail:" + addr
	} else {
		id := strings.TrimPrefix(who.Principal, "user:")
		if a.userStore == nil {
			refuse("That account is gone.")
			return
		}
		if u, err := a.userStore.GetByID(r.Context(), id); err != nil || u == nil {
			refuse("That account is gone.")
			return
		}
		session = id
	}
	_ = a.passkeys.Used(r.Context(), cred)
	token, err := a.sessions.Create(r.Context(), session)
	if err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "session", "could not start session", "")
		return
	}
	auth.RecordAuthSuccess(ip)
	auth.RecordAccountSuccess(who.Account)
	a.signinRecord(who.Account, signin.DoorConsole, signin.StageOK, ip)
	if strings.HasPrefix(session, "vmail:") {
		a.enrolOnSignIn(r, who.Account)
	} else {
		a.userStore.TouchLastLogin(r.Context(), session)
	}
	dbpkg.AuditLog("passkey.signin", "public:"+ip, who.Account, who.Principal)
	auth.SetSessionCookieRemember(w, token, true)
	next := r.URL.Query().Get("next")
	if !isLocalURL(next) {
		next = osHome
	}
	writeJSON(w, r, http.StatusOK, map[string]string{"next": next})
}

// handlePasskeyOnly turns passkey only on or off for the session's own
// principal.
func (a *App) handlePasskeyOnly(w http.ResponseWriter, r *http.Request) {
	principal, account, ok := passkeyPrincipal(r)
	if !ok || a.passkeys == nil {
		writeAPIError(w, r, http.StatusForbidden, "no-account", "Passkeys are for a signed-in account or mailbox.", "")
		return
	}
	on := r.URL.Query().Get("on") == "1"
	if err := a.passkeys.SetOnly(r.Context(), principal, on); err != nil {
		writeAPIError(w, r, http.StatusConflict, "needs-spare", err.Error(), "")
		return
	}
	dbpkg.AuditLog("passkey.only", dbpkg.AuditActor(r), account, map[bool]string{true: "on", false: "off"}[on])
	w.Header().Set("HX-Refresh", "true")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}

// passkeyOnly reports whether principal signs in by passkey only, so that
// its own password is refused at every door. The door checks the password
// first and refuses after, so the answer and its time are a wrong
// password's; app passwords are not its own password and keep working.
func (a *App) passkeyOnly(ctx context.Context, principal string) bool {
	return a.passkeys != nil && a.passkeys.Only(ctx, principal)
}

// passkeysBlock lists the session's own passkeys with Remove, and the control
// that adds one on this device.
func (a *App) passkeysBlock(r *http.Request) string {
	principal, _, ok := passkeyPrincipal(r)
	if !ok || a.passkeys == nil {
		return ""
	}
	list, _ := a.passkeys.List(r.Context(), principal)
	var b strings.Builder
	b.WriteString(`<div class="vm-acct__sub" data-passkeys><span class="field-label">Passkeys</span>`)
	b.WriteString(`<span class="text-sm">A passkey signs you in with this device's lock (a fingerprint, a face or a PIN) instead of a password and a code. It cannot be guessed or phished, and it works while sign-ins to your address are paused.</span>`)
	if len(list) > 0 {
		for _, e := range list {
			used := "never used"
			if !e.LastUsed.IsZero() {
				used = "last used " + config.FormatSite(e.LastUsed, "2 Jan 2006")
			}
			b.WriteString(`<div class="vm-row"><span>` + html.EscapeString(e.Name) + `</span><span class="text-xs muted">added ` +
				html.EscapeString(config.FormatSite(e.Created, "2 Jan 2006")) + `, ` + used + `</span>` +
				`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/passkeys/remove?id=` + strconv.FormatInt(e.ID, 10) +
				`" hx-swap="none" hx-confirm="Remove this passkey? It will no longer sign you in.">Remove</button></div>`)
		}
	}
	switch {
	case a.passkeys.Only(r.Context(), principal):
		b.WriteString(`<div class="vm-row"><span class="text-sm"><strong>Your password is off.</strong> You sign in with a passkey only, so a stolen or guessed password opens nothing.</span>` +
			`<button type="button" class="btn btn--ghost btn--sm" hx-post="/os/api/passkeys/only?on=0" hx-swap="none">Turn the password back on</button></div>`)
	case len(list) >= passkey.OnlyNeeds:
		b.WriteString(`<div class="vm-row"><span class="text-sm">With a spare passkey you can turn your password off, so only a passkey signs you in. Mail apps keep their app passwords.</span>` +
			`<button type="button" class="btn btn--sm" hx-post="/os/api/passkeys/only?on=1" hx-swap="none" hx-confirm="Turn your password off? Only your passkeys will sign you in. Removing passkeys below two turns it back on.">Sign in by passkey only</button></div>`)
	case len(list) == 1:
		b.WriteString(`<p class="text-xs muted">Add a second passkey, on another device, and you can turn your password off.</p>`)
	}
	b.WriteString(`<div class="vm-row"><input class="input input--sm" data-passkey-name placeholder="This device" aria-label="Name for the passkey" maxlength="64">` +
		`<button type="button" class="btn btn--sm" data-passkey-add>Add a passkey</button><span class="text-xs muted" data-passkey-say role="status"></span></div></div>`)
	return b.String()
}
