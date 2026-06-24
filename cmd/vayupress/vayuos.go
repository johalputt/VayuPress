// vayuos.go — VayuOS control layer wiring.
//
// This file boots the VayuOS subsystems (VayuPGP privacy layer, VayuMail
// sovereignty layer, security-update watcher), wires the event bus so that
// account creation auto-provisions PGP keys and mailboxes, and serves the
// VayuOS panel pages plus the public WKD key directory. All panel routes are
// registered under the existing session-protected admin console.
package main

import (
	"context"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/email"
	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/render"
	vkernel "github.com/johalputt/vayupress/internal/vayuos/kernel"
	vmail "github.com/johalputt/vayupress/internal/vayuos/mail"
	vpgp "github.com/johalputt/vayupress/internal/vayuos/pgp"
	"github.com/johalputt/vayupress/internal/vayuos/secwatch"
)

// ── Mail bridge ──────────────────────────────────────────────────────────────

// vayuMailBridge connects VayuMail to VayuPress core (auth, transactional mail,
// PGP). It never stores plaintext passwords or private keys.
type vayuMailBridge struct{ app *App }

func (b *vayuMailBridge) AuthUser(username, password string) (bool, error) {
	domain := b.app.vayuMail.Config().Domain
	addr := username
	if !strings.Contains(addr, "@") {
		addr = username + "@" + domain
	}
	if b.app.userStore == nil {
		return false, nil
	}
	_, err := b.app.userStore.Authenticate(context.Background(), addr, password)
	return err == nil, nil
}

func (b *vayuMailBridge) GetUserByEmail(emailAddr string) (*vmail.MailUser, error) {
	if b.app.userStore == nil {
		return nil, fmt.Errorf("vayumail: user store unavailable")
	}
	users, err := b.app.userStore.List(context.Background())
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if strings.EqualFold(u.Email, emailAddr) {
			local := emailAddr
			if i := strings.Index(local, "@"); i >= 0 {
				local = local[:i]
			}
			return &vmail.MailUser{UserID: u.ID, Email: u.Email, Domain: b.app.vayuMail.Config().Domain, Username: local}, nil
		}
	}
	return nil, fmt.Errorf("vayumail: no such user")
}

func (b *vayuMailBridge) SendTransactional(msg *vmail.TransactionalMessage) error {
	if b.app.mailer == nil || !b.app.mailer.Enabled() {
		return fmt.Errorf("vayumail: transactional mailer not configured")
	}
	for _, to := range msg.To {
		if err := b.app.mailer.Send(email.Message{To: to, Subject: msg.Subject, Text: msg.PlainBody, HTML: msg.Body}); err != nil {
			return fmt.Errorf("send transactional to %s: %w", to, err)
		}
	}
	return nil
}

func (b *vayuMailBridge) EncryptForRecipient(plaintext []byte, recipientEmail string) ([]byte, bool) {
	if b.app.vayuPGP == nil {
		return nil, false
	}
	ct, err := b.app.vayuPGP.Encrypt(plaintext, recipientEmail)
	if err != nil || len(ct) == 0 {
		return nil, false
	}
	return ct, true
}

func (b *vayuMailBridge) SignAs(plaintext []byte, senderUserID string) ([]byte, bool) {
	if b.app.vayuPGP == nil || senderUserID == "" {
		return nil, false
	}
	sig, err := b.app.vayuPGP.Sign(plaintext, senderUserID)
	if err != nil {
		return nil, false
	}
	return sig, true
}

var _ vmail.Bridge = (*vayuMailBridge)(nil)

// pgpDecryptForAccount transparently decrypts an inline PGP message for the
// account that owns the mailbox, when VayuPGP holds that account's private key.
// It is best-effort: on any failure it returns the original bytes unchanged so
// the client always receives a readable (if still-encrypted) message.
func (a *App) pgpDecryptForAccount(accountEmail string, raw []byte) []byte {
	if a.vayuPGP == nil {
		return raw
	}
	const begin = "-----BEGIN PGP MESSAGE-----"
	const end = "-----END PGP MESSAGE-----"
	s := string(raw)
	bi := strings.Index(s, begin)
	if bi < 0 {
		return raw
	}
	ei := strings.Index(s, end)
	if ei < 0 || ei < bi {
		return raw
	}
	ei += len(end)
	armored := s[bi:ei]

	mu, err := (&vayuMailBridge{app: a}).GetUserByEmail(accountEmail)
	if err != nil || mu == nil {
		return raw
	}
	plain, err := a.vayuPGP.Decrypt([]byte(armored), mu.UserID)
	if err != nil {
		return raw
	}
	// Splice the decrypted text back in place of the armored block.
	return []byte(s[:bi] + string(plain) + s[ei:])
}

