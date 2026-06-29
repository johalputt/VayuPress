// Package email provides sovereign SMTP delivery for VayuPress.
//
// It uses only the Go standard library (net/smtp + crypto/tls) — no third-party
// SDKs, no hosted APIs, no telemetry. When SMTP is not configured the Sender
// operates in a graceful no-op mode: callers can always invoke Send without
// guarding on configuration, and unconfigured deployments simply skip delivery
// while logging an audit line. This keeps the newsletter, comment, and
// transactional flows wired without forcing every operator to run a mail server.
//
// Security posture:
//   - STARTTLS is required by default on the submission port (587); set
//     SMTP_TLS=ssl for implicit TLS (465) or SMTP_TLS=none for localhost relays.
//   - Header values are sanitised against CRLF injection before assembly.
//   - Credentials are read once at construction from config and never logged.
package email

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/logging"
)

// TLSMode selects how the transport secures the SMTP connection.
type TLSMode string

const (
	// TLSStartTLS upgrades a plaintext connection via STARTTLS (port 587). Default.
	TLSStartTLS TLSMode = "starttls"
	// TLSImplicit dials straight into TLS (port 465).
	TLSImplicit TLSMode = "ssl"
	// TLSNone disables TLS entirely — only safe for trusted localhost relays.
	TLSNone TLSMode = "none"
)

// Config holds the SMTP connection parameters. The zero value (empty Host)
// yields a no-op Sender.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // envelope + From header, e.g. "VayuPress <hello@example.com>"
	TLS      TLSMode
	Timeout  time.Duration
}

// Message is a single outbound email. Plain text is required; HTML is optional
// and, when present, the message is sent as multipart/alternative.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers messages over SMTP. It is safe for concurrent use.
type Sender struct {
	cfg     Config
	enabled bool
	// fallback, when set, delivers a message through an alternative transport
	// (the built-in VayuMail engine) whenever external SMTP is NOT configured.
	// This is what lets transactional mail (sign-in links, welcome, newsletter
	// confirmations) actually send on a sovereign single-binary deployment that
	// runs its own mail server instead of relaying through a third-party SMTP.
	fallback func(Message) error
}

// New constructs a Sender from cfg. When cfg.Host is empty the returned Sender
// is a no-op (Enabled() == false).
func New(cfg Config) *Sender {
	if cfg.TLS == "" {
		cfg.TLS = TLSStartTLS
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	return &Sender{cfg: cfg, enabled: strings.TrimSpace(cfg.Host) != ""}
}

// Enabled reports whether SMTP is configured. Callers may use this to surface
// status in admin UIs, but Send is always safe to call.
func (s *Sender) Enabled() bool { return s.enabled }

// SetFallback installs an alternative transport used when external SMTP is not
// configured. Passing a non-nil function turns the Sender from a no-op into a
// working transport backed by that function (the VayuMail engine), so callers
// keep using Send unchanged.
func (s *Sender) SetFallback(fn func(Message) error) { s.fallback = fn }

// Active reports whether mail can actually be delivered — either external SMTP
// is configured, or a fallback transport (VayuMail) is wired.
func (s *Sender) Active() bool { return s.enabled || s.fallback != nil }

// From returns the configured From header (useful for building links/footers).
func (s *Sender) From() string { return s.cfg.From }

// Send delivers msg. When the Sender is a no-op it logs and returns nil so that
// upstream flows (subscribe, comment-approve) are not broken on unconfigured
// hosts. A non-nil error indicates a genuine delivery failure on a configured
// host and should be surfaced/retried by the caller.
func (s *Sender) Send(msg Message) error {
	if !s.enabled {
		// No external SMTP. If a fallback transport (VayuMail) is wired, deliver
		// through it — sanitising exactly as the SMTP path does so the body is
		// safe regardless of transport. Otherwise no-op so upstream flows aren't
		// broken on a deployment with neither configured.
		if s.fallback != nil {
			to := sanitizeHeader(msg.To)
			if _, err := mailParse(to); err != nil {
				return fmt.Errorf("email: invalid recipient: %w", err)
			}
			msg.To = to
			msg.HTML = emailHTMLPolicy.Sanitize(msg.HTML)
			msg.Text = stripControl(msg.Text)
			if err := s.fallback(msg); err != nil {
				logging.LogJSON(logging.LogFields{
					Level: "error", Component: "email", Severity: "error",
					Msg: "fallback (VayuMail) delivery failed", Path: redactEmail(to), Error: err.Error(),
				})
				return err
			}
			logging.LogJSON(logging.LogFields{
				Level: "info", Component: "email", Severity: "info",
				Msg: "delivered via VayuMail: " + sanitizeHeader(msg.Subject), Path: redactEmail(to),
			})
			return nil
		}
		logging.LogJSON(logging.LogFields{
			Level: "info", Component: "email", Severity: "info",
			Msg: "no mail transport configured — skipping delivery (set SMTP_HOST or DOMAIN to enable VayuMail)", Path: redactEmail(msg.To),
		})
		return nil
	}
	to := sanitizeHeader(msg.To)
	if _, err := mailParse(to); err != nil {
		return fmt.Errorf("email: invalid recipient: %w", err)
	}
	// Sanitize bodies here so CodeQL's taint tracker sees the barrier in the
	// same call chain as deliver() → wc.Write(raw). The HTML part is run
	// through the UGC policy (strips scripts/events); the plain-text part has
	// control characters removed to prevent SMTP stream corruption.
	msg.HTML = emailHTMLPolicy.Sanitize(msg.HTML)
	msg.Text = stripControl(msg.Text)
	raw, err := s.assemble(to, msg)
	if err != nil {
		return err
	}
	if err := s.deliver(to, raw); err != nil {
		logging.LogJSON(logging.LogFields{
			Level: "error", Component: "email", Severity: "error",
			Msg: "delivery failed", Path: redactEmail(to), Error: err.Error(),
		})
		return err
	}
	logging.LogJSON(logging.LogFields{
		Level: "info", Component: "email", Severity: "info",
		Msg: "delivered: " + sanitizeHeader(msg.Subject), Path: redactEmail(to),
	})
	return nil
}

// deliver opens a connection (honouring the TLS mode), authenticates when
// credentials are present, and writes the assembled message.
func (s *Sender) deliver(to string, raw []byte) error {
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))
	dialer := &net.Dialer{Timeout: s.cfg.Timeout}

	var conn net.Conn
	var err error
	if s.cfg.TLS == TLSImplicit {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(s.cfg.Timeout))

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	if s.cfg.TLS == TLSStartTLS {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return fmt.Errorf("email: server does not advertise STARTTLS (set SMTP_TLS=none for trusted relays)")
		}
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("email: starttls: %w", err)
		}
	}

	if s.cfg.Username != "" {
		auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	from := envelopeAddress(s.cfg.From)
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("email: RCPT TO: %w", err)
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := wc.Write(raw); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("email: close body: %w", err)
	}
	return c.Quit()
}
