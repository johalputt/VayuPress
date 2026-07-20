package mail

// reserved.go — reserved vs. self-claimable mail localparts.
//
// Role-critical and administrative addresses (RFC 2142 plus the common admin /
// infrastructure / business-role names) are never claimable by a reader-member
// self-provisioning their included mailbox, so a paying member can only take a
// *generic* address — the operator keeps postmaster@, abuse@, admin@, security@
// and friends as sellable/administrative inventory. Operator-created mailboxes
// are NOT bound by this (the operator owns the domain); the guard applies only to
// the member self-service claim path.

import (
	"context"
	"strings"
)

// reservedLocalparts is the set of localparts a member may not self-claim.
var reservedLocalparts = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range []string{
		// RFC 2142 (required + common role mailboxes)
		"postmaster", "abuse", "hostmaster", "webmaster", "usenet", "news",
		"uucp", "ftp", "www", "noc", "security", "info", "marketing", "sales",
		"support", "help", "contact",
		// administrative / infrastructure
		"admin", "administrator", "root", "sysadmin", "hostadmin", "mailadmin",
		"mail", "mailer", "mailer-daemon", "daemon", "no-reply", "noreply",
		"do-not-reply", "donotreply", "bounce", "bounces", "spam", "ham",
		"dmarc", "dkim", "spf", "autoconfig", "autodiscover", "wpad",
		"majordomo", "listserv", "list", "lists", "owner", "request",
		"nobody", "anonymous", "null", "void", "test",
		// service names often used as localparts
		"ns", "ns1", "ns2", "mx", "smtp", "imap", "pop", "pop3", "dns",
		"ssl", "tls", "vpn", "api", "dev", "staging", "cpanel", "whm",
		// business roles the operator usually keeps
		"billing", "accounts", "account", "payments", "invoice", "invoices",
		"legal", "privacy", "gdpr", "dpo", "compliance", "hr", "jobs",
		"careers", "press", "media", "office", "team", "staff", "hello",
	} {
		m[s] = true
	}
	return m
}()

// IsReservedLocalpart reports whether a localpart is reserved for the operator
// and therefore must not be self-claimed by a member.
func IsReservedLocalpart(local string) bool {
	return reservedLocalparts[strings.ToLower(strings.TrimSpace(local))]
}

// ValidLocalpart reports whether local is a well-formed, safe mail localpart for
// self-service claiming: 1–64 chars, lowercase a–z / 0–9 and the separators
// . _ - +, must start and end with an alphanumeric, and no two dots in a row.
func ValidLocalpart(local string) bool {
	local = strings.ToLower(strings.TrimSpace(local))
	n := len(local)
	if n == 0 || n > 64 {
		return false
	}
	prevDot := false
	for i, r := range local {
		alnum := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		switch {
		case alnum:
			prevDot = false
		case r == '.' || r == '_' || r == '-' || r == '+':
			if i == 0 || i == n-1 {
				return false // no leading/trailing separators
			}
			if r == '.' && prevDot {
				return false // no consecutive dots
			}
			prevDot = r == '.'
		default:
			return false
		}
	}
	return true
}

// Exists reports whether an address is already taken — as a mailbox account or a
// receive-only alias — so a member claim can be rejected before provisioning.
func (s *AccountStore) Exists(ctx context.Context, email string) bool {
	if s.db == nil {
		return false
	}
	email = normEmail(email)
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_accounts WHERE email=?`, email).Scan(&n)
	if n > 0 {
		return true
	}
	var a int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM vayumail_aliases WHERE alias=?`, email).Scan(&a)
	return a > 0
}
