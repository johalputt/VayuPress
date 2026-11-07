// SPDX-License-Identifier: Apache-2.0

package mail

// sealing.go — the mail of a private mailbox, sealed to its holder's key.
//
// A mailbox whose holder keeps its key (VayuPGP's holder.go) has every
// message encrypted to that key before it is written: on arrival, into Sent
// and Drafts, on an IMAP APPEND, by the importer, and over the mail stored
// before it was made private (SealStored). Encrypting needs only the public
// key, so mail is sealed whether or not the holder is signed in; reading it
// needs the private key, which the server has only while a sign-in that
// proved the password holds it open.
//
// A sealed file is itself a standard PGP/MIME message (RFC 3156): the whole
// original message, headers included, is the encrypted payload, and the
// outer headers carry only what delivery and threading already need — who,
// to whom, when, and the message ids. So a sealed file is mail any OpenPGP
// client reads with the key: the VayuMail app, which takes the locked key and
// unlocks it on the phone, opens it over IMAP whether the server holds the key
// open or not.
//
// Every read of a message file goes through the Maildir (ReadRaw,
// ReadRawFolder, the listing's headersFor), so opening is done here, once,
// for every reader: while the key is held open they get the original
// message, and otherwise the sealed one. The decision of who may read a
// mailbox at all stays where it is (readAuthorised); this is what makes that
// decision hold against someone who reads the disk instead.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// MailboxKeys is what the Maildir needs of VayuPGP to seal and open mail.
// *pgp.Engine is one.
type MailboxKeys interface {
	HolderKeeps(email string) bool
	HeldOpen(email string) bool
	SealTo(email string, plain []byte) ([]byte, error)
	DecryptForEmail(armored []byte, email string) ([]byte, error)
}

// sealedMark names a file this Maildir sealed. Inbound mail cannot forge it
// (stripTrustedHeaders), and it is only looked for in a mailbox whose holder
// keeps the key.
const sealedMark = "X-Vayumail-Sealed"

// outerHeaders are the header fields a sealed file keeps outside the seal:
// what delivery, listing and threading need, and nothing of the content.
var outerHeaders = map[string]bool{
	"from": true, "to": true, "cc": true, "date": true,
	"message-id": true, "in-reply-to": true, "references": true,
}

// SetKeys gives the Maildir its key source. Set before the store is shared.
func (m *Maildir) SetKeys(k MailboxKeys) { m.keys = k }

func mailboxEmail(domain, username string) string {
	if strings.Contains(username, "@") {
		return strings.ToLower(username)
	}
	return strings.ToLower(username + "@" + domain)
}

// sealView is what one listing knows of its mailbox's seal.
type sealView struct {
	email         string
	private, open bool
}

func (m *Maildir) sealViewFor(domain, username string) sealView {
	if m.keys == nil {
		return sealView{}
	}
	email := mailboxEmail(domain, username)
	if !m.keys.HolderKeeps(email) {
		return sealView{email: email}
	}
	return sealView{email: email, private: true, open: m.keys.HeldOpen(email)}
}

// sealFor seals raw when its mailbox is private. A seal that fails refuses
// the write: storing the message in the clear would be the one outcome the
// holder asked never to happen. A message that already looks sealed is sealed
// again rather than trusted: the mark is only a header, and mail brought in
// by the importer or an APPEND comes from elsewhere.
func (m *Maildir) sealFor(domain, username string, raw []byte) ([]byte, error) {
	if m.keys == nil {
		return raw, nil
	}
	email := mailboxEmail(domain, username)
	if !m.keys.HolderKeeps(email) {
		return raw, nil
	}
	armored, err := m.keys.SealTo(email, raw)
	if err != nil {
		return nil, errors.New("vayumail: this private mailbox's mail could not be sealed, so it was not stored")
	}
	return sealedMessage(raw, armored), nil
}

// openFor gives the original of a sealed message while its key is held open,
// and the sealed message otherwise. A message sealed twice (a sealed copy
// appended back by a client) is opened to the original; the bound stops a
// crafted nesting from costing more than a few decryptions.
func (m *Maildir) openFor(domain, username string, raw []byte) []byte {
	if m.keys == nil {
		return raw
	}
	raw, _ = m.openAll(mailboxEmail(domain, username), raw)
	return raw
}

// openAll opens raw's seals, and reports whether it opened any.
func (m *Maildir) openAll(email string, raw []byte) ([]byte, bool) {
	opened := false
	for range 3 {
		if !isSealed(raw) {
			break
		}
		plain, ok := m.open(email, raw)
		if !ok {
			break
		}
		raw, opened = plain, true
	}
	return raw, opened
}

