// SPDX-License-Identifier: Apache-2.0

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
	// MalformedFrom records that the message had zero, several, or a
	// multi-address From header — the shape used to make a verifier and a mail
	// client disagree about who sent it. Always accompanies DMARC="fail".
	MalformedFrom bool
	Header        string // assembled Authentication-Results header value
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
	fromDomain, fromOK := headerFromDomain(raw)
	switch {
	case !fromOK:
		// A message whose From cannot be identified unambiguously is not a
		// message DMARC can be evaluated for, and it is not an innocent one
		// either — a legitimate sender emits one From with one address. Treat it
		// as a failure and quarantine it, rather than letting it through with
		// "dmarc=none", which downstream reads as "the domain has no policy".
		v.DMARC = "fail"
		v.MalformedFrom = true
		v.Quarantine = true
	default:
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
	if v.MalformedFrom {
		// Say why, so an operator reading raw headers can tell this apart from an
		// ordinary alignment failure.
		h.WriteString(" reason=\"malformed From header\"")
	}
	v.Header = h.String()
	return v
}

// headerFromDomain returns the domain of the message's From header, and whether
// the From header is well-formed enough for DMARC to mean anything.
//
// This is where DMARC is most often bypassed, so it fails CLOSED. RFC 5322
// permits exactly one From header carrying exactly one address; real phishing
// routinely sends neither, because the two halves of the pipeline disagree about
// which one wins:
//
//   - TWO From headers. Header.Get returns the first, so a naive verifier
//     evaluates attacker@evil.example (which passes its own DMARC) while many
//     clients render the LAST one — ceo@bank.example. Alignment passes and the
//     reader sees the bank.
//   - A multi-address From ("a@evil.example, ceo@bank.example"). ParseAddress
//     errors, an empty domain falls out, and the DMARC block is skipped
//     entirely — so a message that should have been quarantined is delivered
//     with no policy applied at all. Silently skipping the check is the worst
//     outcome available, because everything downstream reads "dmarc=none" as
//     "the domain publishes no policy" rather than "we could not tell".
//
// So: exactly one From header, exactly one address, or the message is reported
// as malformed and treated as a DMARC failure by the caller.
func headerFromDomain(raw []byte) (domain string, wellFormed bool) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", false
	}
	froms := msg.Header["From"]
	if len(froms) != 1 {
		// Zero (no From at all) or two-plus (the display/verify split above).
		return "", false
	}
	list, err := netmail.ParseAddressList(froms[0])
	if err != nil || len(list) != 1 {
		return "", false
	}
	d := domainOf(list[0].Address)
	if d == "" {
		return "", false
	}
	return d, true
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

// trustedInboundHeaders are the header names this server stamps and later
// TRUSTS. Anything a remote sender supplies under these names is a forgery
// attempt by definition, because only this server is entitled to assert them.
var trustedInboundHeaders = []string{
	"authentication-results",      // RFC 8601 §5 requires deleting extant ones
	"x-vayumail-auth-quarantine",  // consumed by the junk filter (spam.go)
	"x-vayumail-forwarded",        // forwarding loop breaker (inbound.go)
	"x-vayumail-spam",             // any future X-VayuMail assertion
	"x-vayumail-authenticated-as", //
}

// stripTrustedHeaders removes every header a remote sender must not be able to
// assert, returning the message with its body untouched.
//
// RFC 8601 §5 makes this mandatory for Authentication-Results: a verifier MUST
// delete extant fields bearing its own authserv-id, or a sender simply writes
// "dkim=pass" itself. The same reasoning extends to every X-VayuMail-* header
// the pipeline later believes:
//
//   - X-VayuMail-Auth-Quarantine is read by the junk filter. Today the stamped
//     copy is prepended and Header.Get returns the first, so a forgery loses —
//     but that is an accident of ordering, not a guarantee. Any consumer that
//     iterates, takes the last, or reads the raw source is deceived, and a human
//     inspecting headers to decide whether a message is genuine is deceived too.
//   - X-VayuMail-Forwarded is the forwarding loop breaker. A sender who sets it
//     on the way in silently suppresses the recipient's own auto-forward — a
//     targeted, invisible mail-loss attack that would look like a VayuMail bug.
//
// Stripping runs before the stamped headers are prepended, so the delivered
// message carries exactly one set: ours.
func stripTrustedHeaders(raw []byte) []byte {
	// Header section ends at the first blank line; everything after is the body
	// and must be preserved byte for byte (signatures cover it).
	end := len(raw)
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		end = i + 2 // keep the CRLF that terminates the last header line
	} else if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		end = i + 1
	}
	head, body := raw[:end], raw[end:]

	var out bytes.Buffer
	out.Grow(len(head))
	drop := false
	for _, line := range splitLinesKeepEnds(head) {
		// A continuation line (leading space/tab) belongs to the header above it,
		// so it inherits that header's fate — otherwise a folded forgery survives
		// with its first line removed.
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			if drop {
				continue
			}
			out.Write(line)
			continue
		}
		drop = false
		if colon := bytes.IndexByte(line, ':'); colon > 0 {
			name := strings.ToLower(strings.TrimSpace(string(line[:colon])))
			for _, bad := range trustedInboundHeaders {
				if name == bad {
					drop = true
					break
				}
			}
		}
		if drop {
			continue
		}
		out.Write(line)
	}
	out.Write(body)
	return out.Bytes()
}

// splitLinesKeepEnds splits on \n while retaining the line terminator, so the
// header block can be reassembled without altering line endings.
func splitLinesKeepEnds(b []byte) [][]byte {
	var out [][]byte
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, b)
			break
		}
		out = append(out, b[:i+1])
		b = b[i+1:]
	}
	return out
}
