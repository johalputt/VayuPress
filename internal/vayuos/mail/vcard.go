// SPDX-License-Identifier: Apache-2.0

package mail

// vcard.go — contacts in and out as vCards (RFC 6350, and the 3.0 and 2.1
// that phones and other mail services still export). Only what an address
// book here keeps is read: the name, the addresses and the groups
// (CATEGORIES); everything else on a card is passed over.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/quotedprintable"
	"sort"
	"strings"
	"unicode/utf8"
)

// VCard is what this address book keeps of one card.
type VCard struct {
	Name   string
	Emails []string
	Groups []string
}

// maxImportCards bounds one import, as the console's upload limit bounds its
// bytes: an address book larger than this is a list for a newsletter.
const maxImportCards = 5000

// ParseVCards reads every card in data. A line it cannot read is passed
// over rather than failing the file, since exports differ in what else they
// carry; a file with no card at all is an error, as it is not a vCard file.
func ParseVCards(data []byte) ([]VCard, error) {
	var cards []VCard
	var cur *VCard
	var given, family string
	inCard := false
	for _, line := range unfoldVCard(data) {
		name, params, value, ok := splitVCardLine(line)
		if !ok {
			continue
		}
		switch {
		case name == "BEGIN" && strings.EqualFold(value, "VCARD"):
			cur, given, family, inCard = &VCard{}, "", "", true
			continue
		case !inCard:
			continue
		case name == "END" && strings.EqualFold(value, "VCARD"):
			if cur.Name == "" {
				cur.Name = strings.TrimSpace(given + " " + family)
			}
			cards = append(cards, *cur)
			inCard = false
			continue
		}
		value = decodeVCardValue(params, value)
		switch name {
		case "FN":
			cur.Name = strings.TrimSpace(vcardUnescape(value))
		case "N":
			parts := splitVCardList(value, ';')
			if len(parts) > 1 {
				family, given = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			}
		case "EMAIL":
			if e := strings.TrimSpace(strings.TrimPrefix(vcardUnescape(value), "mailto:")); e != "" {
				cur.Emails = append(cur.Emails, e)
			}
		case "CATEGORIES":
			for _, g := range splitVCardList(value, ',') {
				if g = strings.TrimSpace(g); g != "" {
					cur.Groups = append(cur.Groups, g)
				}
			}
		}
	}
	if len(cards) == 0 {
		return nil, errors.New("there is no vCard in that file")
	}
	return cards, nil
}

// unfoldVCard splits data into its logical lines: a line that begins with a
// space or tab continues the one before (RFC 6350 §3.2), and a quoted-
// printable value ending in '=' continues on the next line (vCard 2.1).
func unfoldVCard(data []byte) []string {
	text := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\r", "\n")
	var out []string
	qpOpen := false
	for _, l := range strings.Split(text, "\n") {
		switch {
		case len(out) > 0 && (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")):
			out[len(out)-1] += l[1:]
		case len(out) > 0 && qpOpen:
			out[len(out)-1] = strings.TrimSuffix(out[len(out)-1], "=") + l
		default:
			out = append(out, l)
		}
		last := out[len(out)-1]
		head, _, _ := strings.Cut(last, ":")
		qpOpen = strings.Contains(strings.ToUpper(head), "QUOTED-PRINTABLE") && strings.HasSuffix(last, "=")
	}
	return out
}

// splitVCardLine reads "group.NAME;PARAM=x:value" as its upper-case name
// (the group dropped), its parameters and its value. The value starts at the
// first colon outside a quoted parameter value.
func splitVCardLine(line string) (name string, params []string, value string, ok bool) {
	quoted := false
	for i, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
		case r == ':' && !quoted:
			head := strings.Split(line[:i], ";")
			name = strings.ToUpper(head[0])
			if dot := strings.LastIndexByte(name, '.'); dot >= 0 {
				name = name[dot+1:]
			}
			return name, head[1:], line[i+1:], name != ""
		}
	}
	return "", nil, "", false
}

// decodeVCardValue undoes a value's quoted-printable encoding, and reads
// bytes that are not UTF-8 as Latin-1, the charset older exports use.
func decodeVCardValue(params []string, value string) string {
	for _, p := range params {
		if p = strings.ToUpper(p); p == "QUOTED-PRINTABLE" || p == "ENCODING=QUOTED-PRINTABLE" {
			if b, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(value))); err == nil {
				value = string(b)
			}
			break
		}
	}
	if utf8.ValidString(value) {
		return value
	}
	runes := make([]rune, 0, len(value))
	for i := 0; i < len(value); i++ {
		runes = append(runes, rune(value[i]))
	}
	return string(runes)
}