func (m *Maildir) open(email string, sealed []byte) ([]byte, bool) {
	const begin, end = "-----BEGIN PGP MESSAGE-----", "-----END PGP MESSAGE-----"
	bi := bytes.Index(sealed, []byte(begin))
	ei := bytes.Index(sealed, []byte(end))
	if bi < 0 || ei < bi {
		return nil, false
	}
	plain, err := m.keys.DecryptForEmail(sealed[bi:ei+len(end)], email)
	if err != nil {
		return nil, false
	}
	return plain, true
}

// isSealed reports whether raw's header block carries sealedMark.
func isSealed(raw []byte) bool {
	hdr := raw
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		hdr = raw[:i]
	} else if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		hdr = raw[:i]
	}
	for _, line := range bytes.Split(hdr, []byte("\n")) {
		name, _, ok := bytes.Cut(line, []byte(":"))
		if ok && strings.EqualFold(string(bytes.TrimSpace(name)), sealedMark) {
			return true
		}
	}
	return false
}

// sealedMessage wraps armored, the encryption of raw, as a PGP/MIME message
// carrying raw's outer headers.
func sealedMessage(raw, armored []byte) []byte {
	var b bytes.Buffer
	for _, field := range headerFields(raw) {
		name, _, _ := strings.Cut(field, ":")
		if outerHeaders[strings.ToLower(strings.TrimSpace(name))] {
			b.WriteString(field)
		}
	}
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce)
	boundary := "vayumail-sealed-" + hex.EncodeToString(nonce)
	b.WriteString(sealedMark + ": 1\r\n")
	b.WriteString("Subject: ...\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString(`Content-Type: multipart/encrypted; protocol="application/pgp-encrypted"; boundary="` + boundary + "\"\r\n\r\n")
	b.WriteString("This message is sealed to its mailbox's key.\r\n")
	b.WriteString("--" + boundary + "\r\nContent-Type: application/pgp-encrypted\r\nContent-Description: PGP/MIME version identification\r\n\r\nVersion: 1\r\n\r\n")
	b.WriteString("--" + boundary + "\r\nContent-Type: application/octet-stream; name=\"encrypted.asc\"\r\nContent-Disposition: inline; filename=\"encrypted.asc\"\r\n\r\n")
	b.Write(bytes.ReplaceAll(bytes.ReplaceAll(armored, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n")))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes()
}

// headerFields splits raw's header block into whole fields, each with its
// folded continuation lines and line endings as they were.
func headerFields(raw []byte) []string {
	var fields []string
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if strings.TrimRight(line, "\r\n") == "" {
			break
		}
		if (line[0] == ' ' || line[0] == '\t') && len(fields) > 0 {
			fields[len(fields)-1] += line
			continue
		}
		fields = append(fields, line)
	}
	for i, f := range fields {
		if !strings.HasSuffix(f, "\n") {
			fields[i] = f + "\r\n"
		}
	}
	return fields
}

// SealStored seals the mail a private mailbox held before it was made
// private, in every folder, and reports how many messages it sealed and how
// many it left in the clear to be sealed by the next run.
//
// Each file is rewritten under its own name through tmp/, with its time kept:
// the name carries its flags and its IMAP identity, and the time is the read
// time retention counts from (flags.go). Nothing here locks out a flag change,
// which renames the file; a file renamed while it was being sealed is left
// for the next run rather than written back under its old name.
func (m *Maildir) SealStored(domain, username string) (sealed, left int, err error) {
	email := mailboxEmail(domain, username)
	if m.keys == nil || !m.keys.HolderKeeps(email) {
		return 0, 0, errors.New("vayumail: this mailbox is not private")
	}
	for _, folder := range m.Folders(domain, username) {
		dir := m.folderDir(domain, username, folder)
		for _, sub := range []string{"new", "cur"} {
			entries, rerr := os.ReadDir(filepath.Join(dir, sub))
			if rerr != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				done, ferr := m.sealFile(domain, username, email, dir, filepath.Join(dir, sub, e.Name()))
				switch {
				case ferr != nil:
					left++
				case done:
					sealed++
				}
			}
		}
	}
	return sealed, left, nil
}

func (m *Maildir) sealFile(domain, username, email, dir, path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	// A file is taken as sealed when it is marked and, with the key at hand,
	// opens: mail stored before the mark was stripped from inbound could
	// carry it.
	if isSealed(raw) {
		if !m.keys.HeldOpen(email) {
			return false, nil
		}
		if _, ok := m.open(email, raw); ok {
			return false, nil
		}
	}
	out, err := m.sealFor(domain, username, raw)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return false, err
	}
	tmp := filepath.Join(dir, "tmp", "seal."+filepath.Base(path))
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return false, err
	}
	if err := os.Chtimes(tmp, info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}
