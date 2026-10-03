// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"context"
	netmail "net/mail"
	"strings"
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
// contacts and the message did not fail DMARC. Trusting your contacts is what
// every mail service does; trusting a message that proves it is not from them
// would let anyone who knows whom you trust past the junk filter in their
// name, so a failed DMARC is filed as before.
func (e *Engine) senderIsContact(recipient string, raw []byte) bool {
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil || dmarcOutcome(msg.Header) != dmarcNoFailure {
		return false
	}
	addr, _ := headerFrom(raw)
	return addr != "" && e.accounts.HasContact(context.Background(), recipient, addr)
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
