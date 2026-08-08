// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_mail_recovery.go — the console side of VayuMail account recovery
// (ADR-0144): enrolment, and the readiness view.
//
// The readiness view is the part that makes recovery real rather than nominal.
// A mailbox with no factor enrolled behaves identically to one with ten — right
// up to the day its holder is locked out, at which point nothing can be done.
// The failure is silent by construction, so it has to be visible before then.

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/seo"
	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// recoveryScope resolves WHICH mailbox this request may act on, and is the only
// authorisation check the three endpoints below need.
//
// Recovery must not be an administrator-only errand. The people who get locked
// out are mailbox holders, and requiring an admin to enrol every one of them
// guarantees most are never enrolled — which is exactly the state the readiness
// view keeps reporting. So a holder may enrol their OWN mailbox.
//
// The rule is deliberately narrow: an administrator may name any mailbox; anyone
// else is forced to their own assigned address regardless of what they asked for.
// A holder cannot even read another mailbox's recovery state, because "does this
// account have codes" is useful reconnaissance on its own.
func (a *App) recoveryScope(r *http.Request, requested string) (string, bool) {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if a.isAdminRequest(r) {
		return requested, true
	}
	_, own := a.ownMailbox(r)
	own = strings.ToLower(strings.TrimSpace(own))
	if own == "" {
		return "", false
	}
	// A non-admin gets their own mailbox whatever they asked for. Narrowing
	// SILENTLY is the point and the reason there is no branch here: refusing a
	// mismatch would answer "that is not you" for an address that exists and
	// "no mailbox" for one that does not, turning this endpoint into an
	// enumeration oracle over every mailbox on the install. `requested` is
	// therefore not consulted at all — a value that is merely validated is one a
	// later change can start trusting.
	return own, true
}

// handleVayuOSRecoveryStatus returns the enrolment state of one mailbox, or —
// for an administrator with no address given — the whole readiness list.
func (a *App) handleVayuOSRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	accts, ok := a.recoveryAccounts()
	if !ok {
		writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
		return
	}
	requested := strings.TrimSpace(r.URL.Query().Get("email"))
	// The install-wide readiness list is an administrator view: it names every
	// mailbox that cannot be recovered, which is a target list for anyone else.
	if requested == "" && a.isAdminRequest(r) {
		writeJSON(w, r, 200, map[string]interface{}{
			"unrecoverable": accts.UnrecoverableAccounts(r.Context()),
		})
		return
	}
	addr, allowed := a.recoveryScope(r, requested)
	if !allowed || addr == "" {
		writeAPIError(w, r, 403, "forbidden", "no mailbox is assigned to this account", "")
		return
	}
	writeJSON(w, r, 200, accts.RecoveryStatusFor(r.Context(), addr))
}

// handleVayuOSRecoveryCodes generates a fresh set of recovery codes.
//
// The plaintext is returned exactly once, in this response, and never stored —
// so a caller that loses it must generate again. That is the point: a code set
// the server could re-read would be a code set an attacker could re-read.
func (a *App) handleVayuOSRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	accts, ok := a.recoveryAccounts()
	if !ok {
		writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
		return
	}
	var in struct {
		Email string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	addr, allowed := a.recoveryScope(r, in.Email)
	if !allowed || addr == "" {
		writeAPIError(w, r, 403, "forbidden", "no mailbox is assigned to this account", "")
		return
	}
	codes, err := accts.GenerateRecoveryCodes(r.Context(), addr)
	if err != nil {
		writeAPIError(w, r, 400, "generate-failed", err.Error(), "")
		return
	}
	// Audited because it is a credential-issuing event: whoever holds these can
	// take the mailbox without the password.
	dbpkg.AuditLog("vayumail.recovery.codes_generated", dbpkg.AuditActor(r), addr,
		strconv.Itoa(len(codes))+" codes")
	writeJSON(w, r, 200, map[string]interface{}{"codes": codes, "count": len(codes)})
}

