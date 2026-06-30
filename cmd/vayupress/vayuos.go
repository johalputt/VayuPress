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

func (b *vayuMailBridge) IsLocalRecipient(emailAddr string) bool {
	if b.app.vayuMail == nil {
		return false
	}
	domain := b.app.vayuMail.Config().Domain
	if domain == "" {
		return false
	}
	at := strings.LastIndex(emailAddr, "@")
	if at < 0 || !strings.EqualFold(strings.TrimSpace(emailAddr[at+1:]), domain) {
		return false
	}
	// 1) CMS users (full VayuPress accounts).
	if _, err := b.GetUserByEmail(emailAddr); err == nil {
		return true
	}
	// 2) Admin-managed mail-only accounts (existence regardless of active state,
	// so disabled mailboxes still receive mail rather than bouncing out).
	if b.app.vayuMail.Accounts() != nil {
		if role := b.app.vayuMail.Accounts().RoleFor(context.Background(), emailAddr); role != "" {
			return true
		}
	}
	return false
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

	plain, err := a.vayuPGP.DecryptForEmail([]byte(armored), accountEmail)
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
	// Inbound receive side is enabled by default so a configured domain can
	// receive external mail. Run outbound-only with VAYUOS_MAIL_INBOUND=off.
	// Binding the mail ports is best-effort inside the engine (a failed bind is
	// surfaced but never blocks outbound/local delivery).
	if strings.EqualFold(config.EnvOr("VAYUOS_MAIL_INBOUND", "on"), "off") {
		mailCfg.InboundEnabled = false
	} else {
		mailCfg.InboundEnabled = true
		mailCfg.SMTPListen = config.EnvOr("VAYUOS_MAIL_SMTP_LISTEN", ":25")
		mailCfg.IMAPListen = config.EnvOr("VAYUOS_MAIL_IMAP_LISTEN", ":143")
		mailCfg.SubmissionListen = config.EnvOr("VAYUOS_MAIL_SUBMISSION_LISTEN", ":587")
		mailCfg.IMAPSListen = config.EnvOr("VAYUOS_MAIL_IMAPS_LISTEN", ":993")
		mailCfg.POP3Listen = config.EnvOr("VAYUOS_MAIL_POP3_LISTEN", ":110")
		mailCfg.POP3SListen = config.EnvOr("VAYUOS_MAIL_POP3S_LISTEN", ":995")
		// Optional CA-signed cert (e.g. Let's Encrypt). When unset, VayuMail
		// generates an in-memory self-signed cert so STARTTLS still works.
		mailCfg.TLSCertFile = config.EnvOr("VAYUOS_MAIL_TLS_CERT", "")
		mailCfg.TLSKeyFile = config.EnvOr("VAYUOS_MAIL_TLS_KEY", "")
		// Native ACME (Let's Encrypt) auto-provisioning. When no static cert is
		// set and VAYUOS_MAIL_TLS_ACME=on, VayuMail obtains and auto-renews a
		// trusted certificate for mail.<domain> itself — no certbot, no shell
		// script — so mobile mail apps (the Gmail app, Apple Mail) connect. The
		// HTTP-01 challenge is answered on VAYUOS_MAIL_ACME_HTTP_ADDR (default
		// :80); on a bare VPS this just works.
		if strings.EqualFold(config.EnvOr("VAYUOS_MAIL_TLS_ACME", "off"), "on") {
			mailCfg.ACMEEnabled = true
			mailCfg.ACMEEmail = config.EnvOr("VAYUOS_MAIL_ACME_EMAIL", "")
			mailCfg.ACMECacheDir = config.EnvOr("VAYUOS_MAIL_ACME_CACHE", "")
			mailCfg.ACMEHTTPAddr = config.EnvOr("VAYUOS_MAIL_ACME_HTTP_ADDR", ":80")
			mailCfg.ACMEDirectoryURL = config.EnvOr("VAYUOS_MAIL_ACME_DIRECTORY", "")
			if extra := config.EnvOr("VAYUOS_MAIL_ACME_HOSTS", ""); extra != "" {
				for _, h := range strings.Split(extra, ",") {
					if h = strings.TrimSpace(h); h != "" {
						mailCfg.ACMEExtraHosts = append(mailCfg.ACMEExtraHosts, h)
					}
				}
			}
		}
	}
	// Optional outbound smarthost relay. Sovereign direct-to-MX stays the
	// default; setting VAYUOS_MAIL_RELAY_HOST routes outbound through an
	// authenticated relay whose IP reputation carries deliverability, while
	// inbound, IMAP, local delivery and DKIM signing remain self-hosted.
	if rh := config.EnvOr("VAYUOS_MAIL_RELAY_HOST", ""); rh != "" {
		mailCfg.RelayHost = rh
		mailCfg.RelayPort = config.GetEnvAsInt("VAYUOS_MAIL_RELAY_PORT", 587)
		mailCfg.RelayUsername = config.EnvOr("VAYUOS_MAIL_RELAY_USERNAME", "")
		mailCfg.RelayPassword = config.EnvOr("VAYUOS_MAIL_RELAY_PASSWORD", "")
		// TLS before AUTH is required by default; opt out only for a trusted
		// relay on a private network.
		mailCfg.RelayRequireTLS = !strings.EqualFold(config.EnvOr("VAYUOS_MAIL_RELAY_TLS", "on"), "off")
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

	// Surface a loud, actionable warning at startup when the inbound mail
	// listeners could not bind. Otherwise the only symptom is a silent "could
	// not open connection to server" in the operator's mail app: the privileged
	// mail ports (25/110/143/587/993/995) require CAP_NET_BIND_SERVICE for the
	// non-root service, plus an open firewall and a mail.<domain> DNS record.
	if a.vayuMail != nil && a.vayuMail.Config().Enabled && a.vayuMail.Config().InboundEnabled {
		if err := a.vayuMail.InboundError(); err != nil {
			logging.LogError("vayuos",
				"VayuMail inbound listeners did not all bind — mail clients may fail to connect (run deploy/vayumail-setup.sh)",
				err.Error()+inboundHint(err))
		} else if a.vayuMail.InboundActive() {
			logging.LogInfo("vayuos",
				"VayuMail inbound listening — also ensure the host/cloud firewall allows TCP 25/143/993/110/995/587 and mail."+a.vayuMail.Config().Domain+" resolves to this server")
		}
		// A reachable port with an untrusted (self-signed) certificate is the
		// most common reason a mobile mail app reports "Couldn't open connection
		// to server": the TCP/TLS layer connects, but the client rejects the
		// certificate. Make this explicit and actionable at startup.
		if a.vayuMail.TLSActive() && !a.vayuMail.TLSTrusted() {
			logging.LogError("vayuos",
				"VayuMail is serving a SELF-SIGNED certificate — mobile mail apps (Gmail, Apple Mail) will refuse to connect",
				"enable automatic certificates with VAYUOS_MAIL_TLS_ACME=on, run deploy/vayumail-setup.sh, or set VAYUOS_MAIL_TLS_CERT/VAYUOS_MAIL_TLS_KEY, then restart ("+a.vayuMail.TLSNote()+")")
		}
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
		if a.vayuMail.Config().InboundEnabled {
			if a.vayuMail.InboundActive() {
				extras := []string{}
				if a.vayuMail.TLSActive() {
					extras = append(extras, "STARTTLS")
				}
				if a.vayuMail.SubmissionActive() {
					extras = append(extras, "submission:587")
				}
				if a.vayuMail.IMAPSActive() {
					extras = append(extras, "IMAPS:993")
				}
				if a.vayuMail.POP3Active() {
					extras = append(extras, "POP3:110")
				}
				if a.vayuMail.POP3SActive() {
					extras = append(extras, "POP3S:995")
				}
				msg := "outbound + DKIM active; inbound SMTP/IMAP listening"
				if len(extras) > 0 {
					msg += " + " + strings.Join(extras, ", ")
				}
				if err := a.vayuMail.InboundError(); err != nil {
					msg += "; note: " + err.Error() + inboundHint(err)
				}
				return true, msg
			}
			if err := a.vayuMail.InboundError(); err != nil {
				return true, "outbound + DKIM active; inbound listener unavailable: " + err.Error() + inboundHint(err)
			}
		}
		return true, "outbound queue + DKIM active (inbound disabled)"
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

	// Backfill PGP keypairs for accounts that pre-date auto-keygen (CMS users
	// created before VayuOS, and admin-managed mail accounts which previously
	// got a mailbox but no key). Runs in the background so boot is never blocked;
	// EnsureKeypair is idempotent so this is a no-op once every account has a key.
	go a.backfillPGPKeys(context.Background())
}

// backfillPGPKeys ensures every known local identity (CMS user + admin-managed
// mail account) has a PGP keypair, so the VayuPGP panel lists them and their
// inbound mail can be encrypted at rest / transparently decrypted on read.
func (a *App) backfillPGPKeys(ctx context.Context) {
	if a.vayuPGP == nil {
		return
	}
	// CMS users.
	if a.userStore != nil {
		if users, err := a.userStore.List(ctx); err == nil {
			for _, u := range users {
				if u.Email == "" {
					continue
				}
				if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: u.ID, Name: u.Name, Email: u.Email}); err != nil {
					logging.LogError("vayuos", "PGP key backfill failed for "+u.Email, err.Error())
				}
			}
		}
	}
	// Admin-managed mail accounts (keyed by their email address).
	if a.vayuMail != nil && a.vayuMail.Accounts() != nil {
		if accts, err := a.vayuMail.Accounts().List(ctx); err == nil {
			for _, ac := range accts {
				if ac.Email == "" {
					continue
				}
				if _, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: ac.Email, Name: ac.FullName, Email: ac.Email}); err != nil {
					logging.LogError("vayuos", "PGP key backfill failed for "+ac.Email, err.Error())
				}
			}
		}
	}
}

