// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// imapimport_client.go — the IMAP client Bring mail in reads another provider
// with. It speaks only what an import needs (sign in, list, examine, search
// and fetch by UID), always over TLS: port 993 is TLS from the first byte,
// and on 143 the session must upgrade with STARTTLS before the password is
// sent, or it is not sent at all.

// importMaxLiteral bounds one literal (a message) a server may send; past it
// the session is ended rather than buffered.
const importMaxLiteral = 64 << 20

// importCommandTimeout bounds each command's round trip.
const importCommandTimeout = 2 * time.Minute

type imapClient struct {
	conn net.Conn
	r    *bufio.Reader
	n    int
	caps string
}

// imapResponse is one response line, with each literal it carried kept
// aside in order; the line keeps a {n} marker where each one was.
type imapResponse struct {
	text string
	lits [][]byte
}

var literalAtEnd = regexp.MustCompile(`\{(\d+)\+?\}$`)

// importPortAllowed refuses any port but IMAP's two.
func importPortAllowed(port int) error {
	if port != 993 && port != 143 {
		return errors.New("the port must be 993 (TLS) or 143 (STARTTLS)")
	}
	return nil
}

// dialIMAP opens a TLS session to host:port through dial, and signs in.
// Only 993 and 143 are accepted (importPortAllowed): an import names an IMAP
// server, and any other port would make it a way to probe arbitrary services.
func dialIMAP(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), tlsConf *tls.Config, host string, port int, user, pass string) (*imapClient, error) {
	if err := importPortAllowed(port); err != nil {
		return nil, err
	}
	raw, err := dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", host, err)
	}
	conf := tlsConf.Clone()
	if conf == nil {
		conf = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	conf.ServerName = host
	c := &imapClient{conn: raw}
	if port == 993 {
		c.conn = tls.Client(raw, conf)
	}
	c.r = bufio.NewReaderSize(c.conn, 64<<10)
	_ = c.conn.SetDeadline(time.Now().Add(importCommandTimeout))
	greet, err := c.readResponse()
	if err != nil {
		_ = c.conn.Close()
		return nil, fmt.Errorf("%s did not answer as an IMAP server: %w", host, err)
	}
	if !strings.HasPrefix(greet.text, "* OK") {
		_ = c.conn.Close()
		return nil, fmt.Errorf("%s refused the connection: %s", host, greet.text)
	}
	if port == 143 {
		if _, err := c.command("STARTTLS"); err != nil {
			_ = c.conn.Close()
			return nil, errors.New("this server does not offer STARTTLS on 143, so the password would travel readable; use 993")
		}
		c.conn = tls.Client(c.conn, conf)
		c.r = bufio.NewReaderSize(c.conn, 64<<10)
	}
	if err := c.login(user, pass); err != nil {
		_ = c.conn.Close()
		return nil, err
	}
	return c, nil
}

// login signs in with SASL PLAIN when offered, else LOGIN with quoted
// strings; a password a quoted string cannot carry is refused rather than
// sent another way.
func (c *imapClient) login(user, pass string) error {
	caps, err := c.command("CAPABILITY")
	if err != nil {
		return err
	}
	for _, l := range caps {
		c.caps += " " + strings.ToUpper(l.text)
	}
	if strings.Contains(c.caps, "AUTH=PLAIN") {
		c.n++
		tag := "a" + strconv.Itoa(c.n)
		if err := c.send(tag + " AUTHENTICATE PLAIN"); err != nil {
			return err
		}
		cont, err := c.readResponse()
		if err != nil || !strings.HasPrefix(cont.text, "+") {
			return errors.New("the server refused to sign in")
		}
		if err := c.send(base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + pass))); err != nil {
			return err
		}
		if _, err := c.finish(tag); err != nil {
			return errors.New("the server refused the user name or password (some providers, Gmail among them, need an app password)")
		}
		return nil
	}
	qu, ok1 := imapQuote(user)
	qp, ok2 := imapQuote(pass)
	if !ok1 || !ok2 {
		return errors.New("this server signs in only in a way that cannot carry this password")
	}
	if _, err := c.command("LOGIN " + qu + " " + qp); err != nil {
		return errors.New("the server refused the user name or password (some providers, Gmail among them, need an app password)")
	}
	return nil
}

