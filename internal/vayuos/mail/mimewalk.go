package mail

import (
	"bufio"
	"bytes"
	"fmt"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
)

// mimewalk.go — parse a stored RFC 822 message into the IMAP FETCH data items
// real clients depend on: ENVELOPE (structured headers), BODYSTRUCTURE (the MIME
// tree, so a client knows there's an HTML part and attachments without fetching
// the whole message), and BODY[<section>] extraction (HEADER, TEXT, numbered
// parts). Everything is best-effort: a message that does not parse cleanly still
// yields a usable single-part structure rather than an error, so a malformed
// message never breaks a client's mailbox sync.

// part is one node of a parsed MIME tree.
type part struct {
	header      textproto.MIMEHeader
	headerBytes []byte // raw (top) or canonical (child) header block, CRLF-terminated
	content     []byte // this part's body content (multipart: the bytes holding sub-parts)
	typ         string // lower-case major type, e.g. "text"
	subtype     string // lower-case subtype, e.g. "plain"
	params      map[string]string
	encoding    string
	id          string
	desc        string
	disp        string
	dispParams  map[string]string
	isMultipart bool
	children    []*part
}

// splitHeaderBody divides a raw message/part into its header block (including the
// terminating blank line) and body.
func splitHeaderBody(raw []byte) (header, body []byte) {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i+4], raw[i+4:]
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return raw[:i+2], raw[i+2:]
	}
	return raw, nil
}

func parseHeaderBytes(headerBytes []byte) textproto.MIMEHeader {
	tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(ensureBlankLine(headerBytes))))
	h, _ := tp.ReadMIMEHeader()
	if h == nil {
		h = textproto.MIMEHeader{}
	}
	return h
}

// ensureBlankLine guarantees the header bytes end with a blank line so
// textproto can parse them.
func ensureBlankLine(b []byte) []byte {
	if bytes.HasSuffix(b, []byte("\r\n\r\n")) || bytes.HasSuffix(b, []byte("\n\n")) {
		return b
	}
	return append(append([]byte{}, b...), []byte("\r\n\r\n")...)
}