// inboundHint translates a listener bind failure into an actionable next step
// for the operator, so the pitfalls of self-hosting (privileged ports, a
// pre-installed MTA) are explained right in the panel instead of being silent.
func inboundHint(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied"):
		return " — the process lacks privilege to bind ports below 1024. Grant CAP_NET_BIND_SERVICE (see deploy/vayupress.service), or set VAYUOS_MAIL_SMTP_LISTEN=:2525 / VAYUOS_MAIL_IMAP_LISTEN=:1143 and redirect 25→2525, 143→1143."
	case strings.Contains(msg, "address already in use"), strings.Contains(msg, "in use"):
		return " — another mail server already holds the port. Stop it (e.g. `sudo systemctl disable --now postfix`) and restart, or point VAYUOS_MAIL_SMTP_LISTEN/IMAP_LISTEN at free ports."
	default:
		return ""
	}
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
	admin := a.isAdminRequest(r)
	snap := a.vayuHealth.Snapshot()
	var rows strings.Builder
	for _, c := range snap.Components {
		badge := `<span class="badge badge--ok">OK</span>`
		if !c.OK {
			badge = `<span class="badge badge--warn">DEGRADED</span>`
		}
		rows.WriteString(`<tr><td>` + html.EscapeString(c.Name) + `</td><td>` + badge + `</td><td class="muted">` + html.EscapeString(c.Detail) + `</td></tr>`)
	}
	// Infrastructure cards (PGP keys, DKIM/DNS, security updates) and the
	// subsystem-health table expose operational detail the four non-admin roles
	// do not need, so they are administrator-only.
	infraCards, healthCard := "", ""
	if admin {
		infraCards = `
  <div class="card"><div class="card-title">Privacy (VayuPGP)</div><p class="muted">End-to-end PGP, keys encrypted at rest, WKD published.</p><a class="btn" href="/os/vayuos/pgp">Manage keys</a></div>
  <div class="card"><div class="card-title">Sovereignty (VayuMail)</div><p class="muted">DKIM-signed outbound mail, direct-to-MX, DNS health.</p><a class="btn" href="/os/vayuos/mail">Mail &amp; DNS</a></div>
  <div class="card"><div class="card-title">Security updates</div><p class="muted">Track upstream PGP/crypto security releases.</p><a class="btn" href="/os/vayuos/security">Updates</a></div>`
		healthCard = `
<div class="card"><div class="card-title">Subsystem health</div>
<div class="table-wrap"><table class="table"><thead><tr><th>Component</th><th>Status</th><th>Detail</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>`
	}
	body := `<div class="page-header"><h1>VayuMail</h1>
<span class="muted text-sm">Your mailboxes — read, compose and connect mail apps</span></div>` + vayuosNav("overview", admin) + `
<div class="grid grid-3">
  <div class="card"><div class="card-title">Inbox</div><p class="muted">Read mail received into your mailboxes (Maildir).</p><a class="btn" href="/os/vayuos/mail/inbox">Open inbox</a></div>
  <div class="card"><div class="card-title">Sent</div><p class="muted">Outbound delivery queue with per-message status.</p><a class="btn" href="/os/vayuos/mail/sent">View sent</a></div>
  <div class="card"><div class="card-title">Connect a mail app</div><p class="muted">IMAP/POP3/SMTP settings for the Gmail app, Apple Mail and more.</p><a class="btn" href="/os/vayuos/mail/connect">Connect</a></div>` + infraCards + `
</div>` + healthCard
	writeOSHTML(w, adminOSLayout(nonce, "VayuMail", "vayuos", cfg, htmpl.HTML(body)))
}

