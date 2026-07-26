// SPDX-License-Identifier: Apache-2.0

package main

// handlers_mail_recovery.go — the public, unauthenticated recovery surface
// (ADR-0144).
//
// This is the highest-value attack target in the product: a mailbox is the reset
// channel for its owner's bank, registrar and VPS, and this endpoint exists to
// bypass its password. Three properties are load-bearing.
//
//  1. It NEVER reveals whether an address exists. Mail addresses are public, so
//     knowing one must prove nothing — and "unknown mailbox" would turn this into
//     a free directory of every account on the server. Known and unknown
//     addresses get the same words, the same status and the same shape of work.
//  2. It NEVER mints a session. A reset link that signs you in is a
//     session-stealing link; the holder finishes by signing in normally, which
//     also proves the new password actually reached them.
//  3. Everything funnels through applyMailPasswordReset, so a recovery cannot
//     leave an attacker's app password or webmail session alive.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/seo"
)

// Recovery is deliberately expensive to attack and cheap to use once. The
// per-address budget stops one mailbox being flooded with reset mail (a
// harassment vector as much as a security one); the per-IP budget stops a sweep
// across many addresses.
var (
	recoveryByAddress = newIngestLimiter(3, time.Hour)
	recoveryByIP      = newIngestLimiter(10, time.Hour)
)

// recoverySameAnswer is the only thing an unauthenticated caller is ever told.
// It is phrased so it reads as helpful to the holder and tells an attacker
// nothing: it does not say a mail was sent, only what happens if the account
// exists and has a recovery address.
const recoverySameAnswer = "If that mailbox exists and has a recovery address on file, " +
	"a reset link is on its way to it. The link expires in 30 minutes."

// handleMailRecoverRequest starts a recovery. GET renders the form; POST issues
// the link.
func (a *App) handleMailRecoverRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.renderRecoveryPage(w, r, recoveryFormPage(""))
		return
	}
	addr := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	ip := clientIPForContact(r)

	// Rate limits are applied BEFORE any lookup, and a refusal returns the same
	// answer as a success: a distinguishable "too many requests" would confirm
	// that earlier attempts hit a real account.
	overBudget := !recoveryByIP.allow(ip) || !recoveryByAddress.allow(addr)

	// Audit the REQUEST, not just the completion. A burst against one mailbox is
	// the attack signal, and recording only successful resets means the attempt is
	// never seen at all.
	dbpkg.AuditLog("vayumail.recovery.requested", "public:"+ip, addr,
		map[bool]string{true: "rate-limited", false: "accepted"}[overBudget])

	if !overBudget {
		// Work happens out of band so response time does not vary with whether the
		// address exists, how many codes it has, or how slow the SMTP peer is.
		go a.issueRecoveryLink(addr, ip)
	}
	a.renderRecoveryPage(w, r, recoverySentPage())
}

// issueRecoveryLink does the real work for an accepted request. Every early
// return is silent by design — the caller already answered.
func (a *App) issueRecoveryLink(addr, ip string) {
	defer func() {
		if rec := recover(); rec != nil {
			logging.LogError("vayumail", "recovery link goroutine panicked", addr)
		}
	}()
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	accts := a.vayuMail.Accounts()
	contact := accts.RecoveryContact(ctx, addr)
	if contact == "" {
		// No account, or no verified recovery address. Identical handling: nothing
		// is sent and nothing distinguishes the two cases from outside.
		return
	}
	// In a Tor Space there is no clearnet egress, so a link to an external address
	// cannot be delivered. Fail loudly in the log rather than silently pretending
	// — the operator needs to know recovery codes are the only working factor.
	if safefetch.ClearnetBlocked() {
		logging.LogWarn("vayumail",
			"recovery link not sent for "+addr+
				": clearnet egress is blocked in Tor mode — recovery codes are the only factor here")
		return
	}
	token, err := accts.CreateRecoveryToken(ctx, addr)
	if err != nil {
		logging.LogError("vayumail", "could not create recovery token", err.Error())
		return
	}
	link := seo.Origin(config.Cfg.Domain) + "/mail/recover/reset?token=" + token
	body := "A password reset was requested for the mailbox " + addr + ".\n\n" +
		"Open this link within 30 minutes to choose a new password:\n\n" +
		link + "\n\n" +
		"Requested from " + ip + ".\n\n" +
		"If you did not ask for this, ignore this message — the link expires on its own, " +
		"and nothing changes until it is used. Your current password still works.\n"
	if a.mailer == nil {
		logging.LogError("vayumail", "recovery link not sent: no outbound mailer configured", addr)
		return
	}
	if err := a.mailer.Send(email.Message{
		To:      contact,
		Subject: "Reset your " + addr + " password",
		Text:    body,
	}); err != nil {
		logging.LogError("vayumail", "recovery link delivery failed", err.Error())
	}
}

