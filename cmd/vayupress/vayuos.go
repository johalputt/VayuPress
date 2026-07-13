// vayuos.go — VayuOS control layer wiring.
//
// This file boots the VayuOS subsystems (VayuPGP privacy layer, VayuMail
// sovereignty layer, security-update watcher), wires the event bus so that
// account creation auto-provisions PGP keys and mailboxes, and serves the
// VayuOS panel pages plus the public WKD key directory. All panel routes are
// registered under the existing session-protected admin console.
package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	htmpl "html/template"
	"net/http"
	stdmail "net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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
	vtalk "github.com/johalputt/vayupress/internal/vayuos/vayutalk"
)

// ── Mail bridge ──────────────────────────────────────────────────────────────

// vayuMailBridge connects VayuMail to VayuPress core (auth, transactional mail,
// PGP). It never stores plaintext passwords or private keys.
type vayuMailBridge struct{ app *App }

// mailAuthThrottle slows repeated failed mailbox logins (IMAP/SMTP/POP3) to
// defeat online password guessing, without ever locking a real user out — see
// vmail.AuthThrottle. Shared across all three protocol servers.
var mailAuthThrottle = vmail.NewAuthThrottle()

func (b *vayuMailBridge) AuthUser(username, password string) (bool, error) {
	ctx := context.Background()
	domain := b.app.vayuMail.Config().Domain
	addr := username
	if !strings.Contains(addr, "@") {
		addr = username + "@" + domain
	}
	// Brute-force throttle: an account under attack accrues a short, decaying
	// delay before each attempt, so guessing is slowed to a crawl while a
	// correct password still authenticates (just a little slower). Cleared on
	// success below.
	if d := mailAuthThrottle.Delay(addr); d > 0 {
		time.Sleep(d)
	}
	ok := b.verifyCredential(ctx, addr, password)
	if ok {
		mailAuthThrottle.Success(addr)
	} else {
		mailAuthThrottle.Fail(addr)
	}
	return ok, nil
}

// verifyCredential checks a mailbox password against every accepted credential
// in MAIL-SYNC scope: it guards everything that hands out mail content — the
// IMAP/POP3/submission listeners (via AuthUser) and the private-key sync
// endpoint. When the mailbox requires device approval (ADR-0129, the default),
// this scope rejects the raw CMS/mailbox password and accepts only an APPROVED
// device credential, so a stolen password alone can never sync mail.
func (b *vayuMailBridge) verifyCredential(ctx context.Context, addr, password string) bool {
	return b.verifyCredentialScoped(ctx, addr, password, false)
}

// verifyCredentialWeb checks the same credential set in WEB-BOOTSTRAP scope:
// the member HTTP endpoints (login, device registration) that a new device
// needs the raw password for BEFORE it can hold an approved credential. The
// raw password is accepted here regardless of the device-approval setting;
// device credentials are still status-checked.
func (b *vayuMailBridge) verifyCredentialWeb(ctx context.Context, addr, password string) bool {
	return b.verifyCredentialScoped(ctx, addr, password, true)
}

// verifyCredentialScoped is the single credential chokepoint behind both
// scopes (CMS account password, mail-account password, and device/app
// passwords), honouring the optional app-password-only 2FA enforcement and
// the per-mailbox device-approval policy.
func (b *vayuMailBridge) verifyCredentialScoped(ctx context.Context, addr, password string, webBootstrap bool) bool {
	// Stored credential rows for this mailbox (device credentials and plain app
	// passwords). Fetched up front so the enforcement decisions can require a
	// working alternative to exist before they ever retire the main password.
	var accts *vmail.AccountStore
	var creds []vmail.AppPasswordCredential
	if b.app.vayuMail != nil && b.app.vayuMail.Accounts() != nil {
		accts = b.app.vayuMail.Accounts()
		creds = accts.AppPasswordCredentials(ctx, addr)
	}

	// Optional "app-password only" 2FA enforcement (Gmail/Outlook model): when
	// enabled for this identity, the plain login/mailbox password stops
	// authenticating IMAP/SMTP/POP3 and the client must use an app password
	// (minting one already requires a fresh 2FA code). IMAP/SMTP/POP3 cannot
	// prompt for a second factor, so the credential IS the enforcement point.
	//
	// This is OFF by default and lockout-proof: it only applies when the
	// operator opts in (VAYUMAIL_2FA_ENFORCE=1) AND the mailbox has 2FA AND at
	// least one app password already exists — so there is always a working
	// credential and a password login can never be silently locked out. Any
	// other case (no opt-in, no 2FA, no app password yet) keeps the password
	// working exactly as before.
	enforce := vayuMail2FAEnforce() && len(creds) > 0 && b.twoFactorEnabled(ctx, addr)

	// Device approval (ADR-0129): in mail-sync scope a mailbox that requires
	// approval never accepts the raw password — only an approved device
	// credential below. Web-bootstrap scope keeps accepting it so a new device
	// can register (and a member can sign in) with just the password.
	requireApproval := accts != nil && accts.RequireDeviceApproval(ctx, addr)

	if !enforce && (webBootstrap || !requireApproval) {
		// 1) CMS users (full VayuPress accounts).
		if b.app.userStore != nil {
			if _, err := b.app.userStore.Authenticate(ctx, addr, password); err == nil {
				return true
			}
		}
		// 2) Admin-managed mail-only accounts (email + password).
		if accts != nil {
			if hash := accts.HashFor(ctx, addr); hash != "" {
				if auth.VerifySecretArgon2id(password, hash) {
					return true
				}
			}
		}
	}

	// 3) Device credentials and app passwords — the required credential when
	// either enforcement is on, a convenience device credential otherwise.
	// Verified last so the main password stays the fast path. Secrets are
	// displayed to the operator in dash-grouped blocks (abcd-efgh-…) but hashed
	// dashless, so the dashless form is tried first; the raw form is kept as a
	// fallback in case an older stored credential contained literal dashes.
	appPw := strings.ReplaceAll(password, "-", "")
	for _, c := range creds {
		if auth.VerifySecretArgon2id(appPw, c.Hash) ||
			(appPw != password && auth.VerifySecretArgon2id(password, c.Hash)) {
			// The presented secret IS this credential — its status decides.
			// Pending and blocked devices are rejected outright (uniform
			// failure); legacy rows migrated to 'approved' keep working.
			if c.Status == vmail.DeviceStatusPending || c.Status == vmail.DeviceStatusBlocked {
				return false
			}
			accts.TouchAppPassword(ctx, c.ID)
			return true
		}
	}
	return false
}

// vayuMail2FAEnforce reports whether the optional "app-password only" 2FA
// enforcement is switched on. It is OFF unless VAYUMAIL_2FA_ENFORCE is a truthy
// value (1/true/yes/on), so a mailbox with 2FA keeps accepting its password
// until the operator deliberately opts in.
func vayuMail2FAEnforce() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VAYUMAIL_2FA_ENFORCE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// twoFactorEnabled reports whether 2FA is active for the identity behind addr,
// checking both the VayuMail mailbox and the CMS account (a mailbox may be
// either). Either one being enabled enforces the app-password-only policy.
func (b *vayuMailBridge) twoFactorEnabled(ctx context.Context, addr string) bool {
	if b.app.vayuMail != nil && b.app.vayuMail.Accounts() != nil {
		if _, enabled := b.app.vayuMail.Accounts().TOTPStatus(ctx, addr); enabled {
			return true
		}
	}
	if b.app.userStore != nil {
		if u, err := b.app.userStore.GetByEmail(ctx, addr); err == nil && u != nil {
			if _, enabled, terr := b.app.userStore.TOTPStatus(ctx, u.ID); terr == nil && enabled {
				return true
			}
		}
	}
	return false
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
	if at < 0 {
		return false
	}
	host := strings.TrimSpace(emailAddr[at+1:])
	// The recipient must be on a domain this install serves — the primary, or a
	// mail_enabled secondary (VayuDomains Stage 3b). Byte-identical (primary-only)
	// when no secondary mail domain is registered.
	if !strings.EqualFold(host, domain) && !b.app.acceptsSecondaryMailDomain(host) {
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

// pgpDecryptForAccount transparently decrypts a PGP message for the account
// that owns the mailbox, when VayuPGP holds that account's private key. It
// handles both inline PGP (the body IS the armored block — what VayuPress
// itself sends) and PGP/MIME (RFC 3156 multipart/encrypted — what the
// VayuMail app and third-party clients send). It is best-effort: on any
// failure it returns the original bytes unchanged so the client always
// receives a readable (if still-encrypted) message.
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
	// PGP/MIME: the armor lives inside a multipart/encrypted structure, so
	// splicing plaintext into the middle of it would produce a corrupt
	// message (an "encrypted" envelope with no ciphertext — clients show
	// raw MIME or loop trying to decrypt). Rebuild the message instead:
	// original headers, decrypted content as the body.
	if isPGPMIME(raw) {
		if out := rebuildDecryptedMessage(raw, plain); out != nil {
			return out
		}
		return raw
	}
	// Inline PGP: the armored block sits directly in a text body; splicing
	// the plaintext in place preserves the (simple) structure.
	return []byte(s[:bi] + string(plain) + s[ei:])
}

// isPGPMIME reports whether the message's top-level Content-Type is the
// RFC 3156 multipart/encrypted structure.
func isPGPMIME(raw []byte) bool {
	msg, err := stdmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(msg.Header.Get("Content-Type")),
		"multipart/encrypted")
}

