// SPDX-License-Identifier: Apache-2.0

package mail

// verdict.go — the From domain's check, kept with the message so the reader
// can say "halcyon.dev confirmed it sent this".
//
// The inbound server's Authentication-Results can be believed at delivery,
// but not later: a message sent between local mailboxes, appended over IMAP
// or brought in from elsewhere carries whatever such header its writer chose.
// So the verdict is sealed at delivery with a key only this install holds,
// over the message as delivered: a header written by anyone else, or copied
// from a genuine message onto another, does not verify.

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	netmail "net/mail"
	"strings"
)

const checkedHeader = "X-VayuMail-Checked: "

// UseVerdictKey sets the key that seals and reads the From domain's check.
// Without one nothing is sealed and nothing reads as checked.
func (e *Engine) UseVerdictKey(key []byte) { e.verdictKey = append([]byte(nil), key...) }

// sealVerdict puts the checked header first on raw when the From domain
// authenticated it. raw is as the inbound server stamped it, with its own
// Authentication-Results first and any sender-written X-VayuMail-* removed.
func (e *Engine) sealVerdict(raw []byte) []byte {
	if len(e.verdictKey) == 0 {
		return raw
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil || !fromAuthenticated(msg.Header.Get("Authentication-Results")) {
		return raw
	}
	addr, _ := headerFrom(raw)
	dom := domainOf(addr)
	if dom == "" {
		return raw
	}
	return append([]byte(checkedHeader+dom+"; mac="+e.verdictMAC(dom, raw)+"\r\n"), raw...)
}

func (e *Engine) verdictMAC(dom string, rest []byte) string {
	h := hmac.New(sha256.New, e.verdictKey)
	h.Write([]byte(strings.ToLower(dom)))
	h.Write([]byte{0})
	h.Write(rest)
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// CheckedDomain is the domain that confirmed it sent raw, a message as
// stored, or "" when nothing this install sealed says so.
func (e *Engine) CheckedDomain(raw []byte) string {
	if len(e.verdictKey) == 0 || !bytes.HasPrefix(raw, []byte(checkedHeader)) {
		return ""
	}
	end := bytes.Index(raw, []byte("\r\n"))
	if end < 0 {
		return ""
	}
	dom, mac, ok := strings.Cut(string(raw[len(checkedHeader):end]), "; mac=")
	if !ok || !hmac.Equal([]byte(mac), []byte(e.verdictMAC(dom, raw[end+2:]))) {
		return ""
	}
	return strings.ToLower(dom)
}