// imapQuote is s as an IMAP quoted string, and whether it can be one.
func imapQuote(s string) (string, bool) {
	for _, r := range s {
		if r == '\r' || r == '\n' || r > 126 {
			return "", false
		}
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`, true
}

func (c *imapClient) send(line string) error {
	_ = c.conn.SetDeadline(time.Now().Add(importCommandTimeout))
	_, err := io.WriteString(c.conn, line+"\r\n")
	return err
}

// command runs one command and returns its untagged responses; a NO or BAD
// is an error carrying the server's words.
func (c *imapClient) command(cmd string) ([]imapResponse, error) {
	c.n++
	tag := "a" + strconv.Itoa(c.n)
	if err := c.send(tag + " " + cmd); err != nil {
		return nil, err
	}
	return c.finish(tag)
}

func (c *imapClient) finish(tag string) ([]imapResponse, error) {
	var out []imapResponse
	for {
		resp, err := c.readResponse()
		if err != nil {
			return out, err
		}
		if rest, ok := strings.CutPrefix(resp.text, tag+" "); ok {
			if strings.HasPrefix(strings.ToUpper(rest), "OK") {
				return out, nil
			}
			return out, errors.New(rest)
		}
		out = append(out, resp)
	}
}

// readResponse reads one response line and the literals it carries.
func (c *imapClient) readResponse() (imapResponse, error) {
	var resp imapResponse
	var sb strings.Builder
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return resp, err
		}
		if sb.Len()+len(line) > importMaxLiteral {
			return resp, errors.New("a response line too long to read")
		}
		line = strings.TrimRight(line, "\r\n")
		sb.WriteString(line)
		m := literalAtEnd.FindStringSubmatch(line)
		if m == nil {
			resp.text = sb.String()
			return resp, nil
		}
		n, _ := strconv.Atoi(m[1])
		if n > importMaxLiteral {
			return resp, fmt.Errorf("a message of %d bytes, larger than an import takes", n)
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return resp, err
		}
		resp.lits = append(resp.lits, buf)
	}
}

func (c *imapClient) close() {
	_, _ = c.command("LOGOUT")
	_ = c.conn.Close()
}

// remoteFolder is one folder as LIST names it.
type remoteFolder struct {
	name  string // as the server spells it (modified UTF-7)
	attrs string // lower case
	delim string
}

// list returns every folder the account has.
func (c *imapClient) list() ([]remoteFolder, error) {
	resps, err := c.command(`LIST "" "*"`)
	if err != nil {
		return nil, err
	}
	var out []remoteFolder
	for _, r := range resps {
		rest, ok := strings.CutPrefix(r.text, "* LIST ")
		if !ok {
			continue
		}
		toks := imapTokens(rest, r.lits)
		if len(toks) < 3 {
			continue
		}
		out = append(out, remoteFolder{attrs: strings.ToLower(toks[0]), delim: toks[1], name: toks[2]})
	}
	return out, nil
}

// imapTokens splits a LIST response's rest into its parenthesised group,
// strings, atoms and literals.
func imapTokens(s string, lits [][]byte) []string {
	var out []string
	li := 0
	for i := 0; i < len(s); {
		switch s[i] {
		case ' ':
			i++
		case '(':
			j := strings.IndexByte(s[i:], ')')
			if j < 0 {
				return out
			}
			out = append(out, s[i+1:i+j])
			i += j + 1
		case '"':
			var b strings.Builder
			j := i + 1
			for ; j < len(s) && s[j] != '"'; j++ {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
			}
			out = append(out, b.String())
			i = j + 1
		case '{':
			j := strings.IndexByte(s[i:], '}')
			if j < 0 || li >= len(lits) {
				return out
			}
			out = append(out, string(lits[li]))
			li++
			i += j + 1
		default:
			j := strings.IndexByte(s[i:], ' ')
			if j < 0 {
				j = len(s) - i
			}
			tok := s[i : i+j]
			if strings.EqualFold(tok, "NIL") {
				tok = ""
			}
			out = append(out, tok)
			i += j
		}
	}
	return out
}

var uidValidityRe = regexp.MustCompile(`(?i)\[UIDVALIDITY (\d+)\]`)

// examine opens a folder read-only and returns its UIDVALIDITY.
func (c *imapClient) examine(name string) (uint64, error) {
	q, ok := imapQuote(name)
	if !ok {
		return 0, fmt.Errorf("a folder name that cannot be sent: %q", name)
	}
	resps, err := c.command("EXAMINE " + q)
	if err != nil {
		return 0, err
	}
	for _, r := range resps {
		if m := uidValidityRe.FindStringSubmatch(r.text); m != nil {
			return strconv.ParseUint(m[1], 10, 32)
		}
	}
	return 0, errors.New("the server gave no UIDVALIDITY")
}

// uidsAfter lists the UIDs in the open folder greater than last, in order.
// "n:*" always names the last message even when its UID is below n, so the
// answer is filtered as well as asked for.
func (c *imapClient) uidsAfter(last uint64) ([]uint64, error) {
	resps, err := c.command("UID SEARCH UID " + strconv.FormatUint(last+1, 10) + ":*")
	if err != nil {
		return nil, err
	}
	var out []uint64
	for _, r := range resps {
		rest, ok := strings.CutPrefix(r.text, "* SEARCH")
		if !ok {
			continue
		}
		for _, f := range strings.Fields(rest) {
			if u, err := strconv.ParseUint(f, 10, 32); err == nil && u > last {
				out = append(out, u)
			}
		}
	}
	return out, nil
}

// fetched is one message as FETCH returned it.
type fetched struct {
	uid     uint64
	seen    bool
	flagged bool
	body    []byte
}

var (
	fetchUIDRe   = regexp.MustCompile(`(?i)\bUID (\d+)`)
	fetchFlagsRe = regexp.MustCompile(`(?i)\bFLAGS \(([^)]*)\)`)
)

// fetch reads whole messages by UID, without marking them read there.
func (c *imapClient) fetch(uids []uint64) ([]fetched, error) {
	set := make([]string, len(uids))
	for i, u := range uids {
		set[i] = strconv.FormatUint(u, 10)
	}
	resps, err := c.command("UID FETCH " + strings.Join(set, ",") + " (UID FLAGS BODY.PEEK[])")
	if err != nil {
		return nil, err
	}
	var out []fetched
	for _, r := range resps {
		if !strings.Contains(strings.ToUpper(r.text), " FETCH (") || len(r.lits) == 0 {
			continue
		}
		m := fetchUIDRe.FindStringSubmatch(r.text)
		if m == nil {
			continue
		}
		uid, _ := strconv.ParseUint(m[1], 10, 32)
		f := fetched{uid: uid, body: r.lits[0]}
		if fm := fetchFlagsRe.FindStringSubmatch(r.text); fm != nil {
			flags := strings.ToLower(fm[1])
			f.seen = strings.Contains(flags, `\seen`)
			f.flagged = strings.Contains(flags, `\flagged`)
		}
		out = append(out, f)
	}
	return out, nil
}

// decodeMUTF7 decodes IMAP's modified UTF-7 folder names (RFC 3501 5.1.3):
// "&" starts base64 of UTF-16BE with ',' for '/', "-" ends it, "&-" is "&".
// A name it cannot decode is returned as it was.
func decodeMUTF7(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '&' {
			b.WriteByte(s[i])
			continue
		}
		j := strings.IndexByte(s[i:], '-')
		if j < 0 {
			return s
		}
		enc := s[i+1 : i+j]
		i += j
		if enc == "" {
			b.WriteByte('&')
			continue
		}
		raw, err := base64.RawStdEncoding.DecodeString(strings.ReplaceAll(enc, ",", "/"))
		if err != nil || len(raw)%2 != 0 {
			return s
		}
		u := make([]uint16, len(raw)/2)
		for k := range u {
			u[k] = uint16(raw[2*k])<<8 | uint16(raw[2*k+1])
		}
		b.WriteString(string(utf16.Decode(u)))
	}
	return b.String()
}
