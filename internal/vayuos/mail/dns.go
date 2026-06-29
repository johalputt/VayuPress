package mail

import (
	"context"
	"net"
	"strings"
	"time"
)

// DNSRecord is a single record VayuOS asks the operator (or a provider API) to
// publish for mail to authenticate correctly.
type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"`
}

// PlannedRecords returns the MX/SPF/DKIM/DMARC records VayuMail expects for the
// configured domain. dkimTXT is the value from DKIM.PublicTXT(); dkimName is
// DKIM.RecordName().
func PlannedRecords(cfg Config, dkimName, dkimTXT string) []DNSRecord {
	recs := []DNSRecord{
		{Type: "MX", Name: cfg.Domain + ".", Value: cfg.Hostname + ".", Priority: 10},
	}
	if cfg.SPFEnabled {
		recs = append(recs, DNSRecord{Type: "TXT", Name: cfg.Domain + ".", Value: "v=spf1 a mx ~all"})
	}
	if cfg.DKIMEnabled && dkimTXT != "" {
		recs = append(recs, DNSRecord{Type: "TXT", Name: dkimName + ".", Value: dkimTXT})
	}
	if cfg.DMARCEnabled {
		recs = append(recs, DNSRecord{
			Type:  "TXT",
			Name:  "_dmarc." + cfg.Domain + ".",
			Value: "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + cfg.Domain + "; adkim=s; aspf=s",
		})
	}
	return recs
}

// RecordHealth is the live verification state of one expected record.
type RecordHealth struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Found   string `json:"found"`
	Message string `json:"message"`
}

// DomainHealth aggregates DNS checks for a domain.
type DomainHealth struct {
	Domain    string         `json:"domain"`
	Records   []RecordHealth `json:"records"`
	AllOK     bool           `json:"all_ok"`
	CheckedAt time.Time      `json:"checked_at"`
}

// CheckHealth performs live DNS lookups (MX, SPF, DKIM, DMARC) and reports
// whether each expected record is present. Lookups are best-effort and bounded.
func CheckHealth(ctx context.Context, cfg Config, dkimName string) *DomainHealth {
	res := &DomainHealth{Domain: cfg.Domain, CheckedAt: time.Now().UTC(), AllOK: true}
	resolver := net.Resolver{}

	// MX
	mxOK := false
	var mxFound string
	if mxs, err := resolver.LookupMX(ctx, cfg.Domain); err == nil && len(mxs) > 0 {
		mxOK = true
		mxFound = strings.TrimSuffix(mxs[0].Host, ".")
	}
	res.Records = append(res.Records, health("MX", cfg.Domain, mxOK, mxFound))

	// SPF
	if cfg.SPFEnabled {
		res.Records = append(res.Records, txtContains(ctx, &resolver, "SPF", cfg.Domain, "v=spf1"))
	}
	// DKIM
	if cfg.DKIMEnabled {
		res.Records = append(res.Records, txtContains(ctx, &resolver, "DKIM", dkimName, "v=DKIM1"))
	}
	// DMARC
	if cfg.DMARCEnabled {
		res.Records = append(res.Records, txtContains(ctx, &resolver, "DMARC", "_dmarc."+cfg.Domain, "v=DMARC1"))
	}

	for _, r := range res.Records {
		if !r.OK {
			res.AllOK = false
		}
	}
	return res
}

func health(typ, name string, ok bool, found string) RecordHealth {
	msg := "missing"
	if ok {
		msg = "ok"
	}
	return RecordHealth{Type: typ, Name: name, OK: ok, Found: found, Message: msg}
}

func txtContains(ctx context.Context, r *net.Resolver, typ, name, needle string) RecordHealth {
	txts, err := r.LookupTXT(ctx, name)
	if err != nil {
		return health(typ, name, false, "")
	}
	for _, t := range txts {
		if strings.Contains(t, needle) {
			return health(typ, name, true, t)
		}
	}
	return health(typ, name, false, strings.Join(txts, " | "))
}

