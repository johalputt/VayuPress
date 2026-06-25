package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// DecryptHook optionally transforms a stored message before it is served to a
// client (e.g. transparent PGP decryption for the owning account). It returns
// the message to serve; on any failure it should return the original bytes.
type DecryptHook func(accountEmail string, raw []byte) []byte

// IMAPServer is a minimal RFC 3501 read server providing standard clients
// (Thunderbird, mobile) access to the Maildir. Authentication is delegated to
// VayuPress accounts via the Bridge. It is started only when inbound mail is
// explicitly enabled.
type IMAPServer struct {
	cfg     Config
	bridge  Bridge
	maildir *Maildir
	decrypt DecryptHook

	tls         *tls.Config
	implicitTLS bool
	listenAddr  string

	ln     net.Listener
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewIMAPServer creates an IMAP server bound to cfg.IMAPListen.
func NewIMAPServer(cfg Config, bridge Bridge, md *Maildir, decrypt DecryptHook) *IMAPServer {
	return &IMAPServer{cfg: cfg, bridge: bridge, maildir: md, decrypt: decrypt, listenAddr: cfg.IMAPListen}
}

// WithTLS enables the STARTTLS command on the plaintext (143) listener.
func (s *IMAPServer) WithTLS(t *tls.Config) *IMAPServer {
	s.tls = t
	return s
}

// WithImplicitTLS turns this into an implicit-TLS IMAPS listener bound to addr
// (993): connections are wrapped in TLS immediately, with no STARTTLS step.
func (s *IMAPServer) WithImplicitTLS(t *tls.Config, addr string) *IMAPServer {
	s.tls = t
	s.implicitTLS = true
	s.listenAddr = addr
	return s
}

// Addr returns the actual listen address (useful with :0 in tests).
func (s *IMAPServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Start begins listening.
func (s *IMAPServer) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("vayumail: imap listen %s: %w", s.listenAddr, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop shuts the listener down.
func (s *IMAPServer) Stop(_ context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	s.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
	s.wg.Wait()
	return nil
}

func (s *IMAPServer) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			c := conn
			if s.implicitTLS && s.tls != nil {
				c = tls.Server(conn, s.tls)
			}
			s.handle(c)
		}()
	}
}

type imapSession struct {
	authedUser string // local username
	authedMail string // full email
	selected   bool
	msgs       []StoredMessage
}

func (sess *imapSession) authed() bool { return sess.authedUser != "" }

func (s *IMAPServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
	var (
		br *bufio.Reader
		w  *bufio.Writer
	)
	setup := func(c net.Conn) {
		br = bufio.NewReader(c)
		w = bufio.NewWriter(c)
	}
	setup(conn)
	line := func(str string) { _, _ = w.WriteString(str + "\r\n"); _ = w.Flush() }

	onTLS := s.implicitTLS
	caps := func() string {
		c := "IMAP4rev1 AUTH=PLAIN"
		if s.tls != nil && !onTLS {
			c += " STARTTLS"
		}
		return c
	}
	line("* OK [CAPABILITY " + caps() + "] VayuMail IMAP ready")

	sess := &imapSession{}
	for {
		raw, err := br.ReadString('\n')
		if err != nil {
			return
		}
		raw = strings.TrimRight(raw, "\r\n")
		tag, rest := cutSpace(raw)
		cmd, arg := cutSpace(rest)
		cmd = strings.ToUpper(cmd)
		switch cmd {
		case "CAPABILITY":
			line("* CAPABILITY " + caps())
			line(tag + " OK CAPABILITY completed")
		case "STARTTLS":
			if s.tls == nil || onTLS {
				line(tag + " NO STARTTLS not available")
				continue
			}
			line(tag + " OK Begin TLS negotiation now")
			tconn := tls.Server(conn, s.tls)
			if herr := tconn.Handshake(); herr != nil {
				return
			}
			conn = tconn
			_ = conn.SetDeadline(time.Now().Add(30 * time.Minute))
			setup(conn)
			onTLS = true
			sess = &imapSession{} // RFC 2595: discard any prior session state
		case "NOOP":
			line(tag + " OK NOOP completed")
		case "LOGIN":
			s.doLogin(line, sess, tag, arg)
		case "LIST":
			if !sess.authed() {
				line(tag + " NO Not authenticated")
				continue
			}
			line(`* LIST () "/" "INBOX"`)
			line(tag + " OK LIST completed")
		case "SELECT", "EXAMINE":
			s.doSelect(line, sess, tag, arg)
		case "FETCH":
			s.doFetch(w, line, sess, tag, arg)
		case "UID":
			sub, subArg := cutSpace(arg)
			if strings.EqualFold(sub, "FETCH") {
				s.doFetch(w, line, sess, tag, subArg)
			} else {
				line(tag + " OK")
			}
		case "STORE":
			s.doStore(line, sess, tag, arg)
		case "CLOSE":
			sess.selected = false
			line(tag + " OK CLOSE completed")
		case "LOGOUT":
			line("* BYE VayuMail logging out")
			line(tag + " OK LOGOUT completed")
			return
		default:
			line(tag + " BAD Command not supported")
		}
	}
}

func (s *IMAPServer) doLogin(line func(string), sess *imapSession, tag, arg string) {
	user, pass := parseLoginArgs(arg)
	if user == "" {
		line(tag + " BAD LOGIN requires username and password")
		return
	}
	ok := false
	if s.bridge != nil {
		if got, err := s.bridge.AuthUser(user, pass); err == nil {
			ok = got
		}
	}
	if !ok {
		line(tag + " NO [AUTHENTICATIONFAILED] Invalid credentials")
		return
	}
	local := user
	mailAddr := user
	if i := strings.IndexByte(user, '@'); i >= 0 {
		local = user[:i]
	} else {
		mailAddr = user + "@" + s.cfg.Domain
	}
	sess.authedUser = local
	sess.authedMail = mailAddr
	line(tag + " OK LOGIN completed")
}