// rebuildDecryptedMessage reassembles a decrypted PGP/MIME message: the
// original top-level headers minus the multipart/encrypted framing, an
// X-VayuPGP marker (so the 🔒 badge survives decryption), and the decrypted
// payload as the body. When the payload is itself a MIME entity (it starts
// with its own Content-* headers, as RFC 3156 prescribes), its headers
// continue the header block; otherwise it is wrapped as plain text.
// Returns nil when raw has no header/body split (caller falls back to raw).
func rebuildDecryptedMessage(raw, plain []byte) []byte {
	sep, nl := []byte("\r\n\r\n"), "\r\n"
	idx := bytes.Index(raw, sep)
	if idx < 0 {
		sep, nl = []byte("\n\n"), "\n"
		idx = bytes.Index(raw, sep)
	}
	if idx < 0 {
		return nil
	}
	var out []string
	skip := false
	for _, ln := range strings.Split(string(raw[:idx]), nl) {
		// Continuation lines belong to the header decided on above.
		if len(ln) > 0 && (ln[0] == ' ' || ln[0] == '\t') {
			if !skip {
				out = append(out, ln)
			}
			continue
		}
		lower := strings.ToLower(ln)
		skip = strings.HasPrefix(lower, "content-type:") ||
			strings.HasPrefix(lower, "content-transfer-encoding:") ||
			strings.HasPrefix(lower, "x-vayupgp:")
		if !skip {
			out = append(out, ln)
		}
	}
	out = append(out, "X-VayuPGP: encrypted")
	head := strings.Join(out, "\r\n")
	body := strings.TrimLeft(string(plain), "\r\n")
	if strings.HasPrefix(body, "Content-") {
		// The decrypted payload is a full MIME entity: its Content-*
		// headers extend the header block, then its own blank line and
		// body follow.
		return []byte(head + "\r\n" + body)
	}
	return []byte(head + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body)
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
	// VayuDomains Stage 3b: accept and store mail for mail_enabled secondary
	// domains too, each in its own isolated Maildir. Primary-only (byte-identical)
	// until a secondary mail domain is registered.
	mailCfg.MailAccepts = a.acceptsSecondaryMailDomain
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
	// Retention sweeps (ADR-0130) land in the audit log like every other
	// destructive mail action.
	a.vayuMail.SetRetentionAudit(func(email string, count, days int) {
		dbpkg.AuditLog("vayumail.retention.sweep", "system", email,
			fmt.Sprintf("deleted %d read message(s) past %d days", count, days))
	})

	// VayuTalk — the ephemeral, end-to-end-encrypted messaging relay (ADR-0131).
	// Enabled whenever mail is enabled UNLESS explicitly switched off. It never
	// sees plaintext and never persists: every envelope lives in a bounded
	// in-memory store that a restart purges. The verifier is the same mail-sync
	// credential chokepoint (device approval enforced) wrapped in the shared
	// brute-force throttle; the pubkey provider is VayuPGP (minting on demand).
	talkEnabled := mailCfg.Enabled && !strings.EqualFold(config.EnvOr("VAYUOS_TALK", "on"), "off")
	talkBridge := &vayuMailBridge{app: a}
	a.vayuTalk = vtalk.NewEngine(vtalk.Config{
		Enabled: talkEnabled,
		Verify: func(ctx context.Context, email, password string) bool {
			// Same decaying per-mailbox throttle as the mail listeners, so a
			// VayuTalk connect cannot be used to bypass brute-force defences.
			if d := mailAuthThrottle.Delay(email); d > 0 {
				time.Sleep(d)
			}
			ok := talkBridge.verifyCredential(ctx, email, password)
			if ok {
				mailAuthThrottle.Success(email)
			} else {
				mailAuthThrottle.Fail(email)
			}
			return ok
		},
		PubKey: func(email string) (string, string, error) {
			if a.vayuPGP == nil {
				return "", "", fmt.Errorf("vayupgp unavailable")
			}
			pk, err := a.vayuPGP.GetPublicKey(email)
			if err != nil {
				// Mint on demand so a mailbox that pre-dates auto-keygen still
				// resolves a key (WKD/GetPublicKey would otherwise 404).
				name := email
				if i := strings.Index(name, "@"); i > 0 {
					name = name[:i]
				}
				if _, gerr := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: email, Name: name, Email: email}); gerr != nil {
					return "", "", err
				}
				pk, err = a.vayuPGP.GetPublicKey(email)
				if err != nil {
					return "", "", err
				}
			}
			return pk.Armor, pk.Fingerprint, nil
		},
	})

	secEnabled := strings.EqualFold(config.EnvOr("VAYUOS_SECURITY_UPDATES", "off"), "on")
	a.vayuSec = secwatch.New(secEnabled)

	a.vayuKernel = vkernel.NewBus()
	a.vayuHealth = vkernel.NewHealthMonitor()

	steps := []vkernel.Step{
		{Sub: a.vayuPGP, Critical: true},
		{Sub: a.vayuMail, Critical: false},
		{Sub: a.vayuTalk, Critical: false},
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
			// EnsureKeypair (not GenerateKeypair) so a CMS user created for an
			// address that ALREADY has a mailbox key reuses that key instead of
			// minting a SECOND key for the same email. Two keys per address is
			// what lets the web encrypt to one while a device holds the other —
			// the root of "web message never decrypts on the phone".
			if kp, err := a.vayuPGP.EnsureKeypair(&vpgp.PGPUser{UserID: e.UserID, Name: e.Name, Email: e.Email}); err != nil {
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

// redirectLegacyVayuOS maps the pre-2.8 /os/vayuos/* URLs onto the clean
// /os/vayumail/* namespace. 308 preserves the method so old POST endpoints
// (send, draft, account actions) keep working through the redirect too.
func redirectLegacyVayuOS(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	var next string
	switch {
	case p == "/os/vayuos" || p == "/os/vayuos/":
		next = "/os/vayumail"
	case p == "/os/vayuos/mail" || p == "/os/vayuos/mail/":
		next = "/os/vayumail/dns"
	case strings.HasPrefix(p, "/os/vayuos/mail/"):
		next = "/os/vayumail/" + strings.TrimPrefix(p, "/os/vayuos/mail/")
	default:
		next = "/os/vayumail/" + strings.TrimPrefix(p, "/os/vayuos/")
	}
	if q := r.URL.RawQuery; q != "" {
		next += "?" + q
	}
	http.Redirect(w, r, next, http.StatusPermanentRedirect)
}

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
  <div class="card"><div class="card-title">Privacy (VayuPGP)</div><p class="muted">End-to-end PGP, keys encrypted at rest, WKD published.</p><a class="btn" href="/os/vayumail/pgp">Manage keys</a></div>
  <div class="card"><div class="card-title">Sovereignty (VayuMail)</div><p class="muted">DKIM-signed outbound mail, direct-to-MX, DNS health.</p><a class="btn" href="/os/vayumail/dns">Mail &amp; DNS</a></div>
  <div class="card"><div class="card-title">Security updates</div><p class="muted">Track upstream PGP/crypto security releases.</p><a class="btn" href="/os/vayumail/security">Updates</a></div>`
		healthCard = `
<div class="card"><div class="card-title">Subsystem health</div>
<div class="table-wrap"><table class="table"><thead><tr><th>Component</th><th>Status</th><th>Detail</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div></div>`
	}
	body := `<div class="page-header"><h1>VayuMail</h1>
<span class="muted text-sm">Your mailboxes — read, compose and connect mail apps</span></div>` + vayuosNav("overview", admin) + `
<div class="grid grid-3">
  <div class="card"><div class="card-title">Inbox</div><p class="muted">Read mail received into your mailboxes (Maildir).</p><a class="btn" href="/os/vayumail/inbox">Open inbox</a></div>
  <div class="card"><div class="card-title">Sent</div><p class="muted">Outbound delivery queue with per-message status.</p><a class="btn" href="/os/vayumail/sent">View sent</a></div>
  <div class="card"><div class="card-title">Connect a mail app</div><p class="muted">IMAP/POP3/SMTP settings for the Gmail app, Apple Mail and more.</p><a class="btn" href="/os/vayumail/connect">Connect</a></div>` + infraCards + `
</div>` + healthCard
	writeOSHTML(w, adminOSLayout(nonce, "VayuMail", "vayuos", cfg, htmpl.HTML(body)))
}

func (a *App) handleVayuOSPGP(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// PGP key material is administrator-only — the four non-admin roles never
	// see it (redirected to their inbox).
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os/vayumail/inbox")
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
		a.denyAccess(w, r, "/os/vayumail/inbox")
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
	// Per-domain DNS (VayuDomains Stage 3d): each mail_enabled secondary domain
	// needs its own MX/SPF/DKIM/DMARC. The DKIM key is shared with the primary, so
	// the secondary publishes the SAME key value at its own selector record. Only
	// rendered when a secondary mail domain exists (byte-identical otherwise).
	for _, secHost := range a.mailSecondaryHosts(r.Context()) {
		body.WriteString(`<div class="card"><div class="card-title">DNS records to publish (` + html.EscapeString(secHost) + `)</div>`)
		body.WriteString(`<p class="muted text-sm">Secondary mail domain. Its MX points at this install's mail host; its DKIM key is shared with the primary, so publish the same key value at <span class="mono">` + html.EscapeString(mc.DKIMSelector) + `._domainkey.` + html.EscapeString(secHost) + `</span>.</p>`)
		body.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Type</th><th>Name</th><th>Value</th></tr></thead><tbody>`)
		for _, rec := range a.vayuMail.PlannedRecordsForDomain(secHost) {
			body.WriteString(`<tr><td>` + html.EscapeString(rec.Type) + `</td><td class="mono text-sm">` + html.EscapeString(rec.Name) + `</td><td class="mono text-sm vm-break">` + html.EscapeString(rec.Value) + `</td></tr>`)
		}
		body.WriteString(`</tbody></table></div></div>`)
	}
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
		a.denyAccess(w, r, "/os/vayumail/inbox")
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
    <button type="button" class="btn btn--sm" data-sec-check>Check now</button>
    <a class="btn btn--primary btn--sm" href="/os/update">Update VayuPress →</a>
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
	// One-click dependency update. VayuPress is a single static binary with its
	// dependencies compiled in, so "updating dependencies" means installing the
	// latest signed release (built with the patched dependencies) — there is no
	// separate go-get/rebuild step on the server. The button links to the
	// one-click self-updater (checksum + Ed25519 verified, atomic swap,
	// auto-rollback), the safe enterprise path to apply these patches.
	body.WriteString(`<div class="card"><div class="card-title">Apply updates (one click)</div>
<p class="text-sm muted">VayuPress ships as one self-contained, statically-linked binary, so security patches to the PGP/crypto libraries above are delivered <strong>inside a signed release</strong> — installing the latest release applies them. There is no separate dependency-fetch/rebuild step to run on your server.</p>
<p><a class="btn btn--primary btn--sm" href="/os/update">Update VayuPress now →</a></p>
<p class="text-xs muted">The updater verifies the download by SHA-256 checksum (and Ed25519 signature when a release key is pinned), backs up the database, swaps the binary atomically, and rolls back automatically if the new build fails to start.</p></div>`)
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
		{"overview", "Overview", "/os/vayumail", false},
		{"compose", "Compose", "/os/vayumail/compose", false},
		{"mailbox", "Mailbox", "/os/vayumail/inbox", false},
		{"accounts", "Accounts", "/os/vayumail/accounts", true},
		{"connect", "Connect", "/os/vayumail/connect", false},
		{"outbox", "Outbox", "/os/vayumail/sent", false},
		{"pgp", "PGP Keys", "/os/vayumail/pgp", true},
		{"mail", "DNS", "/os/vayumail/dns", true},
		{"security", "Security", "/os/vayumail/security", true},
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

// mailUserParam reads and STRICTLY validates a ?user= mailbox local-part from
// the request. A mailbox local-part is a small, well-defined character set
// (RFC 5321 dot-atom, lowercased by us); anything containing a character
// outside [a-z0-9._+-] is rejected to "" rather than sanitised, so an
// attacker-supplied value can never carry an HTML/JS metacharacter into a
// rendered fragment or an attribute. This is the sanitiser barrier for
// go/reflected-xss on every VayuMail page that echoes the selected mailbox.
func mailUserParam(r *http.Request) string {
	return sanitizeMailLocalPart(strings.TrimSpace(r.URL.Query().Get("user")))
}

// sanitizeMailLocalPart returns a valid mailbox local-part, or "". A valid
// local-part is [A-Za-z0-9._+-] only — a charset with NO HTML/JS metacharacter
// — so the returned value is safe in every context. It is additionally passed
// through html.EscapeString: on this charset that is a no-op (byte-identical
// output), but it makes the value flow through a sanitiser static analysis
// recognises, so go/reflected-xss is cleared on EVERY downstream sink without
// having to escape at each one individually. Empty in, empty out.
func sanitizeMailLocalPart(s string) string {
	if s == "" || len(s) > 64 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == '+' || c == '-'
		if !ok {
			return ""
		}
	}
	return html.EscapeString(s)
}

// mailIDParam reads and validates a ?id= maildir message id. Maildir unique
// names are [A-Za-z0-9._:,=+-] (timestamp.unique.host plus a ":2,flags" info
// suffix); anything outside that is rejected to "". Like the other mail params
// it is passed through html.EscapeString (a no-op on this charset) so the id is
// a recognised-sanitised value on every downstream sink.
func mailIDParam(r *http.Request) string {
	return sanitizeMailID(strings.TrimSpace(r.URL.Query().Get("id")))
}

// sanitizeMailID is the pure barrier behind mailIDParam, shared by the POST
// action handlers that read the id from the form (not just the query string).
// Real Maildir ids carry their subdirectory ("new/171...vm", "cur/171...:2,S"),
// so exactly one '/' is permitted; '.' never adjoins another '.' (no ".."), so
// the id cannot climb out of the mailbox even before the engine's own
// filepath.Base + ".." rejection in resolveMessage. Every allowed byte is
// HTML-inert, keeping html.EscapeString a byte-identical no-op.
func sanitizeMailID(s string) string {
	if s == "" || len(s) > 256 || strings.Contains(s, "..") ||
		strings.Count(s, "/") > 1 || strings.HasPrefix(s, "/") {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' ||
			c == ',' || c == '=' || c == '+' || c == '-' || c == '/'
		if !ok {
			return ""
		}
	}
	return html.EscapeString(s)
}

// mailFolderParam reads and STRICTLY validates a ?folder= name, defaulting to
// "Inbox". Folder names (standard or custom) are restricted to letters,
// digits, space, underscore and hyphen; anything else is rejected to the
// default, so the folder value can never carry markup into a fragment. This is
// the go/reflected-xss barrier for the folder parameter.
func mailFolderParam(r *http.Request) string {
	return sanitizeMailFolder(strings.TrimSpace(r.URL.Query().Get("folder")))
}

// sanitizeMailFolder is the pure barrier behind mailFolderParam, shared by the
// POST action handlers that read the folder from the form. Invalid input falls
// back to "Inbox". html.EscapeString is a no-op on the allowed charset
// (byte-identical output) but routes the value through a recognised sanitiser
// so go/reflected-xss is cleared on every downstream HTML sink.
func sanitizeMailFolder(f string) string {
	if f == "" || len(f) > 64 {
		return "Inbox"
	}
	for i := 0; i < len(f); i++ {
		c := f[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == ' ' || c == '_' || c == '-'
		if !ok {
			return "Inbox"
		}
	}
	return html.EscapeString(f)
}

// ── VayuMail inbox helpers ───────────────────────────────────────────────────

// mailParseFrom splits a raw From/To header ("Display Name <addr@host>" or a
// bare address) into a display name and address. Best-effort, allocation-light.
func mailParseFrom(raw string) (name, addr string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if i := strings.LastIndex(raw, "<"); i >= 0 {
		if j := strings.Index(raw[i:], ">"); j > 0 {
			addr = strings.TrimSpace(raw[i+1 : i+j])
			name = strings.Trim(strings.TrimSpace(raw[:i]), `"'`)
			return name, addr
		}
	}
	return "", raw
}

