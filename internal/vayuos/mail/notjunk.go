// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	netmail "net/mail"
	"strings"

	"github.com/emersion/go-msgauth/dmarc"
)

// NotJunk is "this is not junk" (pipeline 3ab): the message goes back to the
// Inbox, and its sender joins the mailbox's contacts, whose mail the junk
// filter no longer files as junk (senderIsContact). The move is the
// authorised one every other change makes, and the sender is saved only once
// it has succeeded, so a read-only mailbox can do neither. It returns the
// sender's address, "" when the message names none.
func (e *Engine) NotJunk(rd Reader, id string) (string, error) {
	raw, err := e.ReadFolderMessage(rd, "Junk", id)
	if err != nil {
		return "", err
	}
	if err := e.MoveMessage(rd, id, "Junk", "Inbox"); err != nil {
		return "", err
	}
	addr, name := headerFrom(raw)
	if addr == "" || e.accounts == nil {
		return addr, nil
	}
	dom, local := e.mailboxKey(rd.Key())
	return addr, e.accounts.AddContact(context.Background(), local+"@"+dom, addr, name)
}

// senderIsContact reports whether a message's From is in the recipient's
// contacts and this server's own verdict shows it came from that domain:
// DMARC passed, or, where the domain publishes no DMARC policy, SPF or DKIM
// passed for a domain aligned with it (domainsAligned, relaxed, as DMARC
// itself would). Trusting a From that nothing authenticates would let anyone
// who knows whom you trust past the junk filter in their name. Call it only
// for stamped mail (deliver), whose first Authentication-Results is ours.
func (e *Engine) senderIsContact(recipient string, raw []byte) bool {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil || !fromAuthenticated(msg.Header.Get("Authentication-Results")) {
		return false
	}
	addr, _ := headerFrom(raw)
	return addr != "" && e.accounts.HasContact(context.Background(), recipient, addr)
}

// fromAuthenticated reads this server's Authentication-Results (verifyInbound
// writes it: "host; spf=… smtp.mailfrom=…; dkim=… header.d=…; dmarc=…
// header.from=…") and reports whether the From domain authenticated the
// message.
func fromAuthenticated(ar string) bool {
	v := map[string]string{}
	for _, f := range strings.FieldsFunc(strings.ToLower(ar), func(r rune) bool { return r == ';' || r == ' ' || r == '\t' }) {
		if k, val, ok := strings.Cut(f, "="); ok {
			if _, seen := v[k]; !seen {
				v[k] = val
			}
		}
	}
	switch v["dmarc"] {
	case "pass":
		return true
	case "none":
		from := v["header.from"]
		return v["spf"] == "pass" && domainsAligned(v["smtp.mailfrom"], from, dmarc.AlignmentRelaxed) ||
			v["dkim"] == "pass" && domainsAligned(v["header.d"], from, dmarc.AlignmentRelaxed)
	}
	return false
}

// headerFrom is a message's From address and display name, from its header
// block alone.
func headerFrom(raw []byte) (addr, name string) {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return "", ""
	}
	a, err := netmail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		return "", ""
	}
	return strings.ToLower(strings.TrimSpace(a.Address)), strings.TrimSpace(a.Name)
}
