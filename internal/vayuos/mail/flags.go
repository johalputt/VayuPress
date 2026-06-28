package mail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// flags.go — translate between IMAP system flags and Maildir info flags, and set
// the full flag set on a stored message.
//
// Maildir encodes per-message state as letters after a ":2," suffix on the
// filename (RFC-ish "experimental" semantics, universally used):
//
//	D Draft     → \Draft
//	F Flagged   → \Flagged
//	R Replied   → \Answered
//	S Seen      → \Seen
//	T Trashed   → \Deleted
//	P Passed    → (no standard IMAP flag; preserved but not surfaced)
//
// A message in new/ has no flags and is unseen. A message in cur/ carries
// whatever flags its name encodes. We keep the immutable base name unchanged so
// the message keeps its UID (see uidstore.go) when flags change.

// maildirFlagOrder is the canonical ASCII ordering Maildir filenames use.
const maildirFlagOrder = "DFPRST"

// imapToMaildirFlag maps an IMAP system flag (lower-cased) to its Maildir letter.
var imapToMaildirFlag = map[string]byte{
	`\seen`:     'S',
	`\answered`: 'R',
	`\flagged`:  'F',
	`\deleted`:  'T',
	`\draft`:    'D',
}

// maildirToIMAPFlag is the reverse mapping, for FETCH FLAGS responses.
var maildirToIMAPFlag = map[byte]string{
	'S': `\Seen`,
	'R': `\Answered`,
	'F': `\Flagged`,
	'T': `\Deleted`,
	'D': `\Draft`,
}

// parseIMAPFlag returns the Maildir letter for an IMAP flag token, or 0 if the
// flag has no Maildir representation (keywords are accepted but not stored).
func parseIMAPFlag(tok string) byte {
	return imapToMaildirFlag[strings.ToLower(strings.TrimSpace(tok))]
}

// infoFlagsForID returns the Maildir info-flag letters for a List() id
// ("new/<name>" or "cur/<name>"). Messages in new/ have none.
func infoFlagsForID(id string) string {
	sub, name, ok := strings.Cut(id, "/")
	if !ok || sub == "new" {
		return ""
	}
	_, flags := splitMaildirFlags(name)
	// Keep only recognised letters, in canonical order.
	var b strings.Builder
	for _, c := range maildirFlagOrder {
		if strings.ContainsRune(flags, c) {
			b.WriteByte(byte(c))
		}
	}
	return b.String()
}

// imapFlagsForID returns the IMAP system flags for a message id (e.g. []{"\Seen"}).
func imapFlagsForID(id string) []string {
	out := []string{}
	for _, c := range infoFlagsForID(id) {
		if f, ok := maildirToIMAPFlag[byte(c)]; ok {
			out = append(out, f)
		}
	}
	return out
}

// flagSetForID returns the current flags of a message as a letter→present set.
func flagSetForID(id string) map[byte]bool {
	set := map[byte]bool{}
	for _, c := range infoFlagsForID(id) {
		set[byte(c)] = true
	}
	return set
}

// canonicalFlagString renders a flag set as the ordered Maildir letter string.
func canonicalFlagString(set map[byte]bool) string {
	var b strings.Builder
	for _, c := range maildirFlagOrder {
		if set[byte(c)] {
			b.WriteByte(byte(c))
		}
	}
	return b.String()
}

// setMessageFlags rewrites a message's Maildir flags to exactly the given set
// and returns its new List() id. The immutable base name is preserved (so the
// UID is stable). An empty set moves the message back to new/ (fully unseen);
// any non-empty set places it in cur/ with the ":2,<flags>" suffix.
func (m *Maildir) setMessageFlags(domain, username, folder, id string, set map[byte]bool) (string, error) {
	sub, name, ok := strings.Cut(id, "/")
	if !ok || (sub != "new" && sub != "cur") {
		return id, errors.New("vayumail: invalid message id")
	}
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return id, errors.New("vayumail: invalid message id")
	}
	name = filepath.Base(name)
	base, _ := splitMaildirFlags(name)
	dir := m.folderDir(domain, username, folder)

	flagStr := canonicalFlagString(set)
	var targetSub, targetName string
	if flagStr == "" {
		targetSub, targetName = "new", base
	} else {
		targetSub, targetName = "cur", base+":2,"+flagStr
	}
	if targetSub == sub && targetName == name {
		return id, nil // no change
	}
	if err := os.MkdirAll(filepath.Join(dir, targetSub), 0o700); err != nil {
		return id, err
	}
	src := filepath.Join(dir, sub, name)
	dst := filepath.Join(dir, targetSub, targetName)
	if err := os.Rename(src, dst); err != nil {
		return id, err
	}
	return targetSub + "/" + targetName, nil
}