// handleMailRecoverReset completes a link-based recovery. GET shows the
// new-password form; POST performs it.
//
// The token is consumed only on POST. Consuming it on GET would let a link
// preloader, an antivirus scanner or a mail client's link-checker burn the
// holder's one chance before they ever saw the page.
func (a *App) handleMailRecoverReset(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		a.renderRecoveryPage(w, r, recoveryNoticePage("Recovery unavailable",
			"VayuMail is not running on this server."))
		return
	}
	if r.Method == http.MethodGet {
		a.renderRecoveryPage(w, r, recoveryPasswordFormPage(r.URL.Query().Get("token"), ""))
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	pass := r.FormValue("password")
	confirm := r.FormValue("confirm")
	if pass != confirm {
		a.renderRecoveryPage(w, r, recoveryPasswordFormPage(token, "Those passwords do not match."))
		return
	}
	if len([]rune(pass)) < minMailPasswordLen {
		a.renderRecoveryPage(w, r, recoveryPasswordFormPage(token,
			"Choose a password of at least 8 characters."))
		return
	}
	// Consume first: an invalid or replayed token must cost the caller their
	// attempt whether or not the password was acceptable.
	addr, err := a.vayuMail.Accounts().ConsumeRecoveryToken(r.Context(), token)
	if err != nil {
		dbpkg.AuditLog("vayumail.recovery.token_rejected", "public:"+clientIPForContact(r), "", err.Error())
		a.renderRecoveryPage(w, r, recoveryNoticePage("That link is no longer valid",
			"Reset links can be used once and expire after 30 minutes. Request a new one."))
		return
	}
	deps, ok := a.mailResetDepsFor()
	if !ok {
		a.renderRecoveryPage(w, r, recoveryNoticePage("Recovery unavailable",
			"VayuMail is not running on this server."))
		return
	}
	out, err := applyMailPasswordReset(r.Context(), deps, addr, pass,
		mailResetByLink, "public:"+clientIPForContact(r))
	if err != nil {
		a.renderRecoveryPage(w, r, recoveryNoticePage("That did not work", err.Error()))
		return
	}
	a.renderRecoveryPage(w, r, recoveryDonePage(addr, out))
}

// handleMailRecoverCode completes a code-based recovery. This is the factor that
// works with no network, no third party and no outbound queue — in a Tor Space it
// is the only one.
func (a *App) handleMailRecoverCode(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		a.renderRecoveryPage(w, r, recoveryNoticePage("Recovery unavailable",
			"VayuMail is not running on this server."))
		return
	}
	if r.Method == http.MethodGet {
		a.renderRecoveryPage(w, r, recoveryCodeFormPage("", ""))
		return
	}

	addr := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	code := r.FormValue("code")
	pass := r.FormValue("password")
	confirm := r.FormValue("confirm")
	ip := clientIPForContact(r)

	if !recoveryByIP.allow(ip) || !recoveryByAddress.allow(addr) {
		// A generic failure, not "slow down": the latter tells an attacker their
		// earlier guesses were reaching a real account.
		a.renderRecoveryPage(w, r, recoveryCodeFormPage(addr, recoveryCodeFailure))
		return
	}
	if pass != confirm {
		a.renderRecoveryPage(w, r, recoveryCodeFormPage(addr, "Those passwords do not match."))
		return
	}
	if len([]rune(pass)) < minMailPasswordLen {
		a.renderRecoveryPage(w, r, recoveryCodeFormPage(addr,
			"Choose a password of at least 8 characters."))
		return
	}

	dbpkg.AuditLog("vayumail.recovery.code_attempt", "public:"+ip, addr, "")
	if !a.vayuMail.Accounts().ConsumeRecoveryCode(r.Context(), addr, code) {
		// One message for "no such mailbox" and for "wrong code". ConsumeRecoveryCode
		// runs Argon2id over the stored codes for a real account and returns fast for
		// an unknown one, so the two are distinguishable by timing; that is acceptable
		// here because a code attempt already requires knowing the address, and the
		// per-address budget is 3/hour.
		dbpkg.AuditLog("vayumail.recovery.code_rejected", "public:"+ip, addr, "")
		a.renderRecoveryPage(w, r, recoveryCodeFormPage(addr, recoveryCodeFailure))
		return
	}
	deps, ok := a.mailResetDepsFor()
	if !ok {
		a.renderRecoveryPage(w, r, recoveryNoticePage("Recovery unavailable",
			"VayuMail is not running on this server."))
		return
	}
	out, err := applyMailPasswordReset(r.Context(), deps, addr, pass, mailResetByCode, "public:"+ip)
	if err != nil {
		a.renderRecoveryPage(w, r, recoveryNoticePage("That did not work", err.Error()))
		return
	}
	a.renderRecoveryPage(w, r, recoveryDonePage(addr, out))
}

// recoveryCodeFailure is the single message for every code failure — unknown
// mailbox, wrong code, spent code, or over budget.
const recoveryCodeFailure = "That mailbox and code combination was not accepted. " +
	"Check the code and try again, or use a recovery link instead."

// handleMailRecoverAsk files a request for administrator help — the last path,
// for a holder who enrolled nothing and has no channel left.
//
// Same enumeration contract as everything else here: an unknown address is
// recorded and answered identically, so the response cannot be used to discover
// which mailboxes exist. The administrator sees the address in the queue and
// declines if it is nonsense.
func (a *App) handleMailRecoverAsk(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || a.vayuMail.Accounts() == nil {
		a.renderRecoveryPage(w, r, recoveryNoticePage("Recovery unavailable",
			"VayuMail is not running on this server."))
		return
	}
	if r.Method == http.MethodGet {
		a.renderRecoveryPage(w, r, recoveryAskFormPage("", ""))
		return
	}
	addr := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	note := strings.TrimSpace(r.FormValue("note"))
	ip := clientIPForContact(r)

	if !recoveryByIP.allow(ip) || !recoveryByAddress.allow(addr) {
		// Same page as success. A visible "too many requests" would confirm that
		// earlier attempts landed on something real.
		a.renderRecoveryPage(w, r, recoveryAskedPage())
		return
	}
	if addr == "" {
		a.renderRecoveryPage(w, r, recoveryAskFormPage(addr, "Enter your mail address."))
		return
	}
	if err := a.vayuMail.Accounts().FileRecoveryRequest(r.Context(), addr, note, ip); err != nil {
		logging.LogError("vayumail", "could not file a recovery request", err.Error())
	}
	dbpkg.AuditLog("vayumail.recovery.help_requested", "public:"+ip, addr, note)
	a.renderRecoveryPage(w, r, recoveryAskedPage())
}
