package main

// admin_os_mail_recovery.go — the console side of VayuMail account recovery
// (ADR-0144): enrolment, and the readiness view.
//
// The readiness view is the part that makes recovery real rather than nominal.
// A mailbox with no factor enrolled behaves identically to one with ten — right
// up to the day its holder is locked out, at which point nothing can be done.
// The failure is silent by construction, so it has to be visible before then.

import (
	"encoding/json"
	"html"
	"net/http"
	"strconv"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/safefetch"
	"github.com/johalputt/vayupress/internal/vayuos/mail"
)

// handleVayuOSRecoveryStatus returns the enrolment state of one mailbox, or of
// every mailbox when no address is given.
func (a *App) handleVayuOSRecoveryStatus(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, 403, "forbidden", "administrators only", "")
		return
	}
	accts, ok := a.recoveryAccounts()
	if !ok {
		writeAPIError(w, r, 503, "unavailable", "VayuMail is not running", "")
		return
	}
	if addr := strings.TrimSpace(r.URL.Query().Get("email")); addr != "" {
		writeJSON(w, r, 200, accts.RecoveryStatusFor(r.Context(), addr))
		return
	}
	writeJSON(w, r, 200, map[string]interface{}{
		"unrecoverable": accts.UnrecoverableAccounts(r.Context()),
	})
}

// handleVayuOSRecoveryCodes generates a fresh set of recovery codes.
//
// The plaintext is returned exactly once, in this response, and never stored —
// so a caller that loses it must generate again. That is the point: a code set
// the server could re-read would be a code set an attacker could re-read.
func (a *App) handleVayuOSRecoveryCodes(w http.ResponseWriter, r *http.Request) {
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
		Email string `json:"email"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	addr := strings.TrimSpace(in.Email)
	if addr == "" {
		writeAPIError(w, r, 400, "validation_error", "mailbox required", "")
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
		Email   string `json:"email"`
		Contact string `json:"contact"`
		Action  string `json:"action"` // set | verify | clear
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	addr := strings.TrimSpace(in.Email)
	if addr == "" {
		writeAPIError(w, r, 400, "validation_error", "mailbox required", "")
		return
	}

	switch strings.ToLower(strings.TrimSpace(in.Action)) {
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

	// Enrolment.
	b.WriteString(`<div class="section-head mt-4"><span class="section-head__title">Enrol a mailbox</span>
<span class="section-head__hint">Codes work everywhere; an address is easier to keep</span></div>`)
	var opts strings.Builder
	for _, m := range mailboxes {
		opts.WriteString(`<option value="` + html.EscapeString(m) + `">` + html.EscapeString(m) + `</option>`)
	}
	b.WriteString(`<div class="card" data-recovery-panel>
  <label class="field"><span class="field-label">Mailbox</span>
    <select class="input" data-rec-mailbox aria-label="Mailbox">` + opts.String() + `</select></label>
  <div class="rec-status text-sm muted" data-rec-status>Select a mailbox to see its recovery state.</div>

  <div class="section-head mt-4"><span class="section-head__title">Recovery codes</span>
    <span class="section-head__hint">Ten single-use codes, shown once</span></div>
  <p class="text-sm muted">These need no network and no second mailbox, so they keep working when the mail
  system itself is the problem. Generating a new set <strong>invalidates the previous one</strong>.</p>
  <button type="button" class="btn btn--sm" data-rec-gen>Generate new codes</button>
  <div class="rec-codes" data-rec-codes hidden></div>

  <div class="section-head mt-4"><span class="section-head__title">Recovery address</span>
    <span class="section-head__hint">Must be on a different mail provider</span></div>
  <p class="text-sm muted">An address this server hosts is refused: losing the mailbox would lose the
  recovery address with it.</p>
  <div class="vm-row vm-row--end">
    <label class="field vm-grow"><span class="field-label">Address</span>
      <input class="input" type="email" data-rec-contact placeholder="someone@another-provider.com"
             aria-label="Recovery address"></label>
    <button type="button" class="btn btn--sm" data-rec-set>Save</button>
    <button type="button" class="btn btn--sm" data-rec-verify>Mark verified</button>
    <button type="button" class="btn btn--sm btn--ghost" data-rec-clear>Remove</button>
  </div>
  <p class="text-sm muted mt-2">An address only becomes a usable factor once it is verified — confirm the
  holder really controls it first.</p>
  <div class="text-sm" data-rec-msg role="status" aria-live="polite"></div>
</div>`)

	b.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail-recovery.js?v=` +
		assetVer("js/admin-os-mail-recovery.js") + `"></script>`)

	return monAcc("🔑", "Account recovery", "How a locked-out holder gets back in", chip,
		len(stuck) > 0, b.String())
}