func (a *App) handleVayuOSPGP(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// PGP key material is administrator-only — the four non-admin roles never
	// see it (redirected to their inbox).
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os/vayuos/mail/inbox")
		return
	}
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
<span class="muted text-sm">Ed25519 + Curve25519 · private keys AES-256-GCM encrypted at rest · published via WKD</span></div>` + vayuosNav("pgp", true) + `
<div class="card"><div class="card-title">Keypairs</div>
<div class="table-wrap"><table class="table"><thead><tr><th>Email</th><th>Fingerprint</th><th>State</th><th>Expires</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>
<div class="card"><div class="card-title">Web Key Directory</div><p class="muted">External clients discover these keys at <code>/.well-known/openpgpkey/</code> (advanced method).</p></div>`
	writeOSHTML(w, adminOSLayout(nonce, "VayuPGP", "vayuos", cfg, htmpl.HTML(body)))
}

func (a *App) handleVayuOSMail(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// The DKIM/SPF/DMARC records, live DNS health and deliverability self-check
	// are infrastructure detail — administrator-only.
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os/vayuos/mail/inbox")
		return
	}
	mc := a.vayuMail.Config()
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>VayuMail</h1><span class="muted text-sm">Native outbound mail sovereignty</span></div>`)
	body.WriteString(vayuosNav("mail", true))
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
	// Deliverability self-check — the things that most often send mail to spam.
	body.WriteString(`<div class="card"><div class="card-title">Deliverability self-check</div><p class="muted text-sm">Why mail may be marked as spam. Fix any ✗ rows below.</p><div class="table-wrap"><table class="table"><thead><tr><th>Check</th><th>Status</th><th>Detail</th></tr></thead><tbody>`)
	for _, rh := range a.vayuMail.Deliverability(r.Context()) {
		badge := `<span class="badge badge--ok">ok</span>`
		if !rh.OK {
			badge = `<span class="badge badge--warn">action needed</span>`
		}
		body.WriteString(`<tr><td>` + html.EscapeString(rh.Type) + `</td><td>` + badge + `</td><td class="muted text-sm vm-break">` + html.EscapeString(rh.Message) + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	writeOSHTML(w, adminOSLayout(nonce, "VayuMail", "vayuos", cfg, htmpl.HTML(body.String())))
}

func (a *App) handleVayuOSSecurity(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// The dependency security-update watcher is administrator-only.
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os/vayuos/mail/inbox")
		return
	}
	// Prefer the most recent live report (e.g. after a "Check now"); otherwise do
	// the env-gated check (no network when disabled).
	rep := a.vayuSec.Last()
	if rep == nil {
		rep, _ = a.vayuSec.Check(r.Context())
	}

	// CSRF token so the inline "Check now" control can POST.
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}

	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Security updates</h1>
  <div class="page-actions">
    <span class="muted text-sm">Upstream PGP &amp; crypto dependency monitoring</span>
    <button type="button" class="btn btn--primary btn--sm" data-sec-check>Check now</button>
    <span class="text-xs muted" data-sec-status role="status" aria-live="polite"></span>
  </div>