// ── Boot ─────────────────────────────────────────────────────────────────────

// bootVayuOS constructs and boots the VayuOS subsystems in dependency order:
// VayuPGP (critical) → VayuMail (degrades if no domain) → SecWatch. It then
// registers health checks and wires the account-lifecycle event handlers.
func (a *App) bootVayuOS() {
	master := []byte(config.Cfg.APIKey)
	base := filepath.Dir(config.EnvOr("DB_PATH", "./vayupress.db"))

	pgpCfg := vpgp.DefaultConfig()
	pgpCfg.StorageDir = filepath.Join(base, "vayudata", "pgp")
	pgpCfg.MasterSecret = master
	a.vayuPGP = vpgp.NewEngine(&pgpCfg)

	mailCfg := vmail.DefaultConfig()
	mailCfg.StorageDir = filepath.Join(base, "vayudata", "mail")
	if d := config.Cfg.Domain; d != "" && d != "localhost" {
		mailCfg.Domain = d
		mailCfg.Hostname = "mail." + d
		mailCfg.Enabled = true
	}
	// Inbound receive side is an explicit opt-in (Operational Simplicity Doctrine).
	if strings.EqualFold(config.EnvOr("VAYUOS_MAIL_INBOUND", "off"), "on") {
		mailCfg.InboundEnabled = true
		mailCfg.SMTPListen = config.EnvOr("VAYUOS_MAIL_SMTP_LISTEN", ":25")
		mailCfg.IMAPListen = config.EnvOr("VAYUOS_MAIL_IMAP_LISTEN", ":143")
	}
	a.vayuMail = vmail.NewEngine(&mailCfg, &vayuMailBridge{app: a}, dbpkg.DB)
	// Transparent PGP decryption when serving mail over IMAP to the owner.
	a.vayuMail.SetDecryptHook(a.pgpDecryptForAccount)

	secEnabled := strings.EqualFold(config.EnvOr("VAYUOS_SECURITY_UPDATES", "off"), "on")
	a.vayuSec = secwatch.New(secEnabled)

	a.vayuKernel = vkernel.NewBus()
	a.vayuHealth = vkernel.NewHealthMonitor()

	steps := []vkernel.Step{
		{Sub: a.vayuPGP, Critical: true},
		{Sub: a.vayuMail, Critical: false},
		{Sub: a.vayuSec, Critical: false},
	}
	if _, err := vkernel.Boot(context.Background(), steps, func(s string) { logging.LogInfo("vayuos", s) }); err != nil {
		logging.LogError("vayuos", "VayuOS boot failed", err.Error())
	}

	// Health checks surfaced at /os/api/vayuos/health.
	a.vayuHealth.Register("vayupgp", func() (bool, string) {
		if a.vayuPGP == nil {
			return false, "not initialised"
		}
		return true, "Ed25519/Curve25519 keystore active"
	})
	a.vayuHealth.Register("vayumail", func() (bool, string) {
		if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
			return false, "disabled — set a domain in the wizard"
		}
		return true, "outbound queue + DKIM active"
	})
	a.vayuHealth.Register("vayusecwatch", func() (bool, string) {
		if a.vayuSec != nil && a.vayuSec.Enabled() {
			return true, "monitoring upstream security releases"
		}
		return true, "disabled (privacy default) — set VAYUOS_SECURITY_UPDATES=on"
	})

	// Account lifecycle: UserCreated → auto PGP keypair + mailbox.
	a.vayuKernel.Subscribe(vkernel.UserCreated{}, func(_ context.Context, ev vkernel.Event) {
		e := ev.(vkernel.UserCreated)
		if a.vayuPGP != nil {
			if kp, err := a.vayuPGP.GenerateKeypair(&vpgp.PGPUser{UserID: e.UserID, Name: e.Name, Email: e.Email}); err != nil {
				logging.LogError("vayuos", "auto PGP keygen failed for "+e.Email, err.Error())
			} else {
				// Log only the fingerprint — never key material.
				logging.LogInfo("vayuos", "auto-generated PGP keypair for "+e.Email+" fp="+kp.Fingerprint)
			}
		}
		if a.vayuMail != nil && a.vayuMail.Config().Enabled {
			local := e.Email
			if i := strings.Index(local, "@"); i >= 0 {
				local = local[:i]
			}
			if err := a.vayuMail.CreateMailbox("", local); err != nil {
				logging.LogError("vayuos", "auto-create mailbox failed for "+e.Email, err.Error())
			} else {
				logging.LogInfo("vayuos", "auto-provisioned mailbox for "+e.Email)
			}
		}
	})

	logging.LogInfo("vayuos", "VayuOS control layer online (Publishing · Mail · PGP)")
}