// handleVayuOSRecoveryContact sets, verifies or clears a mailbox's recovery
// address.
//
// "verify" is an administrator confirming out of band that the holder controls
// the address — the console equivalent of the emailed round-trip. It is
// deliberately a separate, explicit action rather than something "set" does
// implicitly: an address that becomes usable the moment it is typed is a typo
// that silently redirects the master key to a stranger.
func (a *App) handleVayuOSRecoveryContact(w http.ResponseWriter, r *http.Request) {
	accts, ok := a.recoveryAccounts()
	if !ok {
		writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
		return
	}
	var in struct {
		Email   string `json:"email"`
		Contact string `json:"contact"`
		Action  string `json:"action"` // set | verify | clear
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	addr, allowed := a.recoveryScope(r, in.Email)
	if !allowed || addr == "" {
		writeAPIError(w, r, 403, "forbidden", "no mailbox is assigned to this account", "")
		return
	}

	action := strings.ToLower(strings.TrimSpace(in.Action))
	// "Mark verified" is an ADMINISTRATOR confirming out of band that the holder
	// controls the address. A holder verifying their own would be self-certifying:
	// they could point recovery at any address and immediately make it usable,
	// which is the whole check the round-trip exists to perform.
	if action == "verify" && !a.isAdminRequest(r) {
		writeAPIError(w, r, 403, "forbidden",
			"An administrator confirms a recovery address. Yours is saved and waiting.", "")
		return
	}

	switch action {
	case "clear":
		if err := accts.ClearRecoveryContact(r.Context(), addr); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
		dbpkg.AuditLog("vayumail.recovery.contact_cleared", dbpkg.AuditActor(r), addr, "")
	case "verify":
		if err := accts.VerifyRecoveryContact(r.Context(), addr); err != nil {
			writeAPIError(w, r, 400, "update-failed", err.Error(), "")
			return
		}
		dbpkg.AuditLog("vayumail.recovery.contact_verified", dbpkg.AuditActor(r), addr, "")
	default: // set
		// The off-install rule is enforced in the store against the set of domains
		// this install actually accepts mail for — not merely "a different domain",
		// because a second domain hosted here dies with the same server.
		if err := accts.SetRecoveryContactPending(r.Context(), addr, in.Contact,
			a.vayuMail.Config().AcceptsMailDomain); err != nil {
			writeAPIError(w, r, 400, "validation_error", err.Error(), "")
			return
		}
		dbpkg.AuditLog("vayumail.recovery.contact_pending", dbpkg.AuditActor(r), addr,
			strings.TrimSpace(in.Contact))
	}
	writeJSON(w, r, 200, accts.RecoveryStatusFor(r.Context(), addr))
}

// recoveryAccounts returns the account store when VayuMail is live.
func (a *App) recoveryAccounts() (*mail.AccountStore, bool) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || a.vayuMail.Accounts() == nil {
		return nil, false
	}
	return a.vayuMail.Accounts(), true
}

// ── The console card ─────────────────────────────────────────────────────────