</div>`)
	body.WriteString(vayuosNav("security", true))
	if !rep.Enabled {
		body.WriteString(`<div class="empty-state">Automatic background checks are off by default (privacy first) — VayuPress never reaches out on its own. Click <strong>Check now</strong> above for a one-time, on-demand check (it fetches only public release metadata from GitHub and sends nothing about your site). To run checks automatically, set <code>VAYUOS_SECURITY_UPDATES=on</code> and restart.</div>`)
	} else if rep.UpdatesAvailable > 0 {
		body.WriteString(`<div class="warn-box">` + itoaSafe(rep.UpdatesAvailable) + ` security-relevant update(s) available. ` + html.EscapeString(rep.UpgradeHint) + `</div>`)
	}
	body.WriteString(buildComponentTable(rep.Components))
	body.WriteString(`<script nonce="` + nonce + `">
(function(){'use strict';
function csrf(){var m=document.cookie.match(/(?:^|;\s*)vp_csrf=([^;]+)/);return m?decodeURIComponent(m[1]):'';}
var b=document.querySelector('[data-sec-check]'),s=document.querySelector('[data-sec-status]');
if(b)b.addEventListener('click',function(){
  b.disabled=true;if(s)s.textContent='Checking upstream releases…';
  fetch('/os/api/vayuos/security/check',{method:'POST',headers:{'X-CSRF-Token':csrf()}})
    .then(function(r){if(r.ok){location.reload();}else{b.disabled=false;if(s)s.textContent='Check failed.';}})
    .catch(function(e){b.disabled=false;if(s)s.textContent='Network error: '+e;});
});
})();
</script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Security updates", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSSecurityCheck performs an on-demand upstream security check
// (admin-initiated; the click is the consent), even when automatic checking is
// disabled. Returns the report as JSON; the page reloads to show it.
func (a *App) handleVayuOSSecurityCheck(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	rep, err := a.vayuSec.CheckNow(r.Context())
	if err != nil {
		writeAPIError(w, r, http.StatusBadGateway, "check-failed", err.Error(), "")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]interface{}{"updatesAvailable": rep.UpdatesAvailable})
}