// publishUserCreated notifies VayuOS that an account was created.
func (a *App) publishUserCreated(ctx context.Context, userID, name, emailAddr string) {
	if a.vayuKernel == nil {
		return
	}
	a.vayuKernel.Publish(ctx, vkernel.UserCreated{UserID: userID, Name: name, Email: emailAddr})
}

// ── Public WKD ───────────────────────────────────────────────────────────────

// handleWKD serves the Web Key Directory for the configured domain at
// /.well-known/openpgpkey/. It is public by design (key discovery).
func (a *App) handleWKD(w http.ResponseWriter, r *http.Request) {
	if a.vayuPGP == nil {
		http.NotFound(w, r)
		return
	}
	a.vayuPGP.ServeWKD(config.Cfg.Domain).ServeHTTP(w, r)
}

// ── Panel pages ──────────────────────────────────────────────────────────────

func (a *App) handleVayuOSDashboard(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	snap := a.vayuHealth.Snapshot()
	var rows strings.Builder
	for _, c := range snap.Components {
		badge := `<span class="badge badge-ok">OK</span>`
		if !c.OK {
			badge = `<span class="badge badge-warn">DEGRADED</span>`
		}
		rows.WriteString(`<tr><td>` + html.EscapeString(c.Name) + `</td><td>` + badge + `</td><td class="muted">` + html.EscapeString(c.Detail) + `</td></tr>`)
	}
	body := `<div class="page-header"><h1>VayuMail</h1>
<span class="muted text-sm">Native mail sovereignty + privacy — Mail · Inbox · Sent · PGP · DNS</span></div>` + vayuosNav("overview") + `
<div class="grid grid-3">
  <div class="card"><div class="card-title">Inbox</div><p class="muted">Read mail received into your mailboxes (Maildir).</p><a class="btn" href="/os/vayuos/mail/inbox">Open inbox</a></div>
  <div class="card"><div class="card-title">Sent</div><p class="muted">Outbound delivery queue with per-message status.</p><a class="btn" href="/os/vayuos/mail/sent">View sent</a></div>
  <div class="card"><div class="card-title">Privacy (VayuPGP)</div><p class="muted">End-to-end PGP, keys encrypted at rest, WKD published.</p><a class="btn" href="/os/vayuos/pgp">Manage keys</a></div>
  <div class="card"><div class="card-title">Sovereignty (VayuMail)</div><p class="muted">DKIM-signed outbound mail, direct-to-MX, DNS health.</p><a class="btn" href="/os/vayuos/mail">Mail &amp; DNS</a></div>
  <div class="card"><div class="card-title">Security updates</div><p class="muted">Track upstream PGP/crypto security releases.</p><a class="btn" href="/os/vayuos/security">Updates</a></div>
</div>
<div class="card"><div class="card-title">Subsystem health</div>
<div class="table-wrap"><table class="table"><thead><tr><th>Component</th><th>Status</th><th>Detail</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>`
	writeOSHTML(w, adminOSLayout(nonce, "VayuMail", "vayuos", cfg, htmpl.HTML(body)))
}