// recoveryCardHTML renders the enrolment and readiness accordion for the Mail
// accounts page.
func (a *App) recoveryCardHTML(r *http.Request, nonce string, mailboxes []string) string {
	accts, ok := a.recoveryAccounts()
	if !ok {
		return ""
	}
	stuck := accts.UnrecoverableAccounts(r.Context())
	total := len(mailboxes)
	covered := total - len(stuck)

	// The chip states the honest number. "Recovery enabled" would be true of the
	// feature and false of the mailboxes, which is the distinction that matters.
	chip := monChip(len(stuck) == 0,
		strconv.Itoa(covered)+"/"+strconv.Itoa(total)+" covered",
		strconv.Itoa(len(stuck))+" cannot be recovered")

	var b strings.Builder
	b.WriteString(`<p class="text-sm muted mb-4">A mailbox holder who forgets their password cannot be helped by
email — the reset link would be delivered to the mailbox they cannot open. Recovery has to be enrolled
<strong>before</strong> it is needed, so this is where you do it.</p>`)

	// Readiness first: it is the actionable part.
	b.WriteString(`<div class="section-head"><span class="section-head__title">Readiness</span>
<span class="section-head__hint">Who could actually get back in</span></div>`)
	if total == 0 {
		b.WriteString(`<div class="empty-state">No mailboxes yet.</div>`)
	} else if len(stuck) == 0 {
		b.WriteString(`<p class="text-sm">✓ Every mailbox has at least one working recovery factor.</p>`)
	} else {
		b.WriteString(`<p class="text-sm"><strong>` + strconv.Itoa(len(stuck)) +
			`</strong> mailbox` + plural(len(stuck)) + ` cannot be recovered at all. If the holder forgets
their password, only a server operator with shell access can help:</p><ul class="rec-list">`)
		for _, e := range stuck {
			b.WriteString(`<li><code>` + html.EscapeString(e) + `</code></li>`)
		}
		b.WriteString(`</ul>`)
	}

	// Tor mode disables the address factor, and saying so here is the difference
	// between a considered design and a silent failure at the worst moment.
	if safefetch.ClearnetBlocked() {
		b.WriteString(`<p class="text-sm muted mt-4"><strong>Tor mode:</strong> there is no clearnet egress,
so a reset link cannot be delivered to an outside address. <strong>Recovery codes are the only factor
that works here</strong> — generate them for every mailbox.</p>`)
	}

	b.WriteString(`<p class="text-sm muted mt-4">Enrol a mailbox from <strong>its own card</strong> below —
recovery sits with that mailbox's other settings, next to forwarding and aliases.</p>`)
	// Assisted-recovery queue. Shown only when someone is actually waiting: an
	// empty section every day trains the eye to skip the one day it is not empty.
	if pending := accts.PendingRecoveryRequests(r.Context()); len(pending) > 0 {
		b.WriteString(`<div class="section-head mt-4"><span class="section-head__title">Waiting for you</span>
<span class="section-head__hint">Holders who are locked out with nothing enrolled</span></div>`)
		b.WriteString(`<p class="text-sm muted">Approving does not set or reveal a password — it creates a
one-time link you hand over through a channel you trust, and the holder chooses their own. Confirm who you
are talking to first: this is the step an attacker would try to talk you through.</p>`)
		b.WriteString(`<div class="table-wrap"><table class="table"><tbody data-rec-queue>`)
		for _, q := range pending {
			b.WriteString(`<tr data-rec-req="` + strconv.FormatInt(q.ID, 10) + `">` +
				`<td class="mono">` + html.EscapeString(q.Email) + `</td>` +
				`<td class="text-xs muted">` + html.EscapeString(q.Note) + `</td>` +
				`<td class="text-xs muted">` + html.EscapeString(q.IP) + `</td>` +
				`<td><button type="button" class="btn btn--xs btn--primary" data-rec-approve>Approve</button> ` +
				`<button type="button" class="btn btn--xs btn--ghost" data-rec-decline>Decline</button></td></tr>`)
		}
		b.WriteString(`</tbody></table></div>`)
		b.WriteString(`<div class="rec-codes" data-rec-approved hidden></div>`)
	}

	b.WriteString(resetTrailHTML(r.Context()))

	// The script is loaded once here and drives every per-mailbox panel below.
	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail-recovery.js?v=` +
		assetVer("js/admin-os-mail-recovery.js") + `"></script>`)

	return monAcc("🔑", "Account recovery", "Who could get back in if they forgot their password", chip,
		len(stuck) > 0, b.String())
}

// selfRecoveryCardHTML is the holder's own view: one mailbox, theirs, no
// readiness list and no mailbox picker. Rendered on the Connect page, which is
// where a holder already goes to set their mail up.
//
// It exists because recovery that only an administrator can enrol is recovery
// most people never get. The endpoints scope by themselves (recoveryScope), so
// this card is a narrower presentation of the same surface, not a second one.
func (a *App) selfRecoveryCardHTML(r *http.Request, nonce string) string {
	accts, ok := a.recoveryAccounts()
	if !ok {
		return ""
	}
	_, own := a.ownMailbox(r)
	own = strings.ToLower(strings.TrimSpace(own))
	if own == "" || !accts.Exists(r.Context(), own) {
		return ""
	}
	st := accts.RecoveryStatusFor(r.Context(), own)
	chip := monChip(st.Ready, "Recovery ready", "No way back in")

	var b strings.Builder
	b.WriteString(`<p class="text-sm muted mb-4">If you forget this mailbox's password, a reset link is no
use — it would be delivered here, to the mailbox you cannot open. Set one of these up <strong>now</strong>,
while you still can.</p>`)

	b.WriteString(`<div class="card" data-recovery-panel>
  <input type="hidden" data-rec-mailbox value="` + html.EscapeString(own) + `">
  <p class="text-sm"><strong>Mailbox:</strong> <code>` + html.EscapeString(own) + `</code></p>
  <div class="rec-status text-sm muted" data-rec-status></div>

  <div class="section-head mt-4"><span class="section-head__title">Recovery codes</span>
    <span class="section-head__hint">Ten single-use codes, shown once</span></div>
  <p class="text-sm muted">The most reliable option: they need no network and no second mailbox, so they
  work even when mail itself is broken. Save, download or print them somewhere you can reach without this
  mailbox. Generating a new set <strong>invalidates the previous one</strong>.</p>
  <button type="button" class="btn btn--primary btn--sm" data-rec-gen>Generate my codes</button>
  <div class="rec-codes" data-rec-codes hidden></div>

  <div class="section-head mt-4"><span class="section-head__title">Recovery address</span>
    <span class="section-head__hint">On a different mail provider</span></div>
  <p class="text-sm muted">An address this server hosts is refused — losing this mailbox would lose it too.
  Your administrator confirms the address before it counts.</p>
  <div class="vm-row vm-row--end">
    <label class="field vm-grow"><span class="field-label">Address</span>
      <input class="input" type="email" data-rec-contact placeholder="you@another-provider.com"
             aria-label="Recovery address"></label>
    <button type="button" class="btn btn--primary btn--sm" data-rec-set>Save</button>
    <button type="button" class="btn btn--sm btn--ghost" data-rec-clear>Remove</button>
  </div>
  <div class="text-sm" data-rec-msg role="status" aria-live="polite"></div>
</div>`)

	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail-recovery.js?v=` +
		assetVer("js/admin-os-mail-recovery.js") + `"></script>`)

	return monAcc("🔑", "Recover my mailbox", "Set this up before you need it", chip, !st.Ready, b.String())
}