// Deliverability runs the live checks that most often send legitimate mail to
// spam: whether the DKIM key actually published in DNS matches the key VayuMail
// signs with, and whether the host's reverse DNS (PTR) matches the mail
// hostname. Both are best-effort.
func Deliverability(ctx context.Context, cfg Config, dkimName, dkimTXT string) []RecordHealth {
	var out []RecordHealth
	r := &net.Resolver{}

	// Outbound smarthost relay: when configured, the relay's IP reputation (not
	// this host's) governs deliverability, so a local PTR/FQDN mismatch is no
	// longer a spam factor for outbound mail. Surface this so the operator reads
	// the checks below in the right context.
	if cfg.RelayEnabled() {
		out = append(out, RecordHealth{
			Type:    "Outbound relay",
			Name:    cfg.RelayAddr(),
			OK:      true,
			Found:   cfg.RelayAddr(),
			Message: "active — outbound mail is sent through this authenticated relay; its IP reputation applies. DKIM is still signed with your domain key, so DMARC stays aligned.",
		})
	}

	// HELO/hostname must be a fully-qualified domain name. A bare label or
	// "localhost" announced in EHLO is an immediate spam signal for Gmail and
	// Outlook.
	hn := RecordHealth{Type: "HELO hostname", Name: cfg.Hostname, Message: "ok", OK: true, Found: cfg.Hostname}
	if cfg.Hostname == "" || !strings.Contains(strings.TrimSuffix(cfg.Hostname, "."), ".") || strings.EqualFold(cfg.Hostname, "localhost") {
		hn.OK = false
		hn.Message = "mail hostname is not a fully-qualified domain name — set VAYUOS_MAIL_HOSTNAME to e.g. mail.example.com so EHLO announces a real FQDN"
	}
	out = append(out, hn)

	// DKIM published-key vs signing-key — a mismatch means dkim=fail at the
	// recipient, which (with an enforcing DMARC policy) lands mail in spam.
	if cfg.DKIMEnabled && dkimTXT != "" {
		wantP := dkimPValue(dkimTXT)
		published, _ := r.LookupTXT(ctx, dkimName)
		joined := strings.ReplaceAll(strings.Join(published, ""), " ", "")
		switch {
		case len(published) == 0:
			out = append(out, RecordHealth{Type: "DKIM key", Name: dkimName, OK: false, Message: "no DKIM record published — publish the DKIM record shown above"})
		case wantP != "" && strings.Contains(joined, wantP):
			out = append(out, RecordHealth{Type: "DKIM key", Name: dkimName, OK: true, Found: "matches signing key", Message: "ok"})
		default:
			out = append(out, RecordHealth{Type: "DKIM key", Name: dkimName, OK: false, Found: "different key in DNS", Message: "published DKIM key does NOT match VayuMail's signing key — replace the TXT record with the DKIM value above or every message fails DKIM"})
		}
	}

	// Reverse DNS (PTR) vs the mail hostname.
	//
	// cfg.Hostname usually resolves to BOTH an A (IPv4) and AAAA (IPv6) address,
	// and a VPS often only has the PTR set on one of them (or one has propagated
	// and the other not yet). Checking just the first address therefore produced
	// false "mismatch" reports. Instead we reverse-resolve EVERY forward IP and
	// pass when ANY of them maps back to the hostname (forward-confirmed rDNS on
	// at least one address is what mail receivers accept). The message lists what
	// was actually found so a real mismatch is still actionable.
	ptr := RecordHealth{Type: "PTR", Name: cfg.Hostname, Message: "could not resolve reverse DNS"}
	if ips, err := r.LookupHost(ctx, cfg.Hostname); err == nil && len(ips) > 0 {
		var found []string
		matched := false
		for _, ip := range ips {
			names, lerr := r.LookupAddr(ctx, ip)
			if lerr != nil || len(names) == 0 {
				continue
			}
			for _, n := range names {
				n = strings.TrimSuffix(n, ".")
				found = append(found, ip+"→"+n)
				if strings.EqualFold(n, cfg.Hostname) {
					matched = true
				}
			}
		}
		if len(found) > 0 {
			ptr.Found = strings.Join(found, ", ")
		}
		switch {
		case matched:
			ptr.OK, ptr.Message = true, "ok"
		case len(found) > 0:
			ptr.Message = "reverse DNS does not match " + cfg.Hostname + " — set rDNS/PTR for your sending IP(s) at your VPS provider (allow a few minutes to propagate); Gmail/Outlook penalise a mismatch"
		default:
			ptr.Message = "no reverse DNS (PTR) found for " + cfg.Hostname + "'s IP(s) — set rDNS/PTR at your VPS provider"
		}
	}
	out = append(out, ptr)
	return out
}

// dkimPValue extracts the whitespace-stripped base64 public key (the p= tag)
// from a DKIM TXT value, for robust comparison against a published record.
func dkimPValue(txt string) string {
	for _, part := range strings.Split(txt, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "p=") {
			return strings.ReplaceAll(strings.TrimSpace(part[2:]), " ", "")
		}
	}
	return ""
}