func (a *App) handleVayuOSPGP(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	keys, _ := a.vayuPGP.ListKeys()
	var rows strings.Builder
	for _, k := range keys {
		state := `<span class="badge badge-ok">active</span>`
		if k.Revoked {
			state = `<span class="badge badge-warn">revoked</span>`
		} else if time.Now().After(k.ExpiresAt) {
			state = `<span class="badge badge-warn">expired</span>`
		}
		rows.WriteString(`<tr><td>` + html.EscapeString(k.Email) + `</td><td class="mono text-sm">` + html.EscapeString(k.Fingerprint) + `</td><td>` + state + `</td><td class="muted">` + k.ExpiresAt.Format("2006-01-02") + `</td></tr>`)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="4" class="muted">No keys yet — keys are generated automatically when accounts are created.</td></tr>`)
	}
	body := `<div class="page-header"><h1>VayuPGP keys</h1>
<span class="muted text-sm">Ed25519 + Curve25519 · private keys AES-256-GCM encrypted at rest · published via WKD</span></div>` + vayuosNav("pgp") + `
<div class="card"><div class="card-title">Keypairs</div>
<div class="table-wrap"><table class="table"><thead><tr><th>Email</th><th>Fingerprint</th><th>State</th><th>Expires</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>
<div class="card"><div class="card-title">Web Key Directory</div><p class="muted">External clients discover these keys at <code>/.well-known/openpgpkey/</code> (advanced method).</p></div>`
	writeOSHTML(w, adminOSLayout(nonce, "VayuPGP", "vayuos", cfg, htmpl.HTML(body)))
}

func (a *App) handleVayuOSMail(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	mc := a.vayuMail.Config()
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>VayuMail</h1><span class="muted text-sm">Native outbound mail sovereignty</span></div>`)
	body.WriteString(vayuosNav("mail"))
	if !mc.Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set your domain (DOMAIN env / first-boot wizard) to activate DKIM signing and outbound delivery.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "VayuMail", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	qs, stats, _ := a.vayuMail.QueueStatus(r.Context())
	body.WriteString(`<div class="grid grid-3">
  <div class="card"><div class="card-title">Pending</div><div class="stat">` + itoaSafe(qs.Pending) + `</div></div>
  <div class="card"><div class="card-title">Delivered</div><div class="stat">` + itoaSafe(stats.Delivered) + `</div></div>
  <div class="card"><div class="card-title">Failed</div><div class="stat">` + itoaSafe(qs.Failed) + `</div></div>
</div>`)
	// DNS records.
	body.WriteString(`<div class="card"><div class="card-title">DNS records to publish (` + html.EscapeString(mc.Domain) + `)</div><div class="table-wrap"><table class="table"><thead><tr><th>Type</th><th>Name</th><th>Value</th></tr></thead><tbody>`)
	for _, rec := range a.vayuMail.PlannedRecords() {
		body.WriteString(`<tr><td>` + html.EscapeString(rec.Type) + `</td><td class="mono text-sm">` + html.EscapeString(rec.Name) + `</td><td class="mono text-sm" style="word-break:break-all">` + html.EscapeString(rec.Value) + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	// Live DNS health.
	hc := a.vayuMail.Health(r.Context())
	body.WriteString(`<div class="card"><div class="card-title">Live DNS health</div><div class="table-wrap"><table class="table"><thead><tr><th>Record</th><th>Status</th><th>Found</th></tr></thead><tbody>`)
	for _, rh := range hc.Records {
		badge := `<span class="badge badge-ok">ok</span>`
		if !rh.OK {
			badge = `<span class="badge badge-warn">missing</span>`
		}
		body.WriteString(`<tr><td>` + html.EscapeString(rh.Type) + `</td><td>` + badge + `</td><td class="mono text-sm" style="word-break:break-all">` + html.EscapeString(rh.Found) + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	writeOSHTML(w, adminOSLayout(nonce, "VayuMail", "vayuos", cfg, htmpl.HTML(body.String())))
}

func (a *App) handleVayuOSSecurity(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	rep, _ := a.vayuSec.Check(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Security updates</h1><span class="muted text-sm">Upstream PGP &amp; crypto dependency monitoring</span></div>`)
	body.WriteString(vayuosNav("security"))
	if !rep.Enabled {
		body.WriteString(`<div class="empty-state">The security-update watcher is disabled by default (privacy first). Enable it by setting <code>VAYUOS_SECURITY_UPDATES=on</code>. It fetches only public release metadata from GitHub and never transmits anything about your site.</div>`)
		// Still show the pinned versions (read from build info, no network).
		body.WriteString(buildComponentTable(rep.Components))
		writeOSHTML(w, adminOSLayout(nonce, "Security updates", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	if rep.UpdatesAvailable > 0 {
		body.WriteString(`<div class="warn-box">` + itoaSafe(rep.UpdatesAvailable) + ` security-relevant update(s) available. ` + html.EscapeString(rep.UpgradeHint) + `</div>`)
	}
	body.WriteString(buildComponentTable(rep.Components))
	writeOSHTML(w, adminOSLayout(nonce, "Security updates", "vayuos", cfg, htmpl.HTML(body.String())))
}

func buildComponentTable(comps []secwatch.Component) string {
	var sb strings.Builder
	sb.WriteString(`<div class="card"><div class="card-title">Tracked dependencies</div><div class="table-wrap"><table class="table"><thead><tr><th>Component</th><th>Current</th><th>Latest</th><th>Status</th></tr></thead><tbody>`)
	for _, c := range comps {
		status := `<span class="badge badge-ok">up to date</span>`
		if c.UpdateAvailable {
			status = `<span class="badge badge-warn">update available</span>`
		}
		latest := c.Latest
		if latest == "" {
			latest = "—"
		}
		sb.WriteString(`<tr><td>` + html.EscapeString(c.Name) + `</td><td class="mono text-sm">` + html.EscapeString(c.Current) + `</td><td class="mono text-sm">` + html.EscapeString(latest) + `</td><td>` + status + `</td></tr>`)
	}
	sb.WriteString(`</tbody></table></div></div>`)
	return sb.String()
}

// handleVayuOSHealthJSON exposes the VayuOS health snapshot as JSON.
func (a *App) handleVayuOSHealthJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, a.vayuHealth.Snapshot())
}

// vayuosNav renders the VayuOS sub-navigation shown on every VayuOS page.
func vayuosNav(active string) string {
	items := []struct{ key, label, href string }{
		{"overview", "Overview", "/os/vayuos"},
		{"inbox", "Inbox", "/os/vayuos/mail/inbox"},
		{"sent", "Sent", "/os/vayuos/mail/sent"},
		{"pgp", "PGP Keys", "/os/vayuos/pgp"},
		{"mail", "Mail & DNS", "/os/vayuos/mail"},
		{"security", "Security", "/os/vayuos/security"},
	}
	var sb strings.Builder
	sb.WriteString(`<div class="tab-bar" style="display:flex;flex-wrap:wrap;gap:.25rem;margin-bottom:1rem;border-bottom:1px solid var(--border,#222);">`)
	for _, it := range items {
		cls := "tab"
		if it.key == active {
			cls = "tab active"
		}
		sb.WriteString(`<a class="` + cls + `" href="` + it.href + `" style="padding:.5rem .85rem;border-radius:.4rem .4rem 0 0;">` + html.EscapeString(it.label) + `</a>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// handleVayuOSInbox lists mailboxes, or (with ?user=) the messages in one.
func (a *App) handleVayuOSInbox(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Inbox</h1><span class="muted text-sm">Received mail (Maildir)</span></div>`)
	body.WriteString(vayuosNav("inbox"))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to a real domain to provision mailboxes. To receive mail, also enable the inbound listener with <code>VAYUOS_MAIL_INBOUND=on</code>.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Inbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	user := strings.TrimSpace(r.URL.Query().Get("user"))

	if user == "" {
		// Mailbox list.
		boxes, err := a.vayuMail.Mailboxes()
		if err != nil {
			body.WriteString(`<div class="empty-state">Could not read mailboxes: ` + html.EscapeString(err.Error()) + `</div>`)
			writeOSHTML(w, adminOSLayout(nonce, "Inbox", "vayuos", cfg, htmpl.HTML(body.String())))
			return
		}
		body.WriteString(`<div class="card"><div class="card-title">Mailboxes</div><div class="table-wrap"><table class="table"><thead><tr><th>Mailbox</th><th>Messages</th><th>Unseen</th></tr></thead><tbody>`)
		if len(boxes) == 0 {
			body.WriteString(`<tr><td colspan="3" class="muted">No mailboxes yet — one is provisioned automatically when an account is created.</td></tr>`)
		}
		for _, b := range boxes {
			addr := b.Username + "@" + domain
			body.WriteString(`<tr><td><a href="/os/vayuos/mail/inbox?user=` + url.QueryEscape(b.Username) + `">` + html.EscapeString(addr) + `</a></td><td>` + itoaSafe(b.Total) + `</td><td>` + itoaSafe(b.Unseen) + `</td></tr>`)
		}
		body.WriteString(`</tbody></table></div></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Inbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	// Messages in one mailbox.
	msgs, err := a.vayuMail.Inbox(domain, user)
	if err != nil {
		body.WriteString(`<div class="empty-state">Could not read mailbox: ` + html.EscapeString(err.Error()) + `</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Inbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div class="card"><div class="card-title">` + html.EscapeString(user+"@"+domain) + ` · <a href="/os/vayuos/mail/inbox">all mailboxes</a></div><div class="table-wrap"><table class="table"><thead><tr><th>From</th><th>Subject</th><th>Date</th></tr></thead><tbody>`)
	if len(msgs) == 0 {
		body.WriteString(`<tr><td colspan="3" class="muted">No messages. Mail arrives here once the inbound SMTP listener (VAYUOS_MAIL_INBOUND=on) receives it.</td></tr>`)
	}
	for _, m := range msgs {
		subj := m.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		link := "/os/vayuos/mail/message?user=" + url.QueryEscape(user) + "&id=" + url.QueryEscape(m.ID)
		seen := ""
		if !m.Seen {
			seen = ` <span class="badge badge-ok">new</span>`
		}
		body.WriteString(`<tr><td class="text-sm">` + html.EscapeString(m.From) + `</td><td><a href="` + link + `">` + html.EscapeString(subj) + `</a>` + seen + `</td><td class="muted text-sm">` + m.Date.Format("2006-01-02 15:04") + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Inbox", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSMessage shows a single received message (PGP-decrypted if possible).
func (a *App) handleVayuOSMessage(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Message</h1><span class="muted text-sm">` + html.EscapeString(user) + `</span></div>`)
	body.WriteString(vayuosNav("inbox"))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || user == "" || id == "" {
		body.WriteString(`<div class="empty-state">Message not available. <a href="/os/vayuos/mail/inbox">Back to Inbox</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	raw, err := a.vayuMail.ReadInboxMessage("", user, id)
	if err != nil {
		body.WriteString(`<div class="empty-state">Could not read message: ` + html.EscapeString(err.Error()) + ` <a href="/os/vayuos/mail/inbox?user=` + url.QueryEscape(user) + `">Back</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div class="card"><div class="card-title"><a href="/os/vayuos/mail/inbox?user=` + url.QueryEscape(user) + `">← Back to mailbox</a></div>`)
	body.WriteString(`<pre style="white-space:pre-wrap;word-break:break-word;font-size:13px;line-height:1.5;max-height:70vh;overflow:auto;">` + html.EscapeString(string(raw)) + `</pre></div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSSent lists recent outbound messages from the delivery queue.
func (a *App) handleVayuOSSent(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Sent</h1><span class="muted text-sm">Outbound delivery queue</span></div>`)
	body.WriteString(vayuosNav("sent"))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to activate outbound delivery.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Sent", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	sent, err := a.vayuMail.Sent(r.Context(), 100)
	if err != nil {
		body.WriteString(`<div class="empty-state">Could not read outbound queue: ` + html.EscapeString(err.Error()) + `</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Sent", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div class="card"><div class="card-title">Recent outbound</div><div class="table-wrap"><table class="table"><thead><tr><th>To</th><th>Subject</th><th>Status</th><th>When</th></tr></thead><tbody>`)
	if len(sent) == 0 {
		body.WriteString(`<tr><td colspan="4" class="muted">Nothing sent yet. Mail sent through VayuMail (DKIM-signed, direct-to-MX) appears here with delivery status.</td></tr>`)
	}
	for _, s := range sent {
		subj := s.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		badge := `<span class="badge badge-ok">` + html.EscapeString(s.State) + `</span>`
		if s.State == "failed" {
			badge = `<span class="badge badge-warn">failed</span>`
		} else if s.State == "pending" {
			badge = `<span class="badge">pending</span>`
		}
		when := s.CreatedAt
		if len(when) > 19 {
			when = when[:19]
		}
		body.WriteString(`<tr><td class="text-sm">` + html.EscapeString(strings.Join(s.To, ", ")) + `</td><td>` + html.EscapeString(subj) + `</td><td>` + badge + `</td><td class="muted text-sm">` + html.EscapeString(when) + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Sent", "vayuos", cfg, htmpl.HTML(body.String())))
}

func itoaSafe(n int) string { return fmt.Sprintf("%d", n) }
