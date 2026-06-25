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

	"github.com/johalputt/vayupress/internal/auth"
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
	// 1) CMS users (full VayuPress accounts).
	if b.app.userStore != nil {
		if _, err := b.app.userStore.Authenticate(context.Background(), addr, password); err == nil {
			return true, nil
		}
	}
	// 2) Admin-managed mail-only accounts (email + password).
	if b.app.vayuMail != nil && b.app.vayuMail.Accounts() != nil {
		if hash := b.app.vayuMail.Accounts().HashFor(context.Background(), addr); hash != "" {
			if auth.VerifySecretArgon2id(password, hash) {
				return true, nil
			}
		}
	}
	return false, nil
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
		badge := `<span class="badge badge--ok">OK</span>`
		if !c.OK {
			badge = `<span class="badge badge--warn">DEGRADED</span>`
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
		state := `<span class="badge badge--ok">active</span>`
		if k.Revoked {
			state = `<span class="badge badge--warn">revoked</span>`
		} else if time.Now().After(k.ExpiresAt) {
			state = `<span class="badge badge--warn">expired</span>`
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
  <div class="card"><div class="card-title">Pending</div><div class="vm-stat">` + itoaSafe(qs.Pending) + `</div></div>
  <div class="card"><div class="card-title">Delivered</div><div class="vm-stat">` + itoaSafe(stats.Delivered) + `</div></div>
  <div class="card"><div class="card-title">Failed</div><div class="vm-stat">` + itoaSafe(qs.Failed) + `</div></div>
</div>`)
	// DNS records.
	body.WriteString(`<div class="card"><div class="card-title">DNS records to publish (` + html.EscapeString(mc.Domain) + `)</div><div class="table-wrap"><table class="table"><thead><tr><th>Type</th><th>Name</th><th>Value</th></tr></thead><tbody>`)
	for _, rec := range a.vayuMail.PlannedRecords() {
		body.WriteString(`<tr><td>` + html.EscapeString(rec.Type) + `</td><td class="mono text-sm">` + html.EscapeString(rec.Name) + `</td><td class="mono text-sm vm-break">` + html.EscapeString(rec.Value) + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	// Live DNS health.
	hc := a.vayuMail.Health(r.Context())
	body.WriteString(`<div class="card"><div class="card-title">Live DNS health</div><div class="table-wrap"><table class="table"><thead><tr><th>Record</th><th>Status</th><th>Found</th></tr></thead><tbody>`)
	for _, rh := range hc.Records {
		badge := `<span class="badge badge--ok">ok</span>`
		if !rh.OK {
			badge = `<span class="badge badge--warn">missing</span>`
		}
		body.WriteString(`<tr><td>` + html.EscapeString(rh.Type) + `</td><td>` + badge + `</td><td class="mono text-sm vm-break">` + html.EscapeString(rh.Found) + `</td></tr>`)
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
		status := `<span class="badge badge--ok">up to date</span>`
		if c.UpdateAvailable {
			status = `<span class="badge badge--warn">update available</span>`
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
		{"compose", "Compose", "/os/vayuos/mail/compose"},
		{"mailbox", "Mailbox", "/os/vayuos/mail/inbox"},
		{"accounts", "Accounts", "/os/vayuos/mail/accounts"},
		{"outbox", "Outbox", "/os/vayuos/mail/sent"},
		{"pgp", "PGP Keys", "/os/vayuos/pgp"},
		{"mail", "DNS", "/os/vayuos/mail"},
		{"security", "Security", "/os/vayuos/security"},
	}
	var sb strings.Builder
	sb.WriteString(`<div class="vmtabs">`)
	for _, it := range items {
		cls := "tab"
		if it.key == active {
			cls = "tab tab--active"
		}
		sb.WriteString(`<a class="` + cls + `" href="` + it.href + `">` + html.EscapeString(it.label) + `</a>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// folderTabs renders the mailbox folder selector (Inbox/Sent/Drafts/Junk/Trash).
// qparam returns a query-string value that is safe both inside the URL and inside
// the surrounding HTML attribute: url.QueryEscape handles URL encoding, and the
// html.EscapeString wrapper is a no-op on that output but gives static analysis
// (CodeQL go/reflected-xss) the HTML-context sanitiser barrier it recognises.
func qparam(s string) string { return html.EscapeString(url.QueryEscape(s)) }

func folderTabs(user, active string) string {
	var sb strings.Builder
	sb.WriteString(`<div class="vmtabs">`)
	for _, f := range []string{"Inbox", "Sent", "Drafts", "Archive", "Junk", "Trash"} {
		cls := "tab"
		if strings.EqualFold(f, active) {
			cls = "tab tab--active"
		}
		href := "/os/vayuos/mail/inbox?user=" + qparam(user) + "&folder=" + qparam(f)
		sb.WriteString(`<a class="` + cls + `" href="` + href + `">` + f + `</a>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// handleVayuOSInbox lists mailboxes, or (with ?user=) the messages in a folder.
func (a *App) handleVayuOSInbox(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Mailbox</h1><span class="muted text-sm">Received &amp; filed mail (Maildir)</span></div>`)
	body.WriteString(vayuosNav("mailbox"))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to a real domain to provision mailboxes. To receive external mail, also enable the inbound listener with <code>VAYUOS_MAIL_INBOUND=on</code>.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}

	if user == "" {
		boxes, err := a.vayuMail.Mailboxes()
		if err != nil {
			body.WriteString(`<div class="empty-state">Could not read mailboxes: ` + html.EscapeString(err.Error()) + `</div>`)
			writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
			return
		}
		body.WriteString(`<div class="card"><div class="card-title">Mailboxes</div><div class="table-wrap"><table class="table"><thead><tr><th>Mailbox</th><th>Inbox messages</th><th>Unseen</th></tr></thead><tbody>`)
		if len(boxes) == 0 {
			body.WriteString(`<tr><td colspan="3" class="muted">No mailboxes yet. Create one under <a href="/os/vayuos/mail/accounts">Accounts</a>, or one is provisioned when a CMS account is created.</td></tr>`)
		}
		for _, b := range boxes {
			addr := b.Username + "@" + domain
			body.WriteString(`<tr><td><a href="/os/vayuos/mail/inbox?user=` + qparam(b.Username) + `">` + html.EscapeString(addr) + `</a></td><td>` + itoaSafe(b.Total) + `</td><td>` + itoaSafe(b.Unseen) + `</td></tr>`)
		}
		body.WriteString(`</tbody></table></div></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	msgs, err := a.vayuMail.ListFolder(user, folder)
	if err != nil {
		body.WriteString(`<div class="empty-state">Could not read folder: ` + html.EscapeString(err.Error()) + `</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div class="card"><div class="card-title">` + html.EscapeString(user+"@"+domain) + ` · <a href="/os/vayuos/mail/inbox">all mailboxes</a></div>`)
	body.WriteString(`<form class="vm-search" method="get" action="/os/vayuos/mail/search">
  <input type="hidden" name="user" value="` + html.EscapeString(user) + `">
  <input class="input" type="search" name="q" placeholder="Search mail (from, subject, body)…" aria-label="Search mail">
  <button class="btn" type="submit">Search</button>
</form>`)
	body.WriteString(folderTabs(user, folder))
	body.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>From</th><th>Subject</th><th>Date</th></tr></thead><tbody>`)
	if len(msgs) == 0 {
		body.WriteString(`<tr><td colspan="3" class="muted">No messages in ` + html.EscapeString(folder) + `.</td></tr>`)
	}
	for _, m := range msgs {
		subj := m.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		who := m.From
		if strings.EqualFold(folder, "Sent") || strings.EqualFold(folder, "Drafts") {
			who = "→ " + m.To
		}
		link := "/os/vayuos/mail/message?user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(m.ID)
		seen := ""
		if !m.Seen && strings.EqualFold(folder, "Inbox") {
			seen = ` <span class="badge badge--ok">new</span>`
		}
		body.WriteString(`<tr><td class="text-sm">` + html.EscapeString(who) + `</td><td><a href="` + link + `">` + html.EscapeString(subj) + `</a>` + seen + `</td><td class="muted text-sm">` + m.Date.Format("2006-01-02 15:04") + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSSearch runs a bounded full-text search across a mailbox's folders.
func (a *App) handleVayuOSSearch(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Search mail</h1><span class="muted text-sm">` + html.EscapeString(user+"@"+a.cfgDomain()) + `</span></div>`)
	body.WriteString(vayuosNav("mailbox"))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || user == "" {
		body.WriteString(`<div class="empty-state">VayuMail is inactive or no mailbox selected. <a href="/os/vayuos/mail/inbox">Back to Mailbox</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Search mail", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div class="card"><div class="card-title"><a href="/os/vayuos/mail/inbox?user=` + qparam(user) + `">← ` + html.EscapeString(user+"@"+a.cfgDomain()) + `</a></div>`)
	body.WriteString(`<form class="vm-search" method="get" action="/os/vayuos/mail/search">
  <input type="hidden" name="user" value="` + html.EscapeString(user) + `">
  <input class="input" type="search" name="q" value="` + html.EscapeString(q) + `" placeholder="Search mail…" aria-label="Search mail">
  <button class="btn btn--primary" type="submit">Search</button>
</form>`)
	if q != "" {
		results, _ := a.vayuMail.Search(user, q, 100)
		body.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Folder</th><th>From</th><th>Subject</th><th>Date</th></tr></thead><tbody>`)
		if len(results) == 0 {
			body.WriteString(`<tr><td colspan="4" class="muted">No matches for “` + html.EscapeString(q) + `”.</td></tr>`)
		}
		for _, m := range results {
			subj := m.Subject
			if subj == "" {
				subj = "(no subject)"
			}
			link := "/os/vayuos/mail/message?user=" + qparam(user) + "&folder=" + qparam(m.Folder) + "&id=" + qparam(m.ID)
			body.WriteString(`<tr><td><span class="badge">` + html.EscapeString(m.Folder) + `</span></td><td class="text-sm">` + html.EscapeString(m.From) + `</td><td><a href="` + link + `">` + html.EscapeString(subj) + `</a></td><td class="muted text-sm">` + m.Date.Format("2006-01-02 15:04") + `</td></tr>`)
		}
		body.WriteString(`</tbody></table></div>`)
	}
	body.WriteString(`</div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Search mail", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSMessage shows a single message with Junk/Trash/Delete actions.
func (a *App) handleVayuOSMessage(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Message</h1><span class="muted text-sm">` + html.EscapeString(user+"@"+a.cfgDomain()) + ` · ` + html.EscapeString(folder) + `</span></div>`)
	body.WriteString(vayuosNav("mailbox"))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || user == "" || id == "" {
		body.WriteString(`<div class="empty-state">Message not available. <a href="/os/vayuos/mail/inbox">Back to Mailbox</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	raw, err := a.vayuMail.ReadFolderMessage(user, folder, id)
	if err != nil {
		body.WriteString(`<div class="empty-state">Could not read message: ` + html.EscapeString(err.Error()) + ` <a href="/os/vayuos/mail/inbox?user=` + qparam(user) + `">Back</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	back := "/os/vayuos/mail/inbox?user=" + qparam(user) + "&folder=" + qparam(folder)
	// Reply / Forward open the composer pre-filled from this message (server-side).
	q := "user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(id)
	replyLink := "/os/vayuos/mail/compose?reply=1&" + q
	forwardLink := "/os/vayuos/mail/compose?forward=1&" + q
	// Action buttons (POST via admin-os-mail.js, CSRF-protected).
	actions := `<div class="vm-actions" data-mail-actions data-user="` + html.EscapeString(user) + `" data-folder="` + html.EscapeString(folder) + `" data-id="` + html.EscapeString(id) + `">`
	actions += `<a class="btn btn--primary" href="` + replyLink + `">Reply</a>`
	actions += `<a class="btn" href="` + forwardLink + `">Forward</a>`
	if !strings.EqualFold(folder, "Junk") {
		actions += `<button class="btn" data-mail-move="Junk">Mark as Junk</button>`
	}
	if !strings.EqualFold(folder, "Archive") {
		actions += `<button class="btn" data-mail-move="Archive">Archive</button>`
	}
	if !strings.EqualFold(folder, "Trash") {
		actions += `<button class="btn" data-mail-move="Trash">Move to Trash</button>`
	} else {
		actions += `<button class="btn" data-mail-move="Inbox">Restore to Inbox</button>`
	}
	actions += `<button class="btn btn--danger" data-mail-delete>Delete permanently</button></div>`
	body.WriteString(`<div class="card"><div class="card-title"><a href="` + back + `">← Back to ` + html.EscapeString(folder) + `</a></div>` + actions +
		`<pre class="vm-pre">` + html.EscapeString(string(raw)) + `</pre></div>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
}

// cfgDomain is a small helper for templates.
func (a *App) cfgDomain() string {
	if a.vayuMail != nil {
		return a.vayuMail.Config().Domain
	}
	return ""
}

// handleVayuOSSent lists recent outbound messages from the delivery queue.
func (a *App) handleVayuOSSent(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Outbox</h1><span class="muted text-sm">Outbound delivery queue</span></div>`)
	body.WriteString(vayuosNav("outbox"))
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
		badge := `<span class="badge badge--ok">` + html.EscapeString(s.State) + `</span>`
		if s.State == "failed" {
			badge = `<span class="badge badge--warn">failed</span>`
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