// mailDisplay returns the best human label for a sender/recipient: the display
// name when present, otherwise the address.
func mailDisplay(raw string) string {
	name, addr := mailParseFrom(raw)
	if name != "" {
		return name
	}
	if addr != "" {
		return addr
	}
	return raw
}

// mailInitials returns 1–2 uppercase initials for the avatar chip.
func mailInitials(raw string) string {
	name, addr := mailParseFrom(raw)
	src := name
	if src == "" {
		src = addr
	}
	src = strings.TrimSpace(src)
	if src == "" {
		return "?"
	}
	if parts := strings.Fields(src); len(parts) >= 2 {
		return strings.ToUpper(string([]rune(parts[0])[:1]) + string([]rune(parts[1])[:1]))
	}
	r := []rune(src)
	if len(r) >= 2 {
		return strings.ToUpper(string(r[:2]))
	}
	return strings.ToUpper(string(r[:1]))
}

// mailAvatarIdx maps a seed deterministically to one of the avatar palette
// classes (0–5) via an FNV-1a hash.
func mailAvatarIdx(seed string) int {
	var h uint32 = 2166136261
	for i := 0; i < len(seed); i++ {
		h ^= uint32(seed[i])
		h *= 16777619
	}
	return int(h % 6)
}

// mailAvatar renders the colored initials chip for a sender/recipient.
func mailAvatar(raw string) string {
	_, addr := mailParseFrom(raw)
	seed := addr
	if seed == "" {
		seed = raw
	}
	return `<span class="vm-av vm-av--` + itoaSafe(mailAvatarIdx(seed)) + `" aria-hidden="true">` + html.EscapeString(mailInitials(raw)) + `</span>`
}

// mailRelTime renders a compact relative timestamp ("just now", "5m", "3h",
// "2d", "Jan 2") with the absolute time as a tooltip.
func mailRelTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	abs := t.Format("2006-01-02 15:04")
	d := time.Since(t)
	var rel string
	switch {
	case d < time.Minute:
		rel = "just now"
	case d < time.Hour:
		rel = itoaSafe(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		rel = itoaSafe(int(d.Hours())) + "h"
	case d < 7*24*time.Hour:
		rel = itoaSafe(int(d.Hours()/24)) + "d"
	default:
		rel = t.Format("Jan 2")
	}
	return `<span title="` + abs + `">` + rel + `</span>`
}

// jsonStringEscape escapes a value for use inside a JSON string literal
// (backslash, double-quote and control characters). It does NOT do HTML
// escaping — the whole attribute is HTML-escaped once, in hxVals.
func jsonStringEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				const hexd = "0123456789abcdef"
				b.WriteByte(hexd[r>>4])
				b.WriteByte(hexd[r&0xf])
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// hxVals builds an hx-vals='{...}' attribute from alternating key/value pairs.
// Each key and value is JSON-string-escaped and then passed through
// html.EscapeString — a sanitiser static analysis recognises — so the tainted
// value path is provably clean (closing go/reflected-xss) while a browser
// un-escapes the attribute back to valid JSON for HTMX. For safe inputs
// (literal keys, and the strictly-charset-validated user/folder params)
// html.EscapeString is a no-op, so the emitted attribute is byte-identical to
// before.
func hxVals(pairs ...string) string {
	esc := func(s string) string { return html.EscapeString(jsonStringEscape(s)) }
	var sb strings.Builder
	sb.WriteString(`hx-vals='{`)
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`"` + esc(pairs[i]) + `":"` + esc(pairs[i+1]) + `"`)
	}
	sb.WriteString(`}'`)
	return sb.String()
}

// folderUnread returns the unseen-message count for the folders where "unread"
// is meaningful (received mail), so the folder tabs can show live badges. Sent
// and Drafts are authored, not received, so they are skipped.
func (a *App) folderUnread(user string) map[string]int {
	counts := map[string]int{}
	if a.vayuMail == nil {
		return counts
	}
	for _, f := range []string{"Inbox", "Junk"} {
		msgs, err := a.vayuMail.ListFolder(user, f)
		if err != nil {
			continue
		}
		n := 0
		for _, m := range msgs {
			if !m.Seen {
				n++
			}
		}
		if n > 0 {
			counts[f] = n
		}
	}
	return counts
}

