package mail

import (
	"context"
	"crypto/tls"
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
