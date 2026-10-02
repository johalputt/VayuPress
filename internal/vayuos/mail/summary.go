// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"io"
	"mime"
	netmail "net/mail"
	"net/textproto"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// What a listing reads of each message, and no more. The header block is
// read up to summaryHeaderBytes and the body up to summaryBodyBytes past it,
// so a message carrying a 50 MB attachment costs a few tens of kilobytes to
// list, once, and then nothing until the file changes (headersFor).
//
// The header bound is generous because the fields a listing needs come LAST:
// relays prepend Received lines, and a long chain once took a flat 32 KB
// prefix past From and Subject. Before these bounds the header block had none.
const (
	summaryHeaderBytes = 256 << 10
	summaryBodyBytes   = 32 << 10
	previewRunes       = 140
)

// encryptedPreview stands in for the text of a message nobody but its
// recipient can read. It is the whole preview: ciphertext is never shown.
const encryptedPreview = "Encrypted message"

// summarize reads one message's header block and the start of its body and
// fills h with what the folder listing shows. A file with no readable header
// leaves h as it was, and lists with no sender or subject.
//
// The body facts are taken from the prefix alone (ADR-0162, "What the
// listing knows"): an attachment whose part header begins past it shows no
// mark, which is the price of never reading a whole message to list it.
func summarize(r io.Reader, h *cachedHeaders) {
	lr := &io.LimitedReader{R: r, N: summaryHeaderBytes}
	br := bufio.NewReader(lr)
	hdr, err := textproto.NewReader(br).ReadMIMEHeader()
	if len(hdr) == 0 && err != nil {
		return
	}
	get := hdr.Get
	h.from, h.to, h.subject = get("From"), get("To"), get("Subject")
	if d, derr := netmail.ParseDate(get("Date")); derr == nil {
		h.date, h.hasDate = d, true
	}
	// Threading evidence travels with the cached summary, so grouping a
	// folder costs nothing beyond the stat it already did.
	h.messageID = cleanMessageID(get("Message-Id"))
	h.inReplyTo = cleanMessageID(get("In-Reply-To"))
	h.refs = splitMessageIDs(get("References"))
	if err != nil {
		return // a header block with no end: no body to speak of
	}

	lr.N = summaryBodyBytes // the body's own allowance, past what br holds
	body := io.LimitReader(br, summaryBodyBytes)
	ctype := get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ctype)
	if isEncryptedType(mediaType, params) {
		h.encrypted, h.preview = true, encryptedPreview
		return
	}
	h.signed = isSignedType(mediaType, params)

	var pm ParsedMessage
	collectPart(ctype, get("Content-Transfer-Encoding"), get("Content-Disposition"), body, &pm, 0)
	for _, a := range pm.Attachments {
		if !isSignatureType(a.ContentType) {
			h.attachment = true
			break
		}
	}
	text := pm.Text
	if text == "" && pm.HTML != "" {
		text = htmlText(pm.HTML)
	}
	switch {
	case strings.Contains(text, "-----BEGIN PGP MESSAGE-----"):
		h.encrypted, h.preview = true, encryptedPreview
		return
	case strings.HasPrefix(strings.TrimSpace(text), "-----BEGIN PGP SIGNED MESSAGE-----"):
		h.signed = true
		// The armour's own lines (the marker, then Hash:) end at a blank line.
		if _, rest, ok := strings.Cut(text, "\n\n"); ok {
			text = rest
		} else if _, rest, ok := strings.Cut(text, "\r\n\r\n"); ok {
			text = rest
		}
		text, _, _ = strings.Cut(text, "-----BEGIN PGP SIGNATURE-----")
	}
	h.preview = clip(strings.Join(strings.Fields(text), " "), previewRunes)
}

// isEncryptedType reports whether a message's own Content-Type says its body
// is ciphertext: PGP/MIME (RFC 3156) or S/MIME enveloped data (RFC 8551).
func isEncryptedType(mediaType string, params map[string]string) bool {
	switch mediaType {
	case "multipart/encrypted", "application/pgp-encrypted":
		return true
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		// S/MIME uses this one type for signed-only data too.
		return !strings.EqualFold(params["smime-type"], "signed-data")
	}
	return false
}

// isSignedType reports a PGP/MIME or S/MIME signature on the message as a
// whole. It says the message is signed, not that the signature is good:
// the reader's seal comes from the verdict, never from this.
func isSignedType(mediaType string, params map[string]string) bool {
	switch mediaType {
	case "multipart/signed":
		return true
	case "application/pkcs7-mime", "application/x-pkcs7-mime":
		return strings.EqualFold(params["smime-type"], "signed-data")
	}
	return false
}

// isSignatureType names the detached signature parts a signed message
// carries, which are not attachments anyone sent.
func isSignatureType(mediaType string) bool {
	switch mediaType {
	case "application/pgp-signature", "application/pkcs7-signature", "application/x-pkcs7-signature":
		return true
	}
	return false
}

// htmlText is the visible text of an HTML body: everything outside tags,
// less what a browser does not draw (style, script, head, title). Only a
// block or a line break separates words; an inline tag does not, so
// "Hello <b>Priya</b>," reads "Hello Priya,".
func htmlText(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for b.Len() < previewRunes*4 {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		name, _ := z.TagName()
		switch tt {
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			switch tag := string(name); {
			case tag == "style" || tag == "script" || tag == "head" || tag == "title":
				if tt == html.StartTagToken {
					skip++
				} else if tt == html.EndTagToken && skip > 0 {
					skip--
				}
			case blockTags[tag]:
				b.WriteByte(' ')
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(z.Text())
			}
		}
	}
	return b.String()
}

// blockTags are the elements a browser starts on a new line.
var blockTags = map[string]bool{
	"p": true, "br": true, "div": true, "li": true, "tr": true, "td": true, "th": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "pre": true, "table": true, "ul": true, "ol": true, "hr": true,
}

// clip cuts s to at most n runes, ending in an ellipsis when it cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for k := range s {
		if i == n {
			return strings.TrimSpace(s[:k]) + "…"
		}
		i++
	}
	return s
}