func folderTabs(user, active string, counts map[string]int) string {
	var sb strings.Builder
	sb.WriteString(`<div class="vmtabs" role="tablist">`)
	for _, f := range vmail.StandardFolders {
		cls := "tab"
		if strings.EqualFold(f, active) {
			cls = "tab tab--active"
		}
		full := "/os/vayumail/inbox?user=" + qparam(user) + "&folder=" + qparam(f)
		frag := "/os/vayumail/inbox/fragment?user=" + qparam(user) + "&folder=" + qparam(f)
		badge := ""
		if n := counts[f]; n > 0 {
			badge = ` <span class="vm-tab-badge">` + itoaSafe(n) + `</span>`
		}
		sb.WriteString(`<a class="` + cls + `" href="` + full + `" hx-get="` + frag + `" hx-target="#vm-inbox-list" hx-swap="innerHTML" hx-push-url="` + full + `">` + f + badge + `</a>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// handleVayuOSInbox lists mailboxes, or (with ?user=) the messages in a folder.
// The folder view is an HTMX fragment (#vm-inbox-list) that swaps in place for
// every action, folder switch, and the live new-mail poll — no full-page reload.
func (a *App) handleVayuOSInbox(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// CSRF cookie so the HTMX row/bulk POSTs pass the double-submit middleware.
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Mailbox</h1><span class="muted text-sm">Received &amp; filed mail (Maildir)</span></div>`)
	body.WriteString(vayuosNav("mailbox", a.isAdminRequest(r)))

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to a real domain to provision mailboxes. The inbound SMTP/IMAP listener runs by default once a domain is set (disable with <code>VAYUOS_MAIL_INBOUND=off</code>); receiving external mail also needs port 25 reachable and MX/A DNS records pointing at this host.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	domain := a.vayuMail.Config().Domain
	user := mailUserParam(r)
	folder := mailFolderParam(r)
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
		body.WriteString(`<div class="card"><div class="card-title">Mailboxes</div><div class="table-wrap"><table class="table vm-list"><thead><tr><th>Mailbox</th><th>Inbox</th><th>Unseen</th></tr></thead><tbody>`)
		if len(boxes) == 0 {
			body.WriteString(`<tr><td colspan="3" class="muted">No mailboxes yet. Create one under <a href="/os/vayumail/accounts">Accounts</a>, or one is provisioned when a CMS account is created.</td></tr>`)
		}
		for _, bx := range boxes {
			addr := bx.Username + "@" + domain
			unseen := ""
			if bx.Unseen > 0 {
				unseen = `<span class="vm-tab-badge">` + itoaSafe(bx.Unseen) + `</span>`
			}
			body.WriteString(`<tr><td><a class="vm-from" href="/os/vayumail/inbox?user=` + qparam(bx.Username) + `">` + mailAvatar(addr) + `<span class="vm-name">` + html.EscapeString(addr) + `</span></a></td><td>` + itoaSafe(bx.Total) + `</td><td>` + unseen + `</td></tr>`)
		}
		body.WriteString(`</tbody></table></div></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	// Folder view — HTMX fragment container. Every action, folder switch and the
	// live new-mail poll swap #vm-inbox-list in place (no full reload). The poll
	// element lives inside the fragment (see vayuInboxBody) so it always carries
	// the folder currently being viewed.
	// Split reading pane: the message list (left) and an in-place reader (right).
	// Rows load the message into #vm-readpane via HTMX; pane actions refresh the
	// list via HX-Trigger. On narrow screens CSS collapses this to one column and
	// the pane overlays the list (see admin-os.css .vm-split).
	body.WriteString(`<div class="vm-split">`)
	body.WriteString(`<div id="vm-inbox-list" class="vm-inbox-list">`)
	body.WriteString(a.vayuInboxBody(user, folder))
	body.WriteString(`</div>`)
	body.WriteString(`<div id="vm-readpane" class="vm-readpane">` + vayuReadpaneEmpty("") + `</div>`)
	body.WriteString(`</div>`)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Mailbox", "vayuos", cfg, htmpl.HTML(body.String())))
}

// vayuInboxBody renders the folder view (toolbar + quota + tabs + message list)
// as an HTMX fragment. It is returned on page load and swapped into
// #vm-inbox-list on the new-mail poll, after any row/bulk action, and on folder
// switch — so the mailbox never does a jarring full-page reload.
func (a *App) vayuInboxBody(user, folder string) string {
	domain := a.vayuMail.Config().Domain
	mbox := user + "@" + domain
	var b strings.Builder

	// Live new-mail poll. It lives inside the fragment (re-rendered on every
	// swap) so it always targets the folder currently in view — a poll bound to
	// the outer wrapper would keep reloading the folder that was first opened.
	frag := "/os/vayumail/inbox/fragment?user=" + qparam(user) + "&folder=" + qparam(folder)
	b.WriteString(`<div class="vm-poller" aria-hidden="true" hx-get="` + frag + `" hx-trigger="every 90s, vm-mail-changed from:body" hx-target="#vm-inbox-list" hx-swap="innerHTML"></div>`)

	// Sticky toolbar: mailbox identity + Compose + search.
	b.WriteString(`<div class="vm-toolbar">`)
	b.WriteString(`<div class="vm-toolbar-id">` + mailAvatar(mbox) + `<div class="vm-toolbar-meta"><strong>` + html.EscapeString(mbox) + `</strong><a class="text-sm muted" href="/os/vayumail/inbox">All mailboxes</a></div></div>`)
	b.WriteString(`<div class="vm-toolbar-actions">`)
	b.WriteString(`<a class="btn btn--primary btn--sm" href="/os/vayumail/compose?user=` + qparam(user) + `">✎ Compose</a>`)
	b.WriteString(`<form class="vm-search" method="get" action="/os/vayumail/search"><input type="hidden" name="user" value="` + html.EscapeString(user) + `"><input class="input input--sm" type="search" name="q" placeholder="Search mail…" aria-label="Search mail"><button class="btn btn--sm" type="submit">Search</button></form>`)
	b.WriteString(`</div></div>`)

	// Storage quota bar (fill width applied by admin-os-mail.js via CSSOM).
	used := a.vayuMail.MailboxUsage(mbox)
	quota := a.vayuMail.MailboxQuota(mbox)
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
		b.WriteString(`<div class="vm-quota"><div class="vm-quota-meta text-sm muted">Storage: ` + html.EscapeString(humanBytes(used)) + ` of ` + html.EscapeString(humanBytes(quota)) + ` used (` + itoaSafe(pct) + `%)</div><div class="vm-quota-track"><div class="vm-quota-fill vm-quota-fill--` + level + `" data-quota-pct="` + itoaSafe(pct) + `"></div></div>`)
		if pct >= 100 {
			b.WriteString(`<div class="vm-quota-full text-sm">⚠ Your mailbox is full — incoming mail may be rejected and you can't send until you free space.</div>`)
		}
		b.WriteString(`</div>`)
	}

	// Folder tabs with live unread badges (HTMX folder switching).
	b.WriteString(folderTabs(user, folder, a.folderUnread(user)))

	msgs, err := a.vayuMail.ListFolder(user, folder)
	if err != nil {
		b.WriteString(`<div class="empty-state">Could not read folder: ` + html.EscapeString(err.Error()) + `</div>`)
		return b.String()
	}
	isDrafts := strings.EqualFold(folder, "Drafts")
	isSent := strings.EqualFold(folder, "Sent")
	received := !isDrafts && !isSent

	// Scope inputs (user+folder) are included by the bulk POSTs via hx-include
	// alongside the checked row ids. Selection state (count + show/hide of the
	// bulk bar) is managed by delegated JS that survives HTMX swaps.
	b.WriteString(`<input type="hidden" name="user" value="` + html.EscapeString(user) + `" data-vm-scope><input type="hidden" name="folder" value="` + html.EscapeString(folder) + `" data-vm-scope>`)
	if len(msgs) > 0 {
		inc := ` hx-include="[data-vm-scope],[data-vm-check]:checked" hx-target="#vm-inbox-list" hx-swap="innerHTML"`
		b.WriteString(`<div class="vm-bulk" data-vm-bulkbar hidden><span class="text-sm muted" data-vm-bulkcount>0 selected</span>`)
		if received {
			b.WriteString(`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/inbox/action" hx-vals='{"action":"mark","mark":"read"}'` + inc + `>Mark read</button>`)
			b.WriteString(`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/inbox/action" hx-vals='{"action":"mark","mark":"unread"}'` + inc + `>Mark unread</button>`)
			b.WriteString(`<button type="button" class="btn btn--sm" hx-post="/os/vayumail/inbox/action" hx-vals='{"action":"pin","pin":"1"}'` + inc + `>📌 Pin</button>`)
			b.WriteString(`<span class="vm-move"><select class="input input--sm" name="to" aria-label="Move selected to folder" hx-post="/os/vayumail/inbox/action" hx-trigger="change" hx-vals='{"action":"move"}'` + inc + `><option value="">Move to…</option>`)
			for _, f := range vmail.StandardFolders {
				// Snoozed is excluded: only the snooze action files there (a
				// manual move would sleep forever with no wake row).
				if strings.EqualFold(f, folder) || strings.EqualFold(f, "Snoozed") {
					continue
				}
				b.WriteString(`<option value="` + html.EscapeString(f) + `">` + html.EscapeString(f) + `</option>`)
			}
			b.WriteString(`</select></span>`)
		}
		b.WriteString(`<button type="button" class="btn btn--sm btn--danger" hx-post="/os/vayumail/inbox/action" hx-vals='{"action":"delete"}' hx-confirm="Permanently delete the selected message(s)?"` + inc + `>Delete</button>`)
		b.WriteString(`</div>`)
	}

	// Message list.
	fromLabel := "From"
	if !received {
		fromLabel = "To"
	}
	b.WriteString(`<div class="table-wrap"><table class="table vm-list"><thead><tr><th class="vm-check"><input type="checkbox" data-vm-check-all aria-label="Select all"></th><th></th><th>` + fromLabel + `</th><th>Subject</th><th>Date</th><th></th></tr></thead><tbody>`)
	if len(msgs) == 0 {
		b.WriteString(`<tr><td colspan="6" class="muted">No messages in ` + html.EscapeString(folder) + `.</td></tr>`)
	}
	// Conversation threading: messages sharing a normalized subject (Re:/Fwd:
	// prefixes stripped) group into one thread. The newest message is the
	// visible row, carrying a count badge that toggles the older ones (hidden
	// rows, flipped by delegated JS that survives HTMX swaps).
	threadOf := map[string]int{} // normalized subject -> thread index
	threadN := 0
	rowHTML := func(m vmail.StoredMessage, threadAttr, extraCls, badge string) string {
		subj := m.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		who := m.From
		if !received {
			who = m.To
		}
		link := "/os/vayumail/message?user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(m.ID)
		if isDrafts {
			link = "/os/vayumail/compose?draft=1&user=" + qparam(user) + "&id=" + qparam(m.ID)
		}
		rowCls := "vm-row-item" + extraCls
		if !m.Seen && received {
			rowCls += " vm-unread"
		}
		pinVal, pinIcon := "1", "📌"
		if m.Flagged {
			pinVal, pinIcon = "0", "📍"
		}
		pin := `<button type="button" class="btn btn--xs btn--ghost" title="Pin" hx-post="/os/vayumail/inbox/action" ` + hxVals("action", "pin", "pin", pinVal, "user", user, "folder", folder, "id", m.ID) + ` hx-target="#vm-inbox-list" hx-swap="innerHTML">` + pinIcon + `</button>`
		tick := ""
		if received {
			mark, label := "read", "Mark read"
			if m.Seen {
				mark, label = "unread", "✓ read"
			}
			tick = `<button type="button" class="btn btn--xs" hx-post="/os/vayumail/inbox/action" ` + hxVals("action", "mark", "mark", mark, "user", user, "folder", folder, "id", m.ID) + ` hx-target="#vm-inbox-list" hx-swap="innerHTML">` + label + `</button>`
		}
		check := `<input type="checkbox" class="vm-check-row" name="id" value="` + html.EscapeString(m.ID) + `" data-vm-check aria-label="Select message">`
		// Non-draft rows open in the split reading pane (HTMX); the href stays as a
		// middle-click / no-JS fallback to the standalone message page. Drafts open
		// the composer for editing (full navigation). data-vm-open lets the delegated
		// JS highlight the active row across HTMX swaps.
		subjA := `<a href="` + link + `"`
		if !isDrafts {
			subjA += ` class="vm-subj-link" data-vm-open hx-get="` + link + `&pane=1" hx-target="#vm-readpane" hx-swap="innerHTML"`
		}
		subjA += `>` + html.EscapeString(subj) + `</a>`
		return `<tr class="` + rowCls + `" data-vm-row` + threadAttr + `><td class="vm-check">` + check + `</td><td>` + pin + `</td><td><div class="vm-from">` + mailAvatar(who) + `<span class="vm-name" title="` + html.EscapeString(who) + `">` + html.EscapeString(mailDisplay(who)) + `</span></div></td><td class="vm-subj">` + subjA + badge + `</td><td class="muted text-sm vm-date">` + mailRelTime(m.Date) + `</td><td class="row-actions">` + tick + `</td></tr>`
	}
	// Pass 1: count thread members per normalized subject.
	counts := map[string]int{}
	for _, m := range msgs {
		if k := normSubject(m.Subject); k != "" {
			counts[k]++
		}
	}
	for _, m := range msgs {
		k := normSubject(m.Subject)
		if k == "" || counts[k] < 2 {
			b.WriteString(rowHTML(m, "", "", "")) // unthreaded row
			continue
		}
		if idx, seen := threadOf[k]; seen {
			// Older thread member: hidden until the lead's badge is toggled.
			b.WriteString(rowHTML(m, ` data-vm-thread="`+strconv.Itoa(idx)+`" hidden`, " vm-thread-child", ""))
			continue
		}
		threadN++
		threadOf[k] = threadN
		badge := ` <button type="button" class="vm-thread-count" data-vm-thread-toggle="` + strconv.Itoa(threadN) + `" title="Show conversation (` + strconv.Itoa(counts[k]) + ` messages)" aria-label="Show conversation">` + strconv.Itoa(counts[k]) + `</button>`
		b.WriteString(rowHTML(m, "", "", badge))
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// normSubject normalizes a subject for conversation grouping: reply/forward
// prefixes (Re:, Fwd:, Fw:, any depth) are stripped and the rest lowercased.
// An empty result means "do not group".
func normSubject(s string) string {
	s = strings.TrimSpace(s)
	for {
		low := strings.ToLower(s)
		switch {
		case strings.HasPrefix(low, "re:"):
			s = strings.TrimSpace(s[3:])
		case strings.HasPrefix(low, "fwd:"):
			s = strings.TrimSpace(s[4:])
		case strings.HasPrefix(low, "fw:"):
			s = strings.TrimSpace(s[3:])
		default:
			return strings.ToLower(s)
		}
	}
}

// handleVayuOSInboxFragment returns the inbox folder view for HTMX (new-mail
// poll, folder switch, post-action refresh).
func (a *App) handleVayuOSInboxFragment(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeOSFragment(w, `<div class="empty-state">VayuMail is inactive.</div>`)
		return
	}
	user := mailUserParam(r)
	folder := mailFolderParam(r)
	if folder == "" {
		folder = "Inbox"
	}
	if !a.isAdminRequest(r) {
		local, _ := a.ownMailbox(r)
		if local == "" {
			writeOSFragment(w, `<div class="empty-state">No mailbox has been assigned to your account.</div>`)
			return
		}
		user = local
	}
	if user == "" {
		writeOSFragment(w, `<div class="empty-state">No mailbox selected.</div>`)
		return
	}
	writeOSFragment(w, a.vayuInboxBody(user, folder))
}

// handleVayuOSInboxAction applies a mark / pin / move / delete to one message
// (single-row action carries its id in hx-vals) or many (bulk carries the
// checked row ids), then returns the refreshed inbox body for HTMX to swap in.
func (a *App) handleVayuOSInboxAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "invalid form", "")
		return
	}
	// Sanitise at the point of read (same barrier as the GET readers): these
	// values are re-rendered into the refreshed inbox fragment below, so a raw
	// form value would be a reflected-XSS sink. sanitizeMailLocalPart /
	// sanitizeMailFolder are no-ops on valid input but route the value through
	// html.EscapeString, clearing the taint for every downstream HTML sink.
	user := sanitizeMailLocalPart(strings.TrimSpace(r.PostFormValue("user")))
	folder := sanitizeMailFolder(strings.TrimSpace(r.PostFormValue("folder")))
	if !a.isAdminRequest(r) {
		local, _ := a.ownMailbox(r)
		if local == "" || !strings.EqualFold(local, user) {
			writeAPIError(w, r, http.StatusForbidden, "forbidden", "you can only manage your own mailbox", "")
			return
		}
		user = local
	}
	if user == "" {
		writeAPIError(w, r, http.StatusBadRequest, "validation_error", "user is required", "")
		return
	}
	// De-duplicate the selected ids (a per-row action carries one id in hx-vals;
	// bulk carries the checked checkboxes, all named "id").
	seen := map[string]bool{}
	var ids []string
	for _, id := range r.Form["id"] {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > 500 {
		ids = ids[:500]
	}
	action := strings.TrimSpace(r.PostFormValue("action"))
	apply := func(id string) error {
		switch action {
		case "mark":
			if r.PostFormValue("mark") == "unread" {
				_, err := a.vayuMail.MarkUnread(user, folder, id)
				return err
			}
			_, err := a.vayuMail.MarkRead(user, folder, id)
			return err
		case "pin":
			_, err := a.vayuMail.SetPinned(user, folder, id, r.PostFormValue("pin") == "1")
			return err
		case "delete":
			return a.vayuMail.DeleteMessage(user, folder, id)
		case "move":
			to := strings.TrimSpace(r.PostFormValue("to"))
			if to == "" {
				return nil
			}
			return a.vayuMail.MoveMessage(user, id, folder, to)
		default:
			return fmt.Errorf("unknown action")
		}
	}
	// Best-effort per message: a single stale id must not fail the whole batch;
	// the refreshed fragment reflects whatever changed.
	for _, id := range ids {
		_ = apply(id)
	}
	writeOSFragment(w, a.vayuInboxBody(user, folder))
}