// vayuCardRecovery renders one mailbox's recovery controls INSIDE its own card,
// beside forwarding, vacation and aliases.
//
// The first version put every mailbox behind a single picker at the top of the
// page. That reads as a separate system bolted on: to enrol a mailbox you had to
// leave its card, find it again in a dropdown, and trust you had the right one.
// Recovery is a property OF a mailbox, so it belongs where its other properties
// are — and with 30-odd addresses on an install, one control per card is the only
// arrangement that stays legible.
//
// Each card is an independent panel; the browser script binds them all.
func (a *App) vayuCardRecovery(ctx context.Context, email string) string {
	accts, ok := a.recoveryAccounts()
	if !ok {
		return ""
	}
	st := accts.RecoveryStatusFor(ctx, email)
	he := html.EscapeString(email)

	// The summary badge is the whole point at a glance: scanning the mailbox list
	// should show which accounts have no way back in.
	state := `<span class="badge badge--warn">no recovery</span>`
	if st.Ready {
		bits := []string{}
		if st.CodesRemaining > 0 {
			bits = append(bits, strconv.Itoa(st.CodesRemaining)+" codes")
		}
		if st.Contact != "" {
			bits = append(bits, "address")
		}
		state = `<span class="badge badge--ok">` + html.EscapeString(strings.Join(bits, " + ")) + `</span>`
	} else if st.ContactPending != "" {
		state = `<span class="badge badge--warn">unverified address</span>`
	}

	var b strings.Builder
	b.WriteString(`<details class="vm-ooo vm-acct__sub" data-recovery-panel><summary>` +
		`<span class="field-label">Account recovery</span> ` + state + `</summary>`)
	b.WriteString(`<input type="hidden" data-rec-mailbox value="` + he + `">`)
	b.WriteString(`<p class="muted text-xs">If this holder forgets their password, a reset link is no use — ` +
		`it would be delivered to the mailbox they cannot open. Enrol one of these before that happens.</p>`)
	b.WriteString(`<div class="rec-status text-xs muted" data-rec-status></div>`)

	b.WriteString(`<div class="vm-row vm-row--end mt-2">
  <button type="button" class="btn btn--primary btn--xs" data-rec-gen>Generate recovery codes</button>
</div>
<div class="rec-codes" data-rec-codes hidden></div>`)

	b.WriteString(`<div class="vm-row vm-row--end mt-2">
  <label class="field vm-grow"><span class="field-label">Recovery address (different provider)</span>
    <input class="input input--sm" type="email" data-rec-contact placeholder="someone@another-provider.com"
           aria-label="Recovery address for ` + he + `"></label>
  <button type="button" class="btn btn--xs" data-rec-set>Save</button>
  <button type="button" class="btn btn--xs" data-rec-verify>Mark verified</button>
  <button type="button" class="btn btn--xs btn--ghost" data-rec-clear>Remove</button>
</div>
<div class="text-xs" data-rec-msg role="status" aria-live="polite"></div>`)

	b.WriteString(`</details>`)
	return b.String()
}

// ── Assisted recovery: request and decision ──────────────────────────────────

// handleVayuOSRecoveryRequests lists the requests awaiting a decision.
func (a *App) handleVayuOSRecoveryRequests(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, 403, "forbidden", "administrators only", "")
		return
	}
	accts, ok := a.recoveryAccounts()
	if !ok {
		writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{"requests": accts.PendingRecoveryRequests(r.Context())})
}