func (s *IMAPServer) doSelect(line func(string), sess *imapSession, tag, arg string) {
	if !sess.authed() {
		line(tag + " NO Not authenticated")
		return
	}
	mbox := strings.Trim(strings.TrimSpace(arg), `"`)
	if !strings.EqualFold(mbox, "INBOX") {
		line(tag + " NO [NONEXISTENT] Only INBOX is available")
		return
	}
	msgs, err := s.maildir.List(s.cfg.Domain, sess.authedUser)
	if err != nil {
		line(tag + " NO Cannot open mailbox")
		return
	}
	sess.msgs = msgs
	sess.selected = true
	unseen := 0
	for _, m := range msgs {
		if !m.Seen {
			unseen++
		}
	}
	line(fmt.Sprintf("* %d EXISTS", len(msgs)))
	line(fmt.Sprintf("* %d RECENT", unseen))
	line(`* FLAGS (\Seen \Answered \Flagged \Deleted \Draft)`)
	line(fmt.Sprintf("* OK [UIDVALIDITY %d] UIDs valid", 1))
	line(fmt.Sprintf("* OK [UIDNEXT %d] Predicted next UID", len(msgs)+1))
	line(tag + " OK [READ-WRITE] SELECT completed")
}

func (s *IMAPServer) doFetch(w *bufio.Writer, line func(string), sess *imapSession, tag, arg string) {
	if !sess.selected {
		line(tag + " NO No mailbox selected")
		return
	}
	seqPart, items := cutSpace(arg)
	up := strings.ToUpper(items)
	lo, hi := parseSeqSet(seqPart, len(sess.msgs))
	wantBody := strings.Contains(up, "BODY") || strings.Contains(up, "RFC822")
	for seq := lo; seq <= hi; seq++ {
		if seq < 1 || seq > len(sess.msgs) {
			continue
		}
		m := sess.msgs[seq-1]
		var fields []string
		if strings.Contains(up, "UID") {
			fields = append(fields, fmt.Sprintf("UID %d", seq))
		}
		if strings.Contains(up, "FLAGS") {
			if m.Seen {
				fields = append(fields, `FLAGS (\Seen)`)
			} else {
				fields = append(fields, "FLAGS ()")
			}
		}
		if strings.Contains(up, "RFC822.SIZE") {
			fields = append(fields, fmt.Sprintf("RFC822.SIZE %d", m.Size))
		}
		if strings.Contains(up, "INTERNALDATE") {
			fields = append(fields, fmt.Sprintf(`INTERNALDATE "%s"`, m.Date.Format("02-Jan-2006 15:04:05 -0700")))
		}
		if wantBody {
			body, err := s.maildir.ReadRaw(s.cfg.Domain, sess.authedUser, m.ID)
			if err != nil {
				body = []byte{}
			}
			if s.decrypt != nil {
				body = s.decrypt(sess.authedMail, body)
			}
			label := "BODY[]"
			if strings.Contains(up, "RFC822") && !strings.Contains(up, "BODY") {
				label = "RFC822"
			}
			prefix := strings.Join(fields, " ")
			if prefix != "" {
				prefix += " "
			}
			// Untagged FETCH with a literal: {n}CRLF<raw>CRLF)
			_, _ = w.WriteString(fmt.Sprintf("* %d FETCH (%s%s {%d}\r\n", seq, prefix, label, len(body)))
			_, _ = w.Write(body)
			_, _ = w.WriteString(")\r\n")
			_ = w.Flush()
		} else {
			line(fmt.Sprintf("* %d FETCH (%s)", seq, strings.Join(fields, " ")))
		}
	}
	line(tag + " OK FETCH completed")
}

func (s *IMAPServer) doStore(line func(string), sess *imapSession, tag, arg string) {
	if !sess.selected {
		line(tag + " NO No mailbox selected")
		return
	}
	seqPart, rest := cutSpace(arg)
	lo, hi := parseSeqSet(seqPart, len(sess.msgs))
	if strings.Contains(strings.ToUpper(rest), `\SEEN`) {
		for seq := lo; seq <= hi; seq++ {
			if seq < 1 || seq > len(sess.msgs) {
				continue
			}
			m := &sess.msgs[seq-1]
			if !m.Seen {
				if newID, err := s.maildir.markSeen(s.cfg.Domain, sess.authedUser, m.ID); err == nil {
					m.ID = newID
					m.Seen = true
				}
			}
			line(fmt.Sprintf(`* %d FETCH (FLAGS (\Seen))`, seq))
		}
	}
	line(tag + " OK STORE completed")
}

func cutSpace(s string) (head, rest string) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func parseLoginArgs(arg string) (user, pass string) {
	parts := tokenizeQuoted(arg)
	switch {
	case len(parts) >= 2:
		return parts[0], parts[1]
	case len(parts) == 1:
		return parts[0], ""
	default:
		return "", ""
	}
}

func tokenizeQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	inQ := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQ = !inQ
		case r == ' ' && !inQ:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

func parseSeqSet(s string, count int) (lo, hi int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 1, count
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		lo = atoiDefault(s[:i], 1)
		if h := s[i+1:]; h == "*" {
			hi = count
		} else {
			hi = atoiDefault(h, count)
		}
		return lo, hi
	}
	n := atoiDefault(s, 0)
	return n, n
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}