// handleVayuOSSearch runs a bounded full-text search across a mailbox's folders.
func (a *App) handleVayuOSSearch(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	user := mailUserParam(r)
	if !a.isAdminRequest(r) {
		// Non-admins may only search their own assigned mailbox.
		user, _ = a.ownMailbox(r)
	}
	sf := parseSearchFilters(r)
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Search mail</h1><span class="muted text-sm">` + html.EscapeString(user+"@"+a.cfgDomain()) + `</span></div>`)
	body.WriteString(vayuosNav("mailbox", a.isAdminRequest(r)))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || user == "" {
		body.WriteString(`<div class="empty-state">VayuMail is inactive or no mailbox selected. <a href="/os/vayumail/inbox">Back to Mailbox</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Search mail", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div class="card"><div class="card-title"><a href="/os/vayumail/inbox?user=` + qparam(user) + `">← ` + html.EscapeString(user+"@"+a.cfgDomain()) + `</a></div>`)
	// Filter bar — results update instantly over HTMX as you type or change a
	// filter (debounced), swapping #vm-search-results with no full-page reload.
	folderOpts := `<option value="">All folders</option>`
	for _, f := range vmail.StandardFolders {
		sel := ""
		if strings.EqualFold(f, sf.folder) {
			sel = ` selected`
		}
		folderOpts += `<option value="` + html.EscapeString(f) + `"` + sel + `>` + html.EscapeString(f) + `</option>`
	}
	unreadChecked := ""
	if sf.unreadOnly {
		unreadChecked = ` checked`
	}
	body.WriteString(`<form class="vm-search-form" hx-get="/os/vayumail/search/fragment" hx-target="#vm-search-results" hx-swap="innerHTML" hx-trigger="submit, input changed delay:400ms, change delay:150ms">
  <input type="hidden" name="user" value="` + html.EscapeString(user) + `">
  <div class="vm-search-row">
    <input class="input" type="search" name="q" value="` + html.EscapeString(sf.q) + `" placeholder="Search mail (from, subject, body)…" aria-label="Search mail" autofocus>
    <button class="btn btn--primary" type="submit">Search</button>
  </div>
  <div class="vm-search-filters">
    <select class="input input--sm" name="folder" aria-label="Folder">` + folderOpts + `</select>
    <input class="input input--sm" type="text" name="from" value="` + html.EscapeString(sf.from) + `" placeholder="From contains…" aria-label="From filter">
    <label class="vm-filter-date">After <input class="input input--sm" type="date" name="after" value="` + html.EscapeString(sf.after) + `"></label>
    <label class="vm-filter-date">Before <input class="input input--sm" type="date" name="before" value="` + html.EscapeString(sf.before) + `"></label>
    <label class="vm-filter-check"><input type="checkbox" name="unread" value="1"` + unreadChecked + `> Unread only</label>
  </div>
</form>`)
	body.WriteString(`<div id="vm-search-results">` + a.vayuSearchResults(user, sf) + `</div>`)
	body.WriteString(`</div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Search mail", "vayuos", cfg, htmpl.HTML(body.String())))
}

// searchFilters holds the query and the refinement filters for a mail search.
type searchFilters struct {
	q, folder, from, after, before string
	unreadOnly                     bool
}

func parseSearchFilters(r *http.Request) searchFilters {
	q := r.URL.Query()
	return searchFilters{
		q:          strings.TrimSpace(q.Get("q")),
		folder:     strings.TrimSpace(q.Get("folder")),
		from:       strings.TrimSpace(q.Get("from")),
		after:      strings.TrimSpace(q.Get("after")),
		before:     strings.TrimSpace(q.Get("before")),
		unreadOnly: q.Get("unread") == "1",
	}
}

// vayuSearchResults runs the full-text search and applies the refinement
// filters, returning the results table (or an empty/prompt state) as an HTMX
// fragment. Matches in From/Subject are highlighted.
func (a *App) vayuSearchResults(user string, sf searchFilters) string {
	var b strings.Builder
	if sf.q == "" {
		b.WriteString(`<div class="empty-state">Type a search above to find mail across every folder — refine with the folder, sender, date and unread filters.</div>`)
		return b.String()
	}
	results, _ := a.vayuMail.Search(user, sf.q, 200)
	afterT, hasAfter := parseDay(sf.after)
	beforeT, hasBefore := parseDay(sf.before)
	fromLower := strings.ToLower(sf.from)
	var matched []vmail.SearchResult
	for _, m := range results {
		if sf.folder != "" && !strings.EqualFold(m.Folder, sf.folder) {
			continue
		}
		if fromLower != "" && !strings.Contains(strings.ToLower(m.From), fromLower) {
			continue
		}
		if sf.unreadOnly && m.Seen {
			continue
		}
		if hasAfter && m.Date.Before(afterT) {
			continue
		}
		if hasBefore && !m.Date.Before(beforeT.AddDate(0, 0, 1)) {
			continue
		}
		matched = append(matched, m)
	}
	b.WriteString(`<div class="vm-search-count text-sm muted">` + itoaSafe(len(matched)) + ` result(s) for “` + html.EscapeString(sf.q) + `”</div>`)
	b.WriteString(`<div class="table-wrap"><table class="table vm-list"><thead><tr><th>Folder</th><th>From</th><th>Subject</th><th>Date</th></tr></thead><tbody>`)
	if len(matched) == 0 {
		b.WriteString(`<tr><td colspan="4" class="muted">No matches. Try a different term or relax the filters.</td></tr>`)
	}
	for _, m := range matched {
		subj := m.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		link := "/os/vayumail/message?user=" + qparam(user) + "&folder=" + qparam(m.Folder) + "&id=" + qparam(m.ID)
		b.WriteString(`<tr><td><span class="badge">` + html.EscapeString(m.Folder) + `</span></td><td><div class="vm-from">` + mailAvatar(m.From) + `<span class="vm-name">` + highlightMatch(mailDisplay(m.From), sf.q) + `</span></div></td><td class="vm-subj"><a href="` + link + `">` + highlightMatch(subj, sf.q) + `</a></td><td class="muted text-sm vm-date">` + mailRelTime(m.Date) + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)
	return b.String()
}

// handleVayuOSSearchFragment returns the search results for the instant HTMX
// filter bar.
func (a *App) handleVayuOSSearchFragment(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeOSFragment(w, `<div class="empty-state">VayuMail is inactive.</div>`)
		return
	}
	user := mailUserParam(r)
	if !a.isAdminRequest(r) {
		user, _ = a.ownMailbox(r)
	}
	if user == "" {
		writeOSFragment(w, `<div class="empty-state">No mailbox selected.</div>`)
		return
	}
	writeOSFragment(w, a.vayuSearchResults(user, parseSearchFilters(r)))
}

// parseDay parses a YYYY-MM-DD date-input value (UTC midnight). ok is false when
// the value is empty or malformed.
func parseDay(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// highlightMatch HTML-escapes text and wraps each occurrence of the search
// term(s) in <mark>. It works on the escaped string and merges overlapping
// match ranges so the emitted markup is always well-formed.
func highlightMatch(text, q string) string {
	esc := html.EscapeString(text)
	q = strings.TrimSpace(q)
	if q == "" {
		return esc
	}
	lower := strings.ToLower(esc)
	seen := map[string]bool{}
	var terms []string
	for _, t := range strings.Fields(q) {
		et := strings.ToLower(html.EscapeString(t))
		if et != "" && !seen[et] {
			seen[et] = true
			terms = append(terms, et)
		}
	}
	type rng struct{ s, e int }
	var ranges []rng
	for _, t := range terms {
		from := 0
		for {
			i := strings.Index(lower[from:], t)
			if i < 0 {
				break
			}
			s := from + i
			ranges = append(ranges, rng{s, s + len(t)})
			from = s + len(t)
		}
	}
	if len(ranges) == 0 {
		return esc
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].s < ranges[j].s })
	merged := []rng{ranges[0]}
	for _, r := range ranges[1:] {
		last := &merged[len(merged)-1]
		if r.s <= last.e {
			if r.e > last.e {
				last.e = r.e
			}
		} else {
			merged = append(merged, r)
		}
	}
	var b strings.Builder
	prev := 0
	for _, r := range merged {
		b.WriteString(esc[prev:r.s])
		b.WriteString("<mark>")
		b.WriteString(esc[r.s:r.e])
		b.WriteString("</mark>")
		prev = r.e
	}
	b.WriteString(esc[prev:])
	return b.String()
}

// handleVayuOSMessage shows a single message with Junk/Trash/Delete actions.
// splitQuoted separates the freshly-written part of a plain-text reply from the
// quoted history beneath it, so the reader can collapse the quote (like Gmail's
// "…"). It returns (text, "") when there is no clear quote boundary or nothing
// above it, so a message is never hidden entirely.
func splitQuoted(text string) (main, quoted string) {
	lines := strings.Split(text, "\n")
	cut := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, ">") ||
			(strings.HasPrefix(t, "On ") && strings.HasSuffix(t, "wrote:")) ||
			t == "-----Original Message-----" ||
			strings.HasPrefix(t, "-------- Forwarded message") {
			cut = i
			break
		}
	}
	if cut < 1 {
		return text, ""
	}
	main = strings.Join(lines[:cut], "\n")
	if strings.TrimSpace(main) == "" {
		return text, ""
	}
	return main, strings.Join(lines[cut:], "\n")
}

// mailPGPBadge returns a small badge when the raw message still carries PGP
// armor — i.e. it is encrypted (and could not be decrypted for this mailbox) or
// carries an inline signature. Successfully decrypted mail shows no armor and
// therefore no badge.
func mailPGPBadge(raw []byte) string {
	rs := string(raw)
	switch {
	case strings.Contains(rs, "-----BEGIN PGP MESSAGE-----"),
		strings.Contains(rs, "X-VayuPGP: encrypted"):
		// The second form is the marker left after transparent server-side
		// decryption (inline or PGP/MIME) — the message WAS end-to-end
		// encrypted even though the served copy is readable.
		return ` <span class="vm-pgp vm-pgp--enc" title="PGP-encrypted message">🔒 Encrypted</span>`
	case strings.Contains(rs, "-----BEGIN PGP SIGNED MESSAGE-----"), strings.Contains(rs, "-----BEGIN PGP SIGNATURE-----"):
		return ` <span class="vm-pgp vm-pgp--sig" title="Carries a PGP signature">✓ Signed</span>`
	}
	return ""
}

func (a *App) handleVayuOSMessage(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	user := mailUserParam(r)
	if !a.isAdminRequest(r) {
		// Non-admins may only read messages in their own assigned mailbox.
		user, _ = a.ownMailbox(r)
	}
	folder := mailFolderParam(r)
	if folder == "" {
		folder = "Inbox"
	}
	id := mailIDParam(r)
	// pane mode: the split reading pane loads the message beside the list via
	// HTMX, so we return just the reader card fragment (no page chrome/layout),
	// and the card's nav + actions are pure HTMX/native (no page-load JS).
	pane := r.URL.Query().Get("pane") == "1"

	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || user == "" || id == "" {
		if pane {
			writeOSHTML(w, vayuReadpaneEmpty("Message not available."))
			return
		}
		var body strings.Builder
		body.WriteString(`<div class="page-header"><h1>Message</h1></div>` + vayuosNav("mailbox", a.isAdminRequest(r)))
		body.WriteString(`<div class="empty-state">Message not available. <a href="/os/vayumail/inbox">Back to Mailbox</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}

	card, ok := a.vayuReaderCard(user, folder, id, pane)
	if !ok {
		if pane {
			writeOSHTML(w, vayuReadpaneEmpty("Could not read this message."))
			return
		}
		var body strings.Builder
		body.WriteString(`<div class="page-header"><h1>Message</h1></div>` + vayuosNav("mailbox", a.isAdminRequest(r)))
		body.WriteString(`<div class="empty-state">Could not read message. <a href="/os/vayumail/inbox?user=` + qparam(user) + `">Back</a></div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	if pane {
		// Fragment only — the inbox page already loaded admin-os-mail.js.
		writeOSHTML(w, card)
		return
	}
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Message</h1><span class="muted text-sm">` + html.EscapeString(user+"@"+a.cfgDomain()) + ` · ` + html.EscapeString(folder) + `</span></div>`)
	body.WriteString(vayuosNav("mailbox", a.isAdminRequest(r)))
	body.WriteString(card)
	body.WriteString(`<script nonce="` + nonce + `" src="/os/static/js/admin-os-mail.js?v=` + assetVer("js/admin-os-mail.js") + `"></script>`)
	writeOSHTML(w, adminOSLayout(nonce, "Message", "vayuos", cfg, htmpl.HTML(body.String())))
}

// vayuReadpaneEmpty renders the reading-pane placeholder (also the "message
// left the folder" state after a pane move/delete).
func vayuReadpaneEmpty(msg string) string {
	if msg == "" {
		msg = "Select a message to read it here."
	}
	return `<div class="vm-readpane-empty"><div class="vm-readpane-empty-ico">✉</div><p class="muted">` + html.EscapeString(msg) + `</p></div>`
}

// vayuReaderCard renders the message reader card for both the standalone
// message page (pane=false: hrefs + admin-os-mail.js actions) and the split
// reading pane (pane=true: HTMX nav/actions targeting #vm-readpane, native
// <details> for raw — nothing depends on JS bound at page load). It reads the
// message and marks it read (received folders only). ok is false on error.
func (a *App) vayuReaderCard(user, folder, id string, pane bool) (string, bool) {
	raw, err := a.vayuMail.ReadFolderMessage(user, folder, id)
	if err != nil {
		return "", false
	}
	received := !strings.EqualFold(folder, "Drafts") && !strings.EqualFold(folder, "Sent")
	if received {
		if nid, merr := a.vayuMail.MarkRead(user, folder, id); merr == nil && nid != "" {
			id = nid
		}
	}
	back := "/os/vayumail/inbox?user=" + qparam(user) + "&folder=" + qparam(folder)
	msgURL := func(mid string) string {
		return "/os/vayumail/message?user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(mid)
	}
	paneURL := func(mid string) string { return msgURL(mid) + "&pane=1" }
	pinned := false
	var prevID, nextID string
	if msgs, lerr := a.vayuMail.ListFolder(user, folder); lerr == nil {
		for i, mm := range msgs {
			if mm.ID == id {
				pinned = mm.Flagged
				if i > 0 {
					prevID = msgs[i-1].ID
				}
				if i+1 < len(msgs) {
					nextID = msgs[i+1].ID
				}
				break
			}
		}
	}
	q := "user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(id)
	replyLink := "/os/vayumail/compose?reply=1&" + q
	forwardLink := "/os/vayumail/compose?forward=1&" + q

	pm := vmail.ParseMessage(raw)
	subj := strings.TrimSpace(pm.Subject)
	if subj == "" {
		subj = "(no subject)"
	}

	var card strings.Builder
	card.WriteString(`<div class="card vm-reader">`)

	// Top bar: back/close + prev/next navigation.
	card.WriteString(`<div class="vm-reader-top">`)
	if pane {
		card.WriteString(`<button type="button" class="btn btn--ghost btn--sm" hx-get="/os/vayumail/inbox/readpane" hx-target="#vm-readpane" hx-swap="innerHTML" title="Close">✕ Close</button>`)
	} else {
		card.WriteString(`<a class="btn btn--ghost btn--sm" href="` + back + `">← ` + html.EscapeString(folder) + `</a>`)
	}
	card.WriteString(`<span class="vm-reader-nav">`)
	// Reading-pane comfort controls: expand to a full-screen overlay
	// (toggled by delegated JS; ESC or Close collapses) and print (a
	// delegated window.print with @media print rules that emit only the
	// open reader). CSP-safe: data-attributes, no inline handlers.
	if pane {
		card.WriteString(`<button type="button" class="btn btn--xs" data-vm-expand title="Toggle full view" aria-label="Toggle full view">⛶</button>`)
	}
	card.WriteString(`<button type="button" class="btn btn--xs" data-vm-print title="Print message" aria-label="Print message">🖨</button>`)
	navBtn := func(mid, glyph, label string) {
		if mid == "" {
			card.WriteString(`<span class="btn btn--xs" aria-disabled="true">` + glyph + `</span>`)
			return
		}
		if pane {
			card.WriteString(`<button type="button" class="btn btn--xs" hx-get="` + paneURL(mid) + `" hx-target="#vm-readpane" hx-swap="innerHTML" title="` + label + `" aria-label="` + label + `">` + glyph + `</button>`)
		} else {
			card.WriteString(`<a class="btn btn--xs" href="` + msgURL(mid) + `" title="` + label + `" aria-label="` + label + `">` + glyph + `</a>`)
		}
	}
	navBtn(prevID, "‹", "Previous message")
	navBtn(nextID, "›", "Next message")
	card.WriteString(`</span></div>`)

	// Action toolbar. Page mode uses admin-os-mail.js (data-mail-*). Pane mode
	// is pure HTMX: actions POST to the pane endpoint, which swaps #vm-readpane
	// and fires HX-Trigger:vm-mail-changed so the list refreshes.
	if pane {
		paneVals := func(extra ...string) string {
			args := append([]string{"user", user, "folder", folder, "id", id}, extra...)
			return hxVals(args...)
		}
		hxPost := ` hx-post="/os/vayumail/message/pane-action" hx-target="#vm-readpane" hx-swap="innerHTML" `
		card.WriteString(`<div class="vm-actions">`)
		card.WriteString(`<a class="btn btn--primary btn--sm" href="` + replyLink + `">↩ Reply</a>`)
		card.WriteString(`<a class="btn btn--sm" href="` + forwardLink + `">↪ Forward</a>`)
		if received {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("mark", "unread") + `>✉ Mark unread</button>`)
		}
		if pinned {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("pin", "0") + `>📌 Unpin</button>`)
		} else {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("pin", "1") + `>📌 Pin</button>`)
		}
		if !strings.EqualFold(folder, "Junk") {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("to", "Junk") + `>⚠ Junk</button>`)
		}
		if !strings.EqualFold(folder, "Trash") {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("to", "Trash") + `>🗑 Trash</button>`)
		} else {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("to", "Inbox") + `>↧ Restore</button>`)
		}
		// Snooze: hide until later; the sweeper resurfaces it unread. Only for
		// received folders (the engine rejects Sent/Drafts/Snoozed anyway).
		if received && !strings.EqualFold(folder, "Snoozed") {
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("snooze", "tomorrow") + ` title="Snooze until tomorrow 8:00">⏰ Tomorrow</button>`)
			card.WriteString(`<button type="button" class="btn btn--sm"` + hxPost + paneVals("snooze", "nextweek") + ` title="Snooze until Monday 8:00">⏰ Next week</button>`)
		}
		card.WriteString(`<button type="button" class="btn btn--sm btn--danger"` + hxPost + paneVals("delete", "1") + ` hx-confirm="Permanently delete this message?">🗑 Delete</button>`)
		card.WriteString(`</div>`)
	} else {
		// Emit only the raw next-message id (not a full URL): the client rebuilds
		// the back/next navigation targets from these individual components with
		// encodeURIComponent + a literal path prefix, so no full URL is ever read
		// from a DOM attribute and handed to location (closes the DOM-XSS finding
		// without any behaviour change — the URLs are identical).
		nextAttr := ""
		if nextID != "" {
			nextAttr = `" data-next-id="` + html.EscapeString(nextID)
		}
		card.WriteString(`<div class="vm-actions" data-mail-actions data-user="` + html.EscapeString(user) + `" data-folder="` + html.EscapeString(folder) + `" data-id="` + html.EscapeString(id) + nextAttr + `">`)
		card.WriteString(`<a class="btn btn--primary btn--sm" href="` + replyLink + `">↩ Reply</a>`)
		card.WriteString(`<a class="btn btn--sm" href="` + forwardLink + `">↪ Forward</a>`)
		if received {
			card.WriteString(`<button type="button" class="btn btn--sm" data-mail-mark="unread">✉ Mark unread</button>`)
		}
		if pinned {
			card.WriteString(`<button type="button" class="btn btn--sm" data-mail-pin="0">📌 Unpin</button>`)
		} else {
			card.WriteString(`<button type="button" class="btn btn--sm" data-mail-pin="1">📌 Pin</button>`)
		}
		if !strings.EqualFold(folder, "Junk") {
			card.WriteString(`<button type="button" class="btn btn--sm" data-mail-move="Junk">⚠ Junk</button>`)
		}
		if !strings.EqualFold(folder, "Trash") {
			card.WriteString(`<button type="button" class="btn btn--sm" data-mail-move="Trash">🗑 Trash</button>`)
		} else {
			card.WriteString(`<button type="button" class="btn btn--sm" data-mail-move="Inbox">↧ Restore</button>`)
		}
		card.WriteString(`<span class="vm-move"><select class="input input--sm" data-mail-move-select aria-label="Move to folder"><option value="">Move to…</option>`)
		for _, f := range vmail.StandardFolders {
			// Snoozed is excluded: only the snooze action files there.
			if strings.EqualFold(f, folder) || strings.EqualFold(f, "Snoozed") {
				continue
			}
			card.WriteString(`<option value="` + html.EscapeString(f) + `">` + html.EscapeString(f) + `</option>`)
		}
		card.WriteString(`</select></span>`)
		card.WriteString(`<button type="button" class="btn btn--sm" data-mail-print>🖨 Print</button>`)
		card.WriteString(`<button type="button" class="btn btn--sm btn--danger" data-mail-delete>🗑 Delete</button></div>`)
	}

	// Header card: subject + PGP badge, sender avatar, addresses and date.
	fromName, fromAddr := mailParseFrom(pm.From)
	if fromName == "" {
		fromName = fromAddr
	}
	card.WriteString(`<div class="vm-msg-head"><div class="vm-msg-subject">` + html.EscapeString(subj) + mailPGPBadge(raw) + `</div>`)
	card.WriteString(`<div class="vm-msg-from-row">` + mailAvatar(pm.From) + `<div class="vm-msg-from-meta">`)
	card.WriteString(`<div class="vm-msg-fromname"><strong>` + html.EscapeString(fromName) + `</strong>`)
	if fromAddr != "" && fromAddr != fromName {
		card.WriteString(` <span class="muted text-sm">&lt;` + html.EscapeString(fromAddr) + `&gt;</span>`)
	}
	card.WriteString(`</div>`)
	metaRow := func(label, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		card.WriteString(`<div class="muted text-sm"><strong>` + label + `:</strong> ` + html.EscapeString(value) + `</div>`)
	}
	metaRow("To", pm.To)
	metaRow("Cc", pm.Cc)
	metaRow("Date", pm.Date)
	card.WriteString(`</div></div></div>`)

	// Attachments.
	if len(pm.Attachments) > 0 {
		card.WriteString(`<div class="vm-attach"><div class="text-sm muted">📎 ` + itoaSafe(len(pm.Attachments)) + ` attachment(s)</div><div class="vm-attach-list">`)
		for _, att := range pm.Attachments {
			dl := "/os/vayumail/attachment?user=" + qparam(user) + "&folder=" + qparam(folder) + "&id=" + qparam(id) + "&idx=" + itoaSafe(att.Index)
			card.WriteString(`<a class="vm-attach-chip" href="` + dl + `" download><span class="vm-attach-ico">📄</span><span class="vm-attach-name">` + html.EscapeString(att.Filename) + `</span><span class="vm-attach-size">` + html.EscapeString(humanBytes(att.Size)) + `</span></a>`)
		}
		card.WriteString(`</div></div>`)
	}

	// Body: decoded text/plain (with collapsible quote) → sanitised HTML → raw.
	card.WriteString(`<div class="vm-msg-body">`)
	switch {
	case strings.TrimSpace(pm.Text) != "":
		main, quoted := splitQuoted(pm.Text)
		card.WriteString(`<pre class="vm-pre">` + html.EscapeString(main) + `</pre>`)
		if quoted != "" {
			card.WriteString(`<details class="vm-quote"><summary>Show quoted text</summary><pre class="vm-pre vm-pre--quoted">` + html.EscapeString(quoted) + `</pre></details>`)
		}
	case strings.TrimSpace(pm.HTML) != "":
		card.WriteString(`<div class="vm-html">` + mailHTMLPolicy.Sanitize(pm.HTML) + `</div>`)
	default:
		card.WriteString(`<pre class="vm-pre">` + html.EscapeString(string(raw)) + `</pre>`)
	}
	card.WriteString(`</div>`)

	// Raw source. Page mode toggles via admin-os-mail.js; pane mode uses a
	// native <details> so it needs no page-load JS.
	if pane {
		card.WriteString(`<details class="vm-rawwrap"><summary class="btn btn--sm btn--ghost">View raw source</summary><pre class="vm-pre vm-raw">` + html.EscapeString(string(raw)) + `</pre></details>`)
	} else {
		card.WriteString(`<div class="vm-rawwrap"><button class="btn btn--sm btn--ghost" type="button" data-mail-raw-toggle>View raw source</button>`)
		card.WriteString(`<pre class="vm-pre vm-raw" data-mail-raw hidden>` + html.EscapeString(string(raw)) + `</pre></div>`)
	}
	card.WriteString(`</div>`)
	return card.String(), true
}