// handleVayuOSRecoveryDecide approves or declines an assisted-recovery request.
//
// Approval does NOT reveal or set a password. It mints a one-time reset link,
// returned once to the administrator, who hands it over through a channel they
// trust — in person, or a phone call they placed themselves. The holder then
// chooses their own password on the ordinary reset page.
//
// This matters: an administrator who types the new password knows it, and a
// mailbox whose password is known by someone other than its holder is not
// recovered, it is shared. The link keeps the secret with the person it belongs
// to and leaves the administrator holding only the authorisation.
func (a *App) handleVayuOSRecoveryDecide(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, 403, "forbidden", "administrators only", "")
		return
	}
	accts, ok := a.recoveryAccounts()
	if !ok {
		writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
		return
	}
	var in struct {
		ID     int64  `json:"id"`
		Action string `json:"action"` // approve | decline
	}
	_ = json.NewDecoder(r.Body).Decode(&in)

	decision := "declined"
	if strings.EqualFold(strings.TrimSpace(in.Action), "approve") {
		decision = "approved"
	}
	actor := dbpkg.AuditActor(r)
	email, err := accts.DecideRecoveryRequest(r.Context(), in.ID, decision, actor)
	if err != nil {
		writeAPIError(w, r, 400, "decide-failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayumail.recovery.request_"+decision, actor, email, "")

	if decision != "approved" {
		writeJSON(w, r, 200, map[string]interface{}{"decision": decision, "email": email})
		return
	}
	// A request can be filed for an address that does not exist — the public
	// endpoint accepts anything so it cannot be used to discover mailboxes. Minting
	// a link for one would be pointless, so say so plainly here, where the audience
	// is an administrator who already knows the account list.
	if !accts.Exists(r.Context(), email) {
		writeJSON(w, r, 200, map[string]interface{}{
			"decision": decision, "email": email,
			"warning": "There is no mailbox at that address, so no reset link was created.",
		})
		return
	}
	token, terr := accts.CreateRecoveryToken(r.Context(), email)
	if terr != nil {
		writeAPIError(w, r, 500, "token-failed", "could not create a reset link", "")
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{
		"decision": decision,
		"email":    email,
		"link":     seo.Origin(config.Cfg.Domain) + "/mail/recover/reset?token=" + token,
		"detail": "Give this link to the holder through a channel you trust — in person, or a call you " +
			"placed yourself. It works once, expires in 30 minutes, and lets them choose their own password.",
	})
}

// ── Reset trail ──────────────────────────────────────────────────────────────

// resetEvent is one entry from the WORM audit log.
type resetEvent struct {
	When   string
	Target string
	Actor  string
	Detail string
}

// recentResets reads the last password resets straight from audit_log.
//
// This is the durable notification channel, and it is the operator's rather than
// the holder's. The two notices a reset already sends — to the recovery address
// and into the mailbox — both depend on something the attacker may now control:
// the in-mailbox copy can simply be deleted by whoever holds the new password.
// audit_log is append-only, so this record survives them.
//
// It also replaces the VayuTalk alert originally planned for this phase. VayuTalk
// authenticates with the MAILBOX PASSWORD (Engine.Connect → verify(email,
// password)), so an alert delivered there is unreadable by someone who just lost
// that password, and readable by whoever caused the reset. A channel that fails
// exactly when it is needed, and leaks to the attacker when it works, is worse
// than no channel.
func recentResets(ctx context.Context, limit int) []resetEvent {
	if dbpkg.DB == nil {
		return nil
	}
	rows, err := dbpkg.DB.QueryContext(ctx,
		`SELECT ts,target,actor,detail FROM audit_log
		 WHERE action='vayumail.password.reset' ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []resetEvent
	for rows.Next() {
		var e resetEvent
		var ts time.Time
		if err := rows.Scan(&ts, &e.Target, &e.Actor, &e.Detail); err == nil {
			e.When = ts.UTC().Format("2 Jan 15:04")
			out = append(out, e)
		}
	}
	_ = rows.Err()
	return out
}

// resetTrailHTML renders the recent resets for the recovery card.
func resetTrailHTML(ctx context.Context) string {
	events := recentResets(ctx, 10)
	if len(events) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<div class="section-head mt-4"><span class="section-head__title">Recent password resets</span>
<span class="section-head__hint">From the append-only audit log</span></div>`)
	b.WriteString(`<p class="text-sm muted">A reset also notifies the holder, but the copy filed into their
mailbox can be deleted by whoever holds the new password. This record cannot.</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><tbody>`)
	for _, e := range events {
		b.WriteString(`<tr><td class="text-xs muted">` + html.EscapeString(e.When) + `</td>` +
			`<td class="mono">` + html.EscapeString(e.Target) + `</td>` +
			`<td class="text-xs">` + html.EscapeString(e.Actor) + `</td>` +
			`<td class="text-xs muted">` + html.EscapeString(e.Detail) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}