// splitVCardList splits value at each sep that is not escaped, and unescapes
// each part.
func splitVCardList(value string, sep byte) []string {
	var out []string
	var cur strings.Builder
	for i := 0; i < len(value); i++ {
		switch {
		case value[i] == '\\' && i+1 < len(value):
			cur.WriteByte(value[i])
			cur.WriteByte(value[i+1])
			i++
		case value[i] == sep:
			out = append(out, vcardUnescape(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(value[i])
		}
	}
	return append(out, vcardUnescape(cur.String()))
}

var vcardUnescaper = strings.NewReplacer(`\\`, `\`, `\,`, `,`, `\;`, `;`, `\n`, "\n", `\N`, "\n")

func vcardUnescape(s string) string { return vcardUnescaper.Replace(s) }

var vcardEscaper = strings.NewReplacer(`\`, `\\`, `,`, `\,`, `;`, `\;`, "\n", `\n`)

// WriteVCards writes cards as vCard 3.0, which every address book still
// reads, each line folded at 75 bytes without splitting a character.
func WriteVCards(w io.Writer, cards []VCard) error {
	var b bytes.Buffer
	for _, c := range cards {
		name := c.Name
		if name == "" && len(c.Emails) > 0 {
			name = c.Emails[0]
		}
		lines := []string{"BEGIN:VCARD", "VERSION:3.0", "FN:" + vcardEscaper.Replace(name), "N:;" + vcardEscaper.Replace(name) + ";;;"}
		for _, e := range c.Emails {
			lines = append(lines, "EMAIL;TYPE=INTERNET:"+vcardEscaper.Replace(e))
		}
		if len(c.Groups) > 0 {
			groups := make([]string, len(c.Groups))
			for i, g := range c.Groups {
				groups[i] = vcardEscaper.Replace(g)
			}
			lines = append(lines, "CATEGORIES:"+strings.Join(groups, ","))
		}
		lines = append(lines, "END:VCARD")
		for _, l := range lines {
			writeFolded(&b, l)
		}
	}
	_, err := w.Write(b.Bytes())
	return err
}

func writeFolded(b *bytes.Buffer, line string) {
	const limit = 75
	for len(line) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(line[cut]) {
			cut--
		}
		b.WriteString(line[:cut] + "\r\n ")
		line = line[cut:]
	}
	b.WriteString(line + "\r\n")
}

// ContactImport is what an import did.
type ContactImport struct {
	// Saved is how many addresses were saved as contacts, new or refreshed.
	Saved int
	// Skipped is how many addresses, or cards with none, were not.
	Skipped int
	// GroupsLeftOut names the groups not made: an invalid name, or one past
	// the mailbox's 50, or a group already at 500 people.
	GroupsLeftOut []string
}

// ImportContacts saves cards into owner's contacts in one transaction: each
// address a contact named by its card, and in the card's groups. A card with
// no name does not wipe the name a contact already has.
func (s *AccountStore) ImportContacts(ctx context.Context, owner string, cards []VCard) (ContactImport, error) {
	var res ContactImport
	if s.db == nil {
		return res, errors.New("vayumail: no storage")
	}
	if len(cards) > maxImportCards {
		return res, errors.New("a file of at most 5000 contacts can be brought in at once")
	}
	owner = normEmail(owner)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback() }()
	// The groups as they stand, by the name compared without case, with how
	// many each holds: the caps are checked as the import fills them.
	size := map[string]int{}
	rows, err := tx.QueryContext(ctx, `SELECT LOWER(name), COUNT(1) FROM vayumail_contact_groups WHERE owner=? GROUP BY LOWER(name)`, owner)
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var g string
		var n int
		if err := rows.Scan(&g, &n); err != nil {
			_ = rows.Close()
			return res, err
		}
		size[g] = n
	}
	if err := rows.Close(); err != nil {
		return res, err
	}
	leftOut := map[string]bool{}
	for _, c := range cards {
		name := strings.TrimSpace(c.Name)
		if r := []rune(name); len(r) > 200 {
			name = string(r[:200])
		}
		if len(c.Emails) == 0 {
			res.Skipped++
		}
		for _, e := range c.Emails {
			e = normEmail(e)
			if !strings.Contains(e, "@") || e == owner {
				res.Skipped++
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO vayumail_contacts(owner,email,name) VALUES(?,?,?)
				ON CONFLICT(owner,email) DO UPDATE SET name=CASE WHEN excluded.name<>'' THEN excluded.name ELSE vayumail_contacts.name END`, owner, e, name); err != nil {
				return res, err
			}
			res.Saved++
			for _, g := range c.Groups {
				key := strings.ToLower(g)
				n, exists := size[key]
				if !ValidGroupName(g) || (!exists && len(size) >= maxContactGroups) || n >= maxContactGroupMembers {
					leftOut[g] = true
					continue
				}
				r, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO vayumail_contact_groups(owner,name,email) VALUES(?,?,?)`, owner, g, e)
				if err != nil {
					return res, err
				}
				if added, _ := r.RowsAffected(); added > 0 {
					size[key] = n + 1
				}
			}
		}
	}
	for g := range leftOut {
		res.GroupsLeftOut = append(res.GroupsLeftOut, g)
	}
	sort.Strings(res.GroupsLeftOut)
	return res, tx.Commit()
}

// ContactsAsVCards is owner's address book as cards, one an address, each
// with the groups it is in.
func (s *AccountStore) ContactsAsVCards(ctx context.Context, owner string) ([]VCard, error) {
	contacts, err := s.ListContacts(ctx, owner)
	if err != nil {
		return nil, err
	}
	groups, err := s.ContactGroups(ctx, owner)
	if err != nil {
		return nil, err
	}
	in := groupsByContact(groups)
	out := make([]VCard, 0, len(contacts))
	for _, c := range contacts {
		out = append(out, VCard{Name: c.Name, Emails: []string{c.Email}, Groups: in[c.Email]})
	}
	return out, nil
}