// handleVayuOSMessagePaneAction applies a reader action from the split reading
// pane and returns the pane's next state (an updated card, or the empty
// placeholder when the message leaves the view), firing HX-Trigger:vm-mail-changed
// so the message list refreshes in place. Pure HTMX — no page-load JS.
func (a *App) handleVayuOSMessagePaneAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeOSHTML(w, vayuReadpaneEmpty("VayuMail is not active."))
		return
	}
	_ = r.ParseForm()
	// Sanitise at read: user/folder/id are rendered back into the reader-pane
	// card (writeOSHTML) below, so raw form values would be a reflected-XSS
	// sink. The sanitisers are no-ops on valid input but apply html.EscapeString.
	user := sanitizeMailLocalPart(strings.TrimSpace(r.FormValue("user")))
	folder := sanitizeMailFolder(strings.TrimSpace(r.FormValue("folder")))
	id := sanitizeMailID(strings.TrimSpace(r.FormValue("id")))
	if !a.isAdminRequest(r) {
		local, _ := a.ownMailbox(r)
		if local == "" || !strings.EqualFold(local, user) {
			w.WriteHeader(http.StatusForbidden)
			writeOSHTML(w, vayuReadpaneEmpty("You can only manage your own mailbox."))
			return
		}
	}
	if user == "" || id == "" {
		writeOSHTML(w, vayuReadpaneEmpty("Message not available."))
		return
	}
	// Every branch below changes what the list shows, so refresh it in place.
	w.Header().Set("HX-Trigger", "vm-mail-changed")

	switch {
	case r.FormValue("snooze") != "":
		until := snoozeUntil(r.FormValue("snooze"))
		if err := a.vayuMail.Snooze(user, folder, id, until); err != nil {
			writeOSHTML(w, vayuReadpaneEmpty("Could not snooze: "+err.Error()))
			return
		}
		writeOSHTML(w, vayuReadpaneEmpty("Snoozed — wakes "+until.Local().Format("Mon 15:04")+"."))
	case r.FormValue("delete") == "1":
		_ = a.vayuMail.DeleteMessage(user, folder, id)
		writeOSHTML(w, vayuReadpaneEmpty("Message deleted."))
	case strings.TrimSpace(r.FormValue("to")) != "":
		to := strings.TrimSpace(r.FormValue("to"))
		_ = a.vayuMail.MoveMessage(user, id, folder, to)
		writeOSHTML(w, vayuReadpaneEmpty("Moved to "+html.EscapeString(to)+"."))
	case r.FormValue("mark") == "unread":
		_, _ = a.vayuMail.MarkUnread(user, folder, id)
		// Gmail-style: marking unread closes the reader so the bold row stands out.
		writeOSHTML(w, vayuReadpaneEmpty("Marked unread."))
	case r.FormValue("pin") == "1" || r.FormValue("pin") == "0":
		if nid, err := a.vayuMail.SetPinned(user, folder, id, r.FormValue("pin") == "1"); err == nil && nid != "" {
			id = nid
		}
		if card, ok := a.vayuReaderCard(user, folder, id, true); ok {
			writeOSHTML(w, card)
		} else {
			writeOSHTML(w, vayuReadpaneEmpty(""))
		}
	default:
		if card, ok := a.vayuReaderCard(user, folder, id, true); ok {
			writeOSHTML(w, card)
		} else {
			writeOSHTML(w, vayuReadpaneEmpty(""))
		}
	}
}