func buildComponentTable(comps []secwatch.Component) string {
	var sb strings.Builder
	sb.WriteString(`<div class="card"><div class="card-title">Tracked dependencies</div><div class="table-wrap"><table class="table"><thead><tr><th>Component</th><th>Current</th><th>Latest</th><th>Status</th></tr></thead><tbody>`)
	for _, c := range comps {
		latest := c.Latest
		var status string
		switch {
		case c.UpdateAvailable:
			status = `<span class="badge badge--warn">update available</span>`
		case latest == "":
			// No upstream version known — the watcher is disabled or the check
			// failed. Don't claim "up to date" when we haven't actually compared.
			status = `<span class="muted text-sm">not checked</span>`
		default:
			status = `<span class="badge badge--ok">up to date</span>`
		}
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

// vayuosNav renders the VayuOS sub-navigation shown on every VayuOS page. The
// admin flag gates the tabs that expose infrastructure detail — PGP keys, the
// DNS records, the security-update watcher and account management — so the four
// non-admin roles (editor, author, reviewer, mailbox) only ever see the mail
// surface they actually use (Overview, Compose, Mailbox, Connect, Outbox).
func vayuosNav(active string, admin bool) string {
	type navTab struct {
		key, label, href string
		adminOnly        bool
	}
	items := []navTab{
		{"overview", "Overview", "/os/vayuos", false},
		{"compose", "Compose", "/os/vayuos/mail/compose", false},
		{"mailbox", "Mailbox", "/os/vayuos/mail/inbox", false},
		{"accounts", "Accounts", "/os/vayuos/mail/accounts", true},
		{"connect", "Connect", "/os/vayuos/mail/connect", false},
		{"outbox", "Outbox", "/os/vayuos/mail/sent", false},
		{"pgp", "PGP Keys", "/os/vayuos/pgp", true},
		{"mail", "DNS", "/os/vayuos/mail", true},
		{"security", "Security", "/os/vayuos/security", true},
	}
	var sb strings.Builder
	sb.WriteString(`<div class="vmtabs">`)
	for _, it := range items {
		if it.adminOnly && !admin {
			continue
		}
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
	body.WriteString(vayuosNav("mailbox", a.isAdminRequest(r)))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to a real domain to provision mailboxes. The inbound SMTP/IMAP listener runs by default once a domain is set (disable with <code>VAYUOS_MAIL_INBOUND=off</code>); receiving external mail also needs port 25 reachable and MX/A DNS records pointing at this host.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}
	// Non-admin staff may only operate their own assigned mailbox — never browse
	// or target another mailbox via ?user=.
	if !a.isAdminRequest(r) {
		local, _ := a.ownMailbox(r)
		if local == "" {
			body.WriteString(`<div class="empty-state">No mailbox has been assigned to your account yet. Ask an administrator to assign you an email address under <strong>Members → Team &amp; roles</strong>.</div>`)
			writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
			return
		}
		user = local
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
	// Storage usage bar: how much of the mailbox quota is in use (0 = unlimited).
	// The fill width/level is applied by admin-os-mail.js from data-* attributes
	// (CSSOM), since the strict admin CSP (style-src 'self') blocks inline styles.
	{
		email := user + "@" + domain
		used := a.vayuMail.MailboxUsage(email)
		quota := a.vayuMail.MailboxQuota(email)
		if quota > 0 {
			pct := int(float64(used) / float64(quota) * 100)
			if pct > 100 {
				pct = 100
			}
			level := "ok"
			if pct >= 90 {
				level = "full"
			} else if pct >= 75 {
				level = "warn"
			}
			body.WriteString(`<div class="vm-quota"><div class="vm-quota-meta text-sm muted">Storage: ` +
				html.EscapeString(humanBytes(used)) + ` of ` + html.EscapeString(humanBytes(quota)) + ` used (` + itoaSafe(pct) + `%)</div>` +
				`<div class="vm-quota-track"><div class="vm-quota-fill vm-quota-fill--` + level + `" data-quota-pct="` + itoaSafe(pct) + `"></div></div>`)
			if pct >= 100 {
				body.WriteString(`<div class="vm-quota-full text-sm">⚠ Your mailbox is full — incoming mail may be rejected and you can't send until you free space.</div>`)
			}
			body.WriteString(`</div>`)
		} else {
			body.WriteString(`<div class="vm-quota text-sm muted">Storage used: ` + html.EscapeString(humanBytes(used)) + ` · quota: unlimited</div>`)
		}
	}
	body.WriteString(`<form class="vm-search" method="get" action="/os/vayuos/mail/search">
  <input type="hidden" name="user" value="` + html.EscapeString(user) + `">
  <input class="input" type="search" name="q" placeholder="Search mail (from, subject, body)…" aria-label="Search mail">
  <button class="btn" type="submit">Search</button>
</form>`)
	body.WriteString(folderTabs(user, folder))
	isDrafts := strings.EqualFold(folder, "Drafts")
	// Bulk action bar — selection drives mark-read / pin / move / delete across
	// many messages at once (wired in admin-os-mail.js). Drafts open in the
	// composer, so bulk acts there are limited to delete.
	if len(msgs) > 0 {
		bar := `<div class="vm-bulk" data-mail-bulk data-user="` + html.EscapeString(user) + `" data-folder="` + html.EscapeString(folder) + `" hidden>`
		bar += `<span class="text-sm muted" data-bulk-count>0 selected</span>`
		if !isDrafts {
			bar += `<button class="btn btn--sm" data-bulk-action="read">Mark read</button>`
			bar += `<button class="btn btn--sm" data-bulk-action="unread">Mark unread</button>`
			bar += `<button class="btn btn--sm" data-bulk-action="pin">📌 Pin</button>`
			bar += `<span class="vm-move"><select class="input input--sm" data-bulk-move aria-label="Move selected to folder"><option value="">Move to…</option>`
			for _, f := range vmail.StandardFolders {
				if strings.EqualFold(f, folder) {
					continue
				}
				bar += `<option value="` + html.EscapeString(f) + `">` + html.EscapeString(f) + `</option>`
			}
			bar += `</select></span>`
		}
		bar += `<button class="btn btn--sm btn--danger" data-bulk-action="delete">Delete</button>`
		bar += `</div>`
		body.WriteString(bar)
	}
	body.WriteString(`<div class="table-wrap"><table class="table" data-mail-list data-user="` + html.EscapeString(user) + `" data-folder="` + html.EscapeString(folder) + `"><thead><tr><th class="vm-check"><input type="checkbox" data-mail-check-all aria-label="Select all"></th><th></th><th>From</th><th>Subject</th><th>Date</th><th></th></tr></thead><tbody>`)
	if len(msgs) == 0 {
		body.WriteString(`<tr><td colspan="6" class="muted">No messages in ` + html.EscapeString(folder) + `.</td></tr>`)
	}
	for _, m := range msgs {
		subj := m.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		who := m.From
		if strings.EqualFold(folder, "Sent") || isDrafts {
			who = "→ " + m.To
		}
		// Drafts reopen in the composer; everything else opens the reader view.
		link := "/os/vayuos/mail/message?user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(m.ID)
		if isDrafts {
			link = "/os/vayuos/mail/compose?draft=1&user=" + qparam(user) + "&id=" + qparam(m.ID)
		}
		seen := ""
		if !m.Seen && !strings.EqualFold(folder, "Sent") && !isDrafts {
			seen = ` <span class="badge badge--ok">new</span>`
		}
		// Pin toggle (Maildir Flagged) — a filled marker when pinned.
		pinVal, pinIcon := "1", "📌"
		if m.Flagged {
			pinVal, pinIcon = "0", "📍"
		}
		pin := `<button class="btn btn--sm btn--ghost" data-mail-pin-row="` + pinVal + `" data-user="` + html.EscapeString(user) + `" data-folder="` + html.EscapeString(folder) + `" data-id="` + html.EscapeString(m.ID) + `" aria-label="Pin">` + pinIcon + `</button>`
		// Read/unread toggle (only meaningful for received folders).
		tick := ""
		if !strings.EqualFold(folder, "Sent") && !isDrafts {
			mark, label := "read", "Mark read"
			if m.Seen {
				mark, label = "unread", "✓ read"
			}
			tick = `<button class="btn btn--sm" data-mail-mark-row="` + mark + `" data-user="` + html.EscapeString(user) + `" data-folder="` + html.EscapeString(folder) + `" data-id="` + html.EscapeString(m.ID) + `">` + label + `</button>`
		}
		check := `<input type="checkbox" class="vm-check-row" data-mail-check value="` + html.EscapeString(m.ID) + `" aria-label="Select message">`
		body.WriteString(`<tr><td class="vm-check">` + check + `</td><td class="text-sm">` + pin + `</td><td class="text-sm">` + html.EscapeString(who) + `</td><td><a href="` + link + `">` + html.EscapeString(subj) + `</a>` + seen + `</td><td class="muted text-sm">` + m.Date.Format("2006-01-02 15:04") + `</td><td class="text-sm">` + tick + `</td></tr>`)
	}
	body.WriteString(`</tbody></table></div></div>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
}

// handleVayuOSSearch runs a bounded full-text search across a mailbox's folders.
func (a *App) handleVayuOSSearch(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	if !a.isAdminRequest(r) {
		// Non-admins may only search their own assigned mailbox.
		user, _ = a.ownMailbox(r)
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Search mail</h1><span class="muted text-sm">` + html.EscapeString(user+"@"+a.cfgDomain()) + `</span></div>`)
	body.WriteString(vayuosNav("mailbox", a.isAdminRequest(r)))
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
	if !a.isAdminRequest(r) {
		// Non-admins may only read messages in their own assigned mailbox.
		user, _ = a.ownMailbox(r)
	}
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" {
		folder = "Inbox"
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Message</h1><span class="muted text-sm">` + html.EscapeString(user+"@"+a.cfgDomain()) + ` · ` + html.EscapeString(folder) + `</span></div>`)
	body.WriteString(vayuosNav("mailbox", a.isAdminRequest(r)))
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
	// Opening a message marks it read (Maildir Seen), like every mail client.
	// Drafts/Sent are authored, not received, so they are left untouched. The
	// rename can change the id, so we use the returned one for the actions below.
	if !strings.EqualFold(folder, "Drafts") && !strings.EqualFold(folder, "Sent") {
		if nid, merr := a.vayuMail.MarkRead(user, folder, id); merr == nil && nid != "" {
			id = nid
		}
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
	actions += `<button class="btn" data-mail-mark="read">✓ Mark read</button>`
	actions += `<button class="btn" data-mail-mark="unread">Mark unread</button>`
	// Pin / unpin (Maildir Flagged). The current pin state is read from disk.
	pinned := false
	if msgs, lerr := a.vayuMail.ListFolder(user, folder); lerr == nil {
		for _, mm := range msgs {
			if mm.ID == id {
				pinned = mm.Flagged
				break
			}
		}
	}
	if pinned {
		actions += `<button class="btn" data-mail-pin="0">📌 Unpin</button>`
	} else {
		actions += `<button class="btn" data-mail-pin="1">📌 Pin</button>`
	}
	if !strings.EqualFold(folder, "Junk") {
		actions += `<button class="btn" data-mail-move="Junk">Mark as Junk</button>`
	}
	if !strings.EqualFold(folder, "Trash") {
		actions += `<button class="btn" data-mail-move="Trash">Move to Trash</button>`
	} else {
		actions += `<button class="btn" data-mail-move="Inbox">Restore to Inbox</button>`
	}
	// General "Move to folder" picker (covers every standard folder).
	actions += `<span class="vm-move"><select class="input input--sm" data-mail-move-select aria-label="Move to folder"><option value="">Move to…</option>`
	for _, f := range vmail.StandardFolders {
		if strings.EqualFold(f, folder) {
			continue
		}
		actions += `<option value="` + html.EscapeString(f) + `">` + html.EscapeString(f) + `</option>`
	}
	actions += `</select></span>`
	actions += `<button class="btn btn--danger" data-mail-delete>Delete permanently</button></div>`
	// Clean reader view: decoded headers + body, with a raw-source toggle.
	pm := vmail.ParseMessage(raw)
	subj := strings.TrimSpace(pm.Subject)
	if subj == "" {
		subj = "(no subject)"
	}
	var card strings.Builder
	card.WriteString(`<div class="card"><div class="card-title"><a href="` + back + `">← Back to ` + html.EscapeString(folder) + `</a></div>`)
	card.WriteString(actions)
	// Header summary (long technical headers are hidden behind "raw source").
	card.WriteString(`<div class="vm-msg-head"><div class="card-title">` + html.EscapeString(subj) + `</div>`)
	hdrRow := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		card.WriteString(`<div class="muted text-sm"><strong>` + label + `:</strong> ` + html.EscapeString(value) + `</div>`)
	}
	hdrRow("From", pm.From)
	hdrRow("To", pm.To)
	hdrRow("Cc", pm.Cc)
	hdrRow("Date", pm.Date)
	card.WriteString(`</div>`)
	// Body: prefer decoded text/plain; else sanitised HTML; else raw fallback.
	card.WriteString(`<div class="vm-msg-body">`)
	switch {
	case strings.TrimSpace(pm.Text) != "":
		card.WriteString(`<pre class="vm-pre">` + html.EscapeString(pm.Text) + `</pre>`)
	case strings.TrimSpace(pm.HTML) != "":
		card.WriteString(`<div class="vm-html">` + mailHTMLPolicy.Sanitize(pm.HTML) + `</div>`)
	default:
		card.WriteString(`<pre class="vm-pre">` + html.EscapeString(string(raw)) + `</pre>`)
	}
	card.WriteString(`</div>`)
	// Raw source, hidden by default, toggled by admin-os-mail.js (CSP-safe).
	card.WriteString(`<div class="vm-rawwrap"><button class="btn" type="button" data-mail-raw-toggle>View raw source</button>`)
	card.WriteString(`<pre class="vm-pre vm-raw" data-mail-raw hidden>` + html.EscapeString(string(raw)) + `</pre></div>`)
	card.WriteString(`</div>`)
	body.WriteString(card.String())
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
	body.WriteString(vayuosNav("outbox", a.isAdminRequest(r)))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to activate outbound delivery.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Sent", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	// The outbound delivery queue is server-wide; non-admins see their own sent
	// mail in their mailbox's Sent folder instead.
	if !a.isAdminRequest(r) {
		body.WriteString(`<div class="empty-state">Your sent messages are in your mailbox under <a href="/os/vayuos/mail/inbox?folder=Sent">Mailbox → Sent</a>. The server-wide delivery queue is visible to administrators only.</div>`)
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
