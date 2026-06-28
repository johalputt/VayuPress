package mail

import (
	"bufio"
	"fmt"
	"strings"
	"time"
)

// imapfetch.go — parse and render IMAP FETCH data items. Real clients lean on
// FETCH heavily: FLAGS/UID for sync, ENVELOPE + BODYSTRUCTURE to render a message
// list and decide what to download, and BODY[<section>] (often BODY.PEEK) to pull
// just the part they need. This file turns a stored message into those items.

// imapMsg is one message in the selected mailbox's snapshot.
type imapMsg struct {
	seq   int
	uid   uint32
	id    string // folder-relative id ("new/x" or "cur/x")
	base  string // immutable base name (UID key)
	size  int64
	flags map[byte]bool
	date  time.Time
}

// imapInternalDate formats a time as an IMAP INTERNALDATE.
func imapInternalDate(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("02-Jan-2006 15:04:05 -0700")
}

// splitFetchItems splits a FETCH item spec into top-level items, keeping
// bracketed (BODY[...]) and parenthesised (HEADER.FIELDS (...)) groups intact.
func splitFetchItems(spec string) []string {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "(") && strings.HasSuffix(spec, ")") {
		spec = strings.TrimSpace(spec[1 : len(spec)-1])
	}
	var items []string
	var cur strings.Builder
	depthParen, depthBracket := 0, 0
	for _, r := range spec {
		switch r {
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		}
		if r == ' ' && depthParen == 0 && depthBracket == 0 {
			if cur.Len() > 0 {
				items = append(items, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		items = append(items, cur.String())
	}
	return expandFetchMacros(items)
}

// expandFetchMacros expands ALL/FAST/FULL into their constituent data items.
func expandFetchMacros(items []string) []string {
	for _, it := range items {
		switch strings.ToUpper(it) {
		case "ALL":
			return []string{"FLAGS", "INTERNALDATE", "RFC822.SIZE", "ENVELOPE"}
		case "FAST":
			return []string{"FLAGS", "INTERNALDATE", "RFC822.SIZE"}
		case "FULL":
			return []string{"FLAGS", "INTERNALDATE", "RFC822.SIZE", "ENVELOPE", "BODY"}
		}
	}
	return items
}

// writeFetchResponse writes one untagged FETCH response for m. When a non-peek
// body item is requested it sets \Seen (returning markSeen=true so the caller
// can persist the flag). uidRequested forces a UID field (used by UID FETCH).
func (s *IMAPServer) writeFetchResponse(w *bufio.Writer, sess *imapSession, m *imapMsg, items []string, uidRequested bool) bool {
	var raw []byte
	var tree *part
	rawLoaded := false
	loadRaw := func() {
		if rawLoaded {
			return
		}
		rawLoaded = true
		b, err := s.maildir.ReadRawFolder(s.cfg.Domain, sess.authedUser, sess.selected, m.id)
		if err != nil {
			b = []byte{}
		}
		if s.decrypt != nil {
			b = s.decrypt(sess.authedMail, b)
		}
		raw = b
		tree = parseMessage(raw)
	}

	markSeen := false
	// First pass: build the inline (non-literal) fields and queue literal writes.
	type lit struct {
		label string
		data  []byte
	}
	var inline []string
	var literals []lit
	ensureUID := uidRequested

	for _, it := range items {
		up := strings.ToUpper(it)
		switch {
		case up == "UID":
			ensureUID = true
		case up == "FLAGS":
			inline = append(inline, "FLAGS ("+strings.Join(imapFlagTokens(m.flags), " ")+")")
		case up == "INTERNALDATE":
			inline = append(inline, fmt.Sprintf(`INTERNALDATE "%s"`, imapInternalDate(m.date)))
		case up == "RFC822.SIZE":
			inline = append(inline, fmt.Sprintf("RFC822.SIZE %d", m.size))
		case up == "ENVELOPE":
			loadRaw()
			inline = append(inline, "ENVELOPE "+renderEnvelope(tree.header))
		case up == "BODYSTRUCTURE":
			loadRaw()
			inline = append(inline, "BODYSTRUCTURE "+renderBodyStructure(tree, true))
		case up == "BODY":
			loadRaw()
			inline = append(inline, "BODY "+renderBodyStructure(tree, false))
		case up == "RFC822":
			loadRaw()
			literals = append(literals, lit{"RFC822", raw})
			markSeen = true
		case up == "RFC822.HEADER":
			loadRaw()
			hb, _ := splitHeaderBody(raw)
			literals = append(literals, lit{"RFC822.HEADER", hb})
		case up == "RFC822.TEXT":
			loadRaw()
			_, body := splitHeaderBody(raw)
			literals = append(literals, lit{"RFC822.TEXT", body})
			markSeen = true
		case strings.HasPrefix(up, "BODY.PEEK[") || strings.HasPrefix(up, "BODY["):
			loadRaw()
			peek := strings.HasPrefix(up, "BODY.PEEK[")
			section, partial := parseBodyBrackets(it)
			data, ok := fetchSection(raw, tree, section)
			if !ok {
				data = []byte{}
			}
			out, origin, isPartial := applyPartialFetch(data, partial)
			label := "BODY[" + section + "]"
			if isPartial {
				label += fmt.Sprintf("<%d>", origin)
			}
			literals = append(literals, lit{label, out})
			if !peek {
				markSeen = true
			}
		}
	}

	if ensureUID {
		inline = append([]string{fmt.Sprintf("UID %d", m.uid)}, inline...)
	}

	// Emit: * <seq> FETCH (inline... literalLabel {n}<CRLF>bytes ...)
	_, _ = w.WriteString(fmt.Sprintf("* %d FETCH (", m.seq))
	wrote := false
	writeSep := func() {
		if wrote {
			_, _ = w.WriteString(" ")
		}
		wrote = true
	}
	for _, f := range inline {
		writeSep()
		_, _ = w.WriteString(f)
	}
	for _, l := range literals {
		writeSep()
		_, _ = w.WriteString(fmt.Sprintf("%s {%d}\r\n", l.label, len(l.data)))
		_, _ = w.Write(l.data)
	}
	_, _ = w.WriteString(")\r\n")
	_ = w.Flush()
	return markSeen
}

// parseBodyBrackets extracts the section between [ ] and any <partial> suffix
// from a BODY[..]/BODY.PEEK[..] item.
func parseBodyBrackets(item string) (section, partial string) {
	lb := strings.IndexByte(item, '[')
	rb := strings.LastIndexByte(item, ']')
	if lb < 0 || rb < 0 || rb < lb {
		return "", ""
	}
	section = strings.TrimSpace(item[lb+1 : rb])
	rest := item[rb+1:]
	if i := strings.IndexByte(rest, '<'); i >= 0 {
		if j := strings.IndexByte(rest, '>'); j > i {
			partial = rest[i+1 : j]
		}
	}
	return section, partial
}

// imapFlagTokens renders a Maildir flag set as IMAP system-flag tokens.
func imapFlagTokens(set map[byte]bool) []string {
	out := []string{}
	for _, c := range maildirFlagOrder {
		if set[byte(c)] {
			if f, ok := maildirToIMAPFlag[byte(c)]; ok {
				out = append(out, f)
			}
		}
	}
	return out
}