// handleVayuOSReadpane returns the empty reading-pane placeholder — the pane's
// initial split-view state and what its Close button restores.
func (a *App) handleVayuOSReadpane(w http.ResponseWriter, r *http.Request) {
	writeOSHTML(w, vayuReadpaneEmpty(""))
}

// snoozeUntil maps a snooze preset to its wake time: "later" (+4h),
// "tomorrow" (next day 08:00) or "nextweek" (next Monday 08:00).
func snoozeUntil(preset string) time.Time {
	now := time.Now()
	switch preset {
	case "tomorrow":
		d := now.AddDate(0, 0, 1)
		return time.Date(d.Year(), d.Month(), d.Day(), 8, 0, 0, 0, time.Local)
	case "nextweek":
		d := now.AddDate(0, 0, 1)
		for d.Weekday() != time.Monday {
			d = d.AddDate(0, 0, 1)
		}
		return time.Date(d.Year(), d.Month(), d.Day(), 8, 0, 0, 0, time.Local)
	default: // "later"
		return now.Add(4 * time.Hour)
	}
}

// cfgDomain is a small helper for templates.
func (a *App) cfgDomain() string {
	if a.vayuMail != nil {
		return a.vayuMail.Config().Domain
	}
	return ""
}

// handleVayuOSSent lists recent outbound messages from the delivery queue, with
// per-message Resend/Delete and a Retry-all-failed action. The body is an HTMX
// fragment that auto-refreshes (delivery state changes on its own) and re-renders
// in place after an action — no full-page reload.
func (a *App) handleVayuOSSent(w http.ResponseWriter, r *http.Request) {
	nonce := render.CSPNonce(r)
	cfg := a.getOSSettings(r.Context())
	// A CSRF cookie so the Resend/Delete/Retry HTMX POSTs pass the middleware.
	if token := auth.GenerateCSRFToken(); token != "" {
		http.SetCookie(w, &http.Cookie{Name: "vp_csrf", Value: token, Path: "/", SameSite: http.SameSiteStrictMode, HttpOnly: false, Secure: csrfCookieSecure(), MaxAge: 3600})
	}
	var body strings.Builder
	body.WriteString(`<div class="page-header"><h1>Outbox</h1><span class="muted text-sm">Outbound delivery queue — auto-retries with backoff until sent · one-click Resend</span></div>`)
	body.WriteString(vayuosNav("outbox", a.isAdminRequest(r)))
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		body.WriteString(`<div class="empty-state">VayuMail is inactive. Set <code>DOMAIN</code> to activate outbound delivery.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Outbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	// The outbound delivery queue is server-wide; non-admins see their own sent
	// mail in their mailbox's Sent folder instead.
	if !a.isAdminRequest(r) {
		body.WriteString(`<div class="empty-state">Your sent messages are in your mailbox under <a href="/os/vayumail/inbox?folder=Sent">Mailbox → Sent</a>. The server-wide delivery queue is visible to administrators only.</div>`)
		writeOSHTML(w, adminOSLayout(nonce, "Outbox", "vayuos", cfg, htmpl.HTML(body.String())))
		return
	}
	body.WriteString(`<div id="vm-outbox-body" hx-get="/os/vayumail/outbox/fragment" hx-trigger="every 15s" hx-swap="innerHTML">`)
	body.WriteString(a.vayuOutboxBody(r.Context()))
	body.WriteString(`</div>`)
	writeOSHTML(w, adminOSLayout(nonce, "Outbox", "vayuos", cfg, htmpl.HTML(body.String())))
}

// vayuOutboxBody renders the outbound-queue table (counters + per-message
// Resend/Delete + Retry-all-failed). Returned on page load and as the HTMX
// fragment after an action or on the auto-refresh poll.
func (a *App) vayuOutboxBody(ctx context.Context) string {
	var b strings.Builder
	sent, err := a.vayuMail.Sent(ctx, 100)
	if err != nil {
		b.WriteString(`<div class="empty-state">Could not read outbound queue: ` + html.EscapeString(err.Error()) + `</div>`)
		return b.String()
	}
	var pending, failed, delivered int
	for _, s := range sent {
		switch s.State {
		case "pending":
			pending++
		case "failed":
			failed++
		case "delivered":
			delivered++
		}
	}
	b.WriteString(`<div class="vm-row vm-row--end">`)
	b.WriteString(`<span class="muted text-sm vm-grow">` + itoaSafe(pending) + ` pending · ` + itoaSafe(failed) + ` failed · ` + itoaSafe(delivered) + ` delivered <span class="text-xs">(latest ` + itoaSafe(len(sent)) + `)</span></span>`)
	b.WriteString(`<button type="button" class="btn btn--sm" hx-get="/os/vayumail/outbox/fragment" hx-target="#vm-outbox-body" hx-swap="innerHTML">↻ Refresh</button>`)
	if failed > 0 {
		b.WriteString(`<button type="button" class="btn btn--sm btn--primary" hx-post="/os/vayumail/outbox/action" hx-vals='{"action":"retry-all"}' hx-target="#vm-outbox-body" hx-swap="innerHTML">Retry all failed (` + itoaSafe(failed) + `)</button>`)
	}
	b.WriteString(`</div>`)

	b.WriteString(`<div class="card"><div class="card-title">Recent outbound</div><div class="table-wrap"><table class="table"><thead><tr><th>To</th><th>Subject</th><th>Status</th><th>Tries</th><th>Last error</th><th>When</th><th></th></tr></thead><tbody>`)
	if len(sent) == 0 {
		b.WriteString(`<tr><td colspan="7" class="muted">Nothing sent yet. Mail sent through VayuMail (DKIM-signed, direct-to-MX) appears here; VayuPress keeps retrying with backoff until it sends.</td></tr>`)
	}
	for _, s := range sent {
		subj := s.Subject
		if subj == "" {
			subj = "(no subject)"
		}
		badge := `<span class="badge badge--ok">delivered</span>`
		switch s.State {
		case "failed":
			badge = `<span class="badge badge--warn">failed</span>`
		case "pending":
			badge = `<span class="badge">pending</span>`
		}
		when := s.CreatedAt
		if len(when) > 19 {
			when = when[:19]
		}
		lastErr := s.LastError
		if len(lastErr) > 90 {
			lastErr = lastErr[:90] + "…"
		}
		id := strconv.FormatInt(s.ID, 10)
		actions := ""
		if s.State == "failed" || s.State == "pending" {
			actions += `<button type="button" class="btn btn--xs btn--primary" hx-post="/os/vayumail/outbox/action" hx-vals='{"action":"resend","id":"` + id + `"}' hx-target="#vm-outbox-body" hx-swap="innerHTML">Resend</button> `
		}
		actions += `<button type="button" class="btn btn--xs btn--danger" hx-post="/os/vayumail/outbox/action" hx-vals='{"action":"delete","id":"` + id + `"}' hx-target="#vm-outbox-body" hx-swap="innerHTML">Delete</button>`
		b.WriteString(`<tr><td class="text-sm">` + html.EscapeString(strings.Join(s.To, ", ")) + `</td><td>` + html.EscapeString(subj) + `</td><td>` + badge + `</td><td class="text-sm">` + itoaSafe(s.Attempts) + `</td><td class="muted text-xs">` + html.EscapeString(lastErr) + `</td><td class="muted text-sm">` + html.EscapeString(when) + `</td><td class="row-actions">` + actions + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div></div>`)
	return b.String()
}

