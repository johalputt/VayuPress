package mail

import (
	"bytes"
	"net"
	netmail "net/mail"
	"strings"

	"blitiri.com.ar/go/spf"
	"github.com/emersion/go-msgauth/dkim"
	"github.com/emersion/go-msgauth/dmarc"
)

// AuthVerdict summarises the inbound authentication checks for a received
// message. Result strings use the standard tokens (pass/fail/none/…) so they
// can be emitted verbatim in an Authentication-Results header.
type AuthVerdict struct {
	SPF        string // pass/fail/softfail/neutral/none/temperror/permerror
	DKIM       string // pass/fail/none
	DKIMDomain string // signing domain of the first passing DKIM signature
	DMARC      string // pass/fail/none
	Quarantine bool   // DMARC failed AND the From domain publishes p=quarantine/reject
	Header     string // assembled Authentication-Results header value
}

// verifyInbound runs SPF (connecting IP vs envelope sender), DKIM (message
// signatures), and DMARC (policy + alignment with the From-header domain) on a
// received message. Every lookup is best-effort — a DNS error degrades to
// "none"/"temperror" and never blocks delivery — so this is safe to call inline
// during the SMTP transaction. hostname is the authserv-id for the header.
func verifyInbound(hostname string, ip net.IP, helo, mailFrom string, raw []byte) AuthVerdict {
	v := AuthVerdict{SPF: "none", DKIM: "none", DMARC: "none"}

	// ── SPF: connecting IP against the envelope MAIL FROM domain. ──
	if ip != nil && mailFrom != "" {
		if res, _ := spf.CheckHostWithSender(ip, helo, mailFrom); res != "" {
			v.SPF = string(res)
		}
	}
	spfDomain := domainOf(mailFrom)

	// ── DKIM: verify all signatures; remember the first that passes. ──
	if verifs, err := dkim.Verify(bytes.NewReader(raw)); err == nil {
		for _, ver := range verifs {
			if ver.Err == nil {
				v.DKIM, v.DKIMDomain = "pass", ver.Domain
				break
			}
			v.DKIM = "fail"
		}
	}

	// ── DMARC: keyed on the From-header domain, with alignment. ──
	fromDomain := headerFromDomain(raw)
	if fromDomain != "" {
		if rec, err := dmarc.Lookup(fromDomain); err == nil {
			spfAligned := v.SPF == "pass" && domainsAligned(spfDomain, fromDomain, rec.SPFAlignment)
			dkimAligned := v.DKIM == "pass" && domainsAligned(v.DKIMDomain, fromDomain, rec.DKIMAlignment)
			if spfAligned || dkimAligned {
				v.DMARC = "pass"
			} else {
				v.DMARC = "fail"
				v.Quarantine = rec.Policy == dmarc.PolicyQuarantine || rec.Policy == dmarc.PolicyReject
			}
		}
	}

	var h strings.Builder
	h.WriteString(hostname)
	h.WriteString("; spf=" + v.SPF)
	if spfDomain != "" {
		h.WriteString(" smtp.mailfrom=" + spfDomain)
	}
	h.WriteString("; dkim=" + v.DKIM)
	if v.DKIMDomain != "" {
		h.WriteString(" header.d=" + v.DKIMDomain)
	}
	h.WriteString("; dmarc=" + v.DMARC)
	if fromDomain != "" {
		h.WriteString(" header.from=" + fromDomain)
	}
	v.Header = h.String()
	return v
}

// headerFromDomain returns the domain of the message's From header.
func headerFromDomain(raw []byte) string {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	addr, err := netmail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		return ""
	}
	return domainOf(addr.Address)
}

// domainsAligned reports DMARC identifier alignment. Strict requires an exact
// (case-insensitive) match; relaxed (the default) also accepts an
// organizational-domain match, approximated here by a subdomain suffix.
func domainsAligned(d, fromDomain string, mode dmarc.AlignmentMode) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	fromDomain = strings.ToLower(strings.TrimSpace(fromDomain))
	if d == "" || fromDomain == "" {
		return false
	}
	if d == fromDomain {
		return true
	}
	if mode == dmarc.AlignmentStrict {
		return false
	}
	return strings.HasSuffix(d, "."+fromDomain) || strings.HasSuffix(fromDomain, "."+d)
}

// authResultsHeader renders the full Authentication-Results header line.
func (v AuthVerdict) authResultsHeader() string {
	return "Authentication-Results: " + v.Header + "\r\n"
}
