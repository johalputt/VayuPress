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
	a.vayuMail = vmail.NewEngine(&mailCfg, &vayuMailBridge{app: a}, dbpkg.DB)

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
	body := `<div class="page-header"><h1>VayuOS</h1>
<span class="muted text-sm">Complete digital sovereignty in one binary — Publishing · Mail · PGP</span></div>
<div class="grid grid-3">
  <div class="card"><div class="card-title">Privacy (VayuPGP)</div><p class="muted">End-to-end PGP, keys encrypted at rest, WKD published.</p><a class="btn" href="/os/vayuos/pgp">Manage keys</a></div>
  <div class="card"><div class="card-title">Sovereignty (VayuMail)</div><p class="muted">DKIM-signed outbound mail, direct-to-MX, retry queue.</p><a class="btn" href="/os/vayuos/mail">Mail &amp; DNS</a></div>
  <div class="card"><div class="card-title">Security updates</div><p class="muted">Track upstream PGP/crypto security releases.</p><a class="btn" href="/os/vayuos/security">Updates</a></div>
</div>
<div class="card"><div class="card-title">Subsystem health</div>
<div class="table-wrap"><table class="table"><thead><tr><th>Component</th><th>Status</th><th>Detail</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>`
	writeOSHTML(w, adminOSLayout(nonce, "VayuOS", "vayuos", cfg, htmpl.HTML(body)))
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
<span class="muted text-sm">Ed25519 + Curve25519 · private keys AES-256-GCM encrypted at rest · published via WKD</span></div>
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

func itoaSafe(n int) string { return fmt.Sprintf("%d", n) }