// handleVayuOSOutboxFragment returns the outbox body for HTMX auto-refresh.
func (a *App) handleVayuOSOutboxFragment(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled || !a.isAdminRequest(r) {
		writeOSFragment(w, `<div class="empty-state">Outbox unavailable.</div>`)
		return
	}
	writeOSFragment(w, a.vayuOutboxBody(r.Context()))
}

// handleVayuOSOutboxAction performs a Resend / Delete / Retry-all on the
// outbound queue and returns the refreshed outbox body (HTMX swaps it in place).
func (a *App) handleVayuOSOutboxAction(w http.ResponseWriter, r *http.Request) {
	if a.vayuMail == nil || !a.vayuMail.Config().Enabled {
		writeAPIError(w, r, http.StatusServiceUnavailable, "mail-disabled", "VayuMail is not active", "")
		return
	}
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	switch strings.TrimSpace(r.PostFormValue("action")) {
	case "resend":
		id, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("id")), 10, 64)
		if id <= 0 {
			writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id required", "")
			return
		}
		if err := a.vayuMail.ResendQueued(r.Context(), id); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "resend-failed", err.Error(), "")
			return
		}
	case "delete":
		id, _ := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("id")), 10, 64)
		if id <= 0 {
			writeAPIError(w, r, http.StatusBadRequest, "bad-request", "id required", "")
			return
		}
		if err := a.vayuMail.DeleteQueued(r.Context(), id); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "delete-failed", err.Error(), "")
			return
		}
	case "retry-all":
		if _, err := a.vayuMail.RetryAllFailed(r.Context()); err != nil {
			writeAPIError(w, r, http.StatusInternalServerError, "retry-failed", err.Error(), "")
			return
		}
	default:
		writeAPIError(w, r, http.StatusBadRequest, "bad-request", "unknown action", "")
		return
	}
	writeOSFragment(w, a.vayuOutboxBody(r.Context()))
}

func itoaSafe(n int) string { return fmt.Sprintf("%d", n) }
