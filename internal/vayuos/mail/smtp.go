package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"
)

// NewMXDeliverer returns a DeliverFunc that delivers mail directly to each
// recipient domain's MX hosts (no third-party relay — full sovereignty), using
// opportunistic STARTTLS. heloHost is announced in EHLO/HELO.
func NewMXDeliverer(heloHost string, timeout time.Duration) DeliverFunc {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return func(ctx context.Context, from string, to []string, raw []byte) error {
		byDomain := map[string][]string{}
		for _, addr := range to {
			d := domainOf(addr)
			if d == "" {
				return fmt.Errorf("vayumail: bad recipient %q", addr)
			}
			byDomain[d] = append(byDomain[d], addr)
		}
		for domain, rcpts := range byDomain {
			if err := deliverToDomain(ctx, heloHost, from, domain, rcpts, raw, timeout); err != nil {
				return err
			}
		}
		return nil
	}
}

func domainOf(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(addr[at+1:]))
}

func deliverToDomain(ctx context.Context, heloHost, from, domain string, rcpts []string, raw []byte, timeout time.Duration) error {
	hosts, err := mxHosts(domain)
	if err != nil || len(hosts) == 0 {
		return fmt.Errorf("vayumail: no MX for %s: %v", domain, err)
	}
	var lastErr error
	for _, host := range hosts {
		if err := deliverViaHost(ctx, heloHost, from, host, rcpts, raw, timeout); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("vayumail: delivery to %s failed: %w", domain, lastErr)
}

func mxHosts(domain string) ([]string, error) {
	mxs, err := net.LookupMX(domain)
	if err == nil && len(mxs) > 0 {
		sort.Slice(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
		hosts := make([]string, 0, len(mxs))
		for _, mx := range mxs {
			hosts = append(hosts, strings.TrimSuffix(mx.Host, "."))
		}
		return hosts, nil
	}
	// Fallback: implicit MX is the domain itself (RFC 5321 §5.1).
	return []string{domain}, nil
}

func deliverViaHost(ctx context.Context, heloHost, from, host string, rcpts []string, raw []byte, timeout time.Duration) error {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "25"))
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()

	if heloHost == "" {
		heloHost = "localhost"
	}
	if err := c.Hello(heloHost); err != nil {
		return err
	}
	// Opportunistic STARTTLS.
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range rcpts {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(raw); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// NewRelayDeliverer returns a DeliverFunc that sends every queued message
// through a configured authenticated SMTP smarthost relay (submission) instead
// of direct-to-MX. The relay's established IP reputation and policies then
// govern final inbox placement — the pragmatic remedy for a fresh self-hosted
// IP with no sending history. Everything else stays sovereign: VayuMail still
// DKIM-signs the message with the domain key before it is queued, so DMARC
// alignment via DKIM is preserved end-to-end. All recipients are sent in a
// single submission transaction (relays accept any destination domain).
func NewRelayDeliverer(cfg Config, heloHost string, timeout time.Duration) DeliverFunc {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return func(ctx context.Context, from string, to []string, raw []byte) error {
		return relayDeliver(ctx, cfg, heloHost, from, to, raw, timeout)
	}
}

func relayDeliver(ctx context.Context, cfg Config, heloHost, from string, to []string, raw []byte, timeout time.Duration) error {
	if len(to) == 0 {
		return errors.New("vayumail: relay: no recipients")
	}
	addr := cfg.RelayAddr()
	implicitTLS := cfg.RelayPort == 465

	dialer := net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.DialWithDialer(&dialer, "tcp", addr, &tls.Config{ServerName: cfg.RelayHost, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("vayumail: relay dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	c, err := smtp.NewClient(conn, cfg.RelayHost)
	if err != nil {
		return err
	}
	defer c.Close()

	if heloHost == "" {
		heloHost = "localhost"
	}
	if err := c.Hello(heloHost); err != nil {
		return err
	}

	if !implicitTLS {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: cfg.RelayHost, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
		} else if cfg.RelayRequireTLS {
			return fmt.Errorf("vayumail: relay %s offers no STARTTLS but RelayRequireTLS is set", addr)
		}
	}

	if cfg.RelayUsername != "" {
		if err := c.Auth(relayAuth(cfg.RelayUsername, cfg.RelayPassword, cfg.RelayHost, c)); err != nil {
			return fmt.Errorf("vayumail: relay auth: %w", err)
		}
	}

	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(raw); err != nil {
		_ = wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// relayAuth selects an SMTP AUTH mechanism the relay advertises, preferring the
// standard PLAIN and falling back to the widely-deployed LOGIN mechanism (e.g.
// Office 365). Both refuse to send credentials over an unencrypted, non-local
// connection.
func relayAuth(username, password, host string, c *smtp.Client) smtp.Auth {
	_, mechs := c.Extension("AUTH")
	up := strings.ToUpper(mechs)
	if !strings.Contains(up, "PLAIN") && strings.Contains(up, "LOGIN") {
		return &loginAuth{username: username, password: password}
	}
	return smtp.PlainAuth("", username, password, host)
}

// loginAuth implements the non-standard SMTP "LOGIN" SASL mechanism for relays
// that offer LOGIN but not PLAIN.
type loginAuth struct {
	username, password string
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhostName(server.Name) {
		return "", nil, errors.New("vayumail: refusing LOGIN auth over unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(strings.TrimSpace(string(fromServer)), ":")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	default:
		return nil, fmt.Errorf("vayumail: unexpected LOGIN challenge %q", fromServer)
	}
}

func isLocalhostName(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}