// serializeHeader renders a parsed header back to canonical bytes (used for
// synthesized child-part headers).
func serializeHeader(h textproto.MIMEHeader) []byte {
	var b bytes.Buffer
	for k, vs := range h {
		for _, v := range vs {
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(v)
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\r\n")
	return b.Bytes()
}

// parseMessage parses raw into a MIME tree.
func parseMessage(raw []byte) *part {
	hb, body := splitHeaderBody(raw)
	return buildPart(parseHeaderBytes(hb), hb, body)
}

func buildPart(h textproto.MIMEHeader, headerBytes, content []byte) *part {
	p := &part{
		header:      h,
		headerBytes: headerBytes,
		content:     content,
		typ:         "text",
		subtype:     "plain",
		params:      map[string]string{},
		encoding:    "7bit",
	}
	if enc := strings.TrimSpace(h.Get("Content-Transfer-Encoding")); enc != "" {
		p.encoding = enc
	}
	p.id = strings.TrimSpace(h.Get("Content-ID"))
	p.desc = strings.TrimSpace(h.Get("Content-Description"))
	if ct := h.Get("Content-Type"); ct != "" {
		if mt, params, err := mime.ParseMediaType(ct); err == nil {
			if i := strings.IndexByte(mt, '/'); i >= 0 {
				p.typ = strings.ToLower(mt[:i])
				p.subtype = strings.ToLower(mt[i+1:])
			}
			p.params = params
		}
	}
	if cd := h.Get("Content-Disposition"); cd != "" {
		if dt, dp, err := mime.ParseMediaType(cd); err == nil {
			p.disp = dt
			p.dispParams = dp
		}
	}
	if p.typ == "multipart" {
		if boundary := p.params["boundary"]; boundary != "" {
			p.isMultipart = true
			mr := multipart.NewReader(bytes.NewReader(content), boundary)
			for {
				mp, err := mr.NextRawPart()
				if err != nil {
					break
				}
				childContent, _ := readAllLimited(mp)
				childHdrBytes := serializeHeader(mp.Header)
				p.children = append(p.children, buildPart(mp.Header, childHdrBytes, childContent))
			}
		}
	}
	return p
}

func readAllLimited(r *multipart.Part) ([]byte, error) {
	var buf bytes.Buffer
	// Cap a single part at the configured max message size to bound memory.
	_, err := buf.ReadFrom(r)
	return buf.Bytes(), err
}

// ── ENVELOPE ─────────────────────────────────────────────────────────────────

// renderEnvelope builds the IMAP ENVELOPE structure for a message header.
func renderEnvelope(h textproto.MIMEHeader) string {
	date := imapStr(h.Get("Date"))
	subject := imapStr(decodeWord(h.Get("Subject")))
	from := addrList(h.Get("From"))
	sender := from
	if v := h.Get("Sender"); v != "" {
		sender = addrList(v)
	}
	replyTo := from
	if v := h.Get("Reply-To"); v != "" {
		replyTo = addrList(v)
	}
	to := addrList(h.Get("To"))
	cc := addrList(h.Get("Cc"))
	bcc := addrList(h.Get("Bcc"))
	inReplyTo := imapStr(h.Get("In-Reply-To"))
	messageID := imapStr(h.Get("Message-ID"))
	return "(" + date + " " + subject + " " + from + " " + sender + " " + replyTo + " " +
		to + " " + cc + " " + bcc + " " + inReplyTo + " " + messageID + ")"
}

// addrList renders an address header as an IMAP parenthesized address list, or
// NIL when empty/unparseable.
func addrList(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "NIL"
	}
	addrs, err := mail.ParseAddressList(v)
	if err != nil || len(addrs) == 0 {
		// Fall back to a single best-effort address from the raw value.
		if at := strings.LastIndexByte(v, '@'); at >= 0 {
			local := strings.Trim(v[:at], " <\"")
			host := strings.Trim(v[at+1:], " >\"")
			return "((NIL NIL " + imapStr(local) + " " + imapStr(host) + "))"
		}
		return "NIL"
	}
	var b strings.Builder
	b.WriteString("(")
	for _, a := range addrs {
		name := imapStr(a.Name)
		mbox, host := "NIL", "NIL"
		if at := strings.LastIndexByte(a.Address, '@'); at >= 0 {
			mbox = imapStr(a.Address[:at])
			host = imapStr(a.Address[at+1:])
		} else if a.Address != "" {
			mbox = imapStr(a.Address)
		}
		b.WriteString("(" + name + " NIL " + mbox + " " + host + ")")
	}
	b.WriteString(")")
	return b.String()
}

// decodeWord decodes RFC 2047 encoded-words (=?utf-8?...?=) best-effort.
func decodeWord(s string) string {
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// ── BODYSTRUCTURE ────────────────────────────────────────────────────────────

// renderBodyStructure builds the IMAP BODY (ext=false) or BODYSTRUCTURE
// (ext=true) for a parsed part tree.
func renderBodyStructure(p *part, ext bool) string {
	if p.isMultipart {
		var b strings.Builder
		b.WriteString("(")
		if len(p.children) == 0 {
			// A multipart with no parseable parts — present an empty text part so
			// the structure stays well-formed.
			b.WriteString(`("TEXT" "PLAIN" NIL NIL NIL "7BIT" 0 0)`)
		}
		for _, c := range p.children {
			b.WriteString(renderBodyStructure(c, ext))
		}
		b.WriteString(" ")
		b.WriteString(imapStr(strings.ToUpper(p.subtype)))
		if ext {
			b.WriteString(" " + paramList(p.params) + " NIL NIL")
		}
		b.WriteString(")")
		return b.String()
	}
	size := len(p.content)
	var b strings.Builder
	b.WriteString("(")
	b.WriteString(imapStr(strings.ToUpper(p.typ)) + " " + imapStr(strings.ToUpper(p.subtype)) + " ")
	b.WriteString(paramList(p.params) + " ")
	b.WriteString(imapStr(p.id) + " ")
	b.WriteString(imapStr(p.desc) + " ")
	b.WriteString(imapStr(strings.ToUpper(p.encoding)) + " ")
	b.WriteString(strconv.Itoa(size))
	if p.typ == "text" {
		b.WriteString(" " + strconv.Itoa(countLines(p.content)))
	}
	if ext {
		// md5(NIL) disposition language location
		b.WriteString(" NIL " + dispositionField(p) + " NIL NIL")
	}
	b.WriteString(")")
	return b.String()
}

func dispositionField(p *part) string {
	if p.disp == "" {
		return "NIL"
	}
	return "(" + imapStr(strings.ToUpper(p.disp)) + " " + paramList(p.dispParams) + ")"
}

func paramList(params map[string]string) string {
	if len(params) == 0 {
		return "NIL"
	}
	var b strings.Builder
	b.WriteString("(")
	first := true
	for k, v := range params {
		if !first {
			b.WriteString(" ")
		}
		first = false
		b.WriteString(imapStr(strings.ToUpper(k)) + " " + imapStr(v))
	}
	b.WriteString(")")
	return b.String()
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return bytes.Count(b, []byte("\n"))
}

// imapStr renders a Go string as an IMAP quoted string, or NIL when empty.
func imapStr(s string) string {
	if s == "" {
		return "NIL"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// ── BODY[<section>] extraction ───────────────────────────────────────────────

// fetchSection returns the bytes for a BODY[<section>] request against raw.
// Supported sections: "" (whole), HEADER, TEXT, HEADER.FIELDS (..),
// HEADER.FIELDS.NOT (..), and numbered parts (1, 1.2, 1.MIME, 2.TEXT, ...).
func fetchSection(raw []byte, root *part, section string) ([]byte, bool) {
	s := strings.TrimSpace(section)
	switch {
	case s == "":
		return raw, true
	case strings.EqualFold(s, "HEADER"):
		hb, _ := splitHeaderBody(raw)
		return hb, true
	case strings.EqualFold(s, "TEXT"):
		_, body := splitHeaderBody(raw)
		return body, true
	case strings.HasPrefix(strings.ToUpper(s), "HEADER.FIELDS"):
		hb, _ := splitHeaderBody(raw)
		return filterHeaderFields(hb, s), true
	}
	if root == nil {
		return nil, false
	}
	return navigateSection(root, s)
}

// navigateSection walks a dotted part path (e.g. "1.2", "1.MIME", "2.TEXT").
func navigateSection(root *part, sec string) ([]byte, bool) {
	toks := strings.Split(sec, ".")
	cur := root
	for i, t := range toks {
		if n, err := strconv.Atoi(t); err == nil {
			if cur.isMultipart {
				if n < 1 || n > len(cur.children) {
					return nil, false
				}
				cur = cur.children[n-1]
				continue
			}
			// Non-multipart: part 1 refers to the singleton body itself.
			if n != 1 {
				return nil, false
			}
			if i == len(toks)-1 {
				return cur.content, true
			}
			continue
		}
		switch strings.ToUpper(t) {
		case "MIME", "HEADER":
			return cur.headerBytes, true
		case "TEXT":
			return cur.content, true
		default:
			return nil, false
		}
	}
	return cur.content, true
}

// filterHeaderFields returns only the header lines named in a
// "HEADER.FIELDS (To From ...)" / "HEADER.FIELDS.NOT (...)" section spec,
// terminated by a blank line (per RFC 3501).
func filterHeaderFields(headerBytes []byte, spec string) []byte {
	not := strings.Contains(strings.ToUpper(spec), "HEADER.FIELDS.NOT")
	names := map[string]bool{}
	if i := strings.IndexByte(spec, '('); i >= 0 {
		if j := strings.LastIndexByte(spec, ')'); j > i {
			for _, f := range strings.Fields(spec[i+1 : j]) {
				names[strings.ToLower(strings.Trim(f, `"`))] = true
			}
		}
	}
	var out bytes.Buffer
	lines := strings.Split(strings.ReplaceAll(string(headerBytes), "\r\n", "\n"), "\n")
	include := false
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' { // folded continuation
			if include {
				out.WriteString(ln)
				out.WriteString("\r\n")
			}
			continue
		}
		key := ln
		if c := strings.IndexByte(ln, ':'); c >= 0 {
			key = ln[:c]
		}
		want := names[strings.ToLower(strings.TrimSpace(key))]
		if not {
			want = !want
		}
		include = want
		if include {
			out.WriteString(ln)
			out.WriteString("\r\n")
		}
	}
	out.WriteString("\r\n")
	return out.Bytes()
}

// applyPartialFetch returns the requested <offset.size> slice of b (BODY[..]<o.s>)
// and the octet offset for the response tag. ok is false when no partial spec.
func applyPartialFetch(b []byte, spec string) (out []byte, origin int, partial bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return b, 0, false
	}
	var off, size int
	if _, err := fmt.Sscanf(spec, "%d.%d", &off, &size); err != nil {
		if _, err2 := fmt.Sscanf(spec, "%d", &off); err2 != nil {
			return b, 0, false
		}
		size = len(b)
	}
	if off < 0 || off > len(b) {
		return []byte{}, off, true
	}
	end := off + size
	if end > len(b) {
		end = len(b)
	}
	return b[off:end], off, true
}
