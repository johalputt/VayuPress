// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// pop3d.go — a minimal RFC 1939 POP3 server (with STLS, RFC 2595) giving simple
// clients a download-and-keep/delete view of a VayuMail account's inbox. POP3 is
// single-folder by design, so it serves INBOX only; IMAP is the richer,
// multi-folder protocol. Authentication is delegated to VayuPress accounts via
// the Bridge, and the PGP decrypt hook is applied on RETR so a client downloads
// readable mail. Listeners are best-effort, mirroring the SMTP/IMAP servers.

// POP3Server is a POP3 listener (plaintext+STLS on 110, or implicit TLS on 995).
type POP3Server struct {
	// limiter bounds concurrent connections globally and per source address.
	// Without it the AuthThrottle delay is defeated by simply opening more
	// sockets — see connlimit.go.
	limiter *connLimiter
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

// NewPOP3Server creates a POP3 server bound to cfg.POP3Listen.
func NewPOP3Server(cfg Config, bridge Bridge, md *Maildir, decrypt DecryptHook) *POP3Server {
	return &POP3Server{cfg: cfg, bridge: bridge, maildir: md, decrypt: decrypt, listenAddr: cfg.POP3Listen, limiter: newConnLimiter(0, 0)}
}

// WithTLS enables the STLS command on the plaintext (110) listener.
func (s *POP3Server) WithTLS(t *tls.Config) *POP3Server { s.tls = t; return s }

// WithImplicitTLS turns this into an implicit-TLS POP3S listener bound to addr
// (995): connections are wrapped in TLS immediately, with no STLS step.
func (s *POP3Server) WithImplicitTLS(t *tls.Config, addr string) *POP3Server {
	s.tls = t
	s.implicitTLS = true
	s.listenAddr = addr
	return s
}

// Addr returns the actual listen address (useful with :0 in tests).
func (s *POP3Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Start begins listening.
func (s *POP3Server) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("vayumail: pop3 listen %s: %w", s.listenAddr, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop shuts the listener down.
func (s *POP3Server) Stop(_ context.Context) error {
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

func (s *POP3Server) acceptLoop() {
	defer s.wg.Done()
	var bo acceptBackoff
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				return
			}
			// Back off instead of spinning: Accept fails persistently on
			// descriptor exhaustion, and a bare `continue` there burns a core at
			// 100% until it clears (see acceptBackoff).
			bo.pause()
			continue
		}
		bo.reset()
		release, ok := s.limiter.acquire(conn.RemoteAddr())
		if !ok {
			// Over the global or per-source cap. Refuse before doing any work:
			// servicing it would let an attacker turn concurrency into
			// brute-force throughput against the auth delay.
			_, _ = conn.Write([]byte("-ERR too many connections, try again later\r\n"))
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer release()
			c := conn
			if s.implicitTLS && s.tls != nil {
				c = tls.Server(conn, s.tls)
			}
			s.handle(c)
		}()
	}
}

// pop3Msg is one inbox message in a POP3 session snapshot.
type pop3Msg struct {
	id      string // folder-relative id ("new/x" or "cur/x")
	uidl    string // stable unique-id (the immutable Maildir base name)
	size    int64
	deleted bool
}

func (s *POP3Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	br := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	ok := func(msg string) { _, _ = w.WriteString("+OK " + msg + "\r\n"); _ = w.Flush() }
	errResp := func(msg string) { _, _ = w.WriteString("-ERR " + msg + "\r\n"); _ = w.Flush() }

	onTLS := s.implicitTLS
	ok("VayuMail POP3 ready")

	var (
		user        string
		authed      bool
		authTries   int
		localUser   string
		localDomain = s.cfg.Domain // Maildir key; a secondary login overrides it (Stage 3b)
		msgs        []pop3Msg
	)

	for {
		raw, err := readCommandLine(br)
		if err != nil {
			return
		}
		cmd, arg := cutSpace(strings.TrimRight(raw, "\r\n"))
		switch strings.ToUpper(cmd) {
		case "CAPA":
			ok("Capability list follows")
			_, _ = w.WriteString("USER\r\nUIDL\r\nTOP\r\n")
			if s.tls != nil && !onTLS {
				_, _ = w.WriteString("STLS\r\n")
			}
			_, _ = w.WriteString(".\r\n")
			_ = w.Flush()
		case "STLS":
			if s.tls == nil || onTLS {
				errResp("STLS not available")
				continue
			}
			ok("Begin TLS negotiation")
			tconn := tls.Server(conn, s.tls)
			if herr := tconn.Handshake(); herr != nil {
				return
			}
			conn = tconn
			_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
			br = bufio.NewReader(conn)
			w = bufio.NewWriter(conn)
			onTLS = true
			user, authed = "", false
		case "USER":
			user = strings.TrimSpace(arg)
			ok("send PASS")
		case "PASS":
			if user == "" {
				errResp("USER first")
				continue
			}
			if authTries >= maxAuthTriesPerConn {
				// Drop rather than keep answering. The throttle delays each attempt
				// but never ends the session, so without this one socket buys
				// unlimited guesses.
				errResp("too many authentication failures")
				return
			}
			authTries++
			// Source-keyed throttle: per-mailbox counters cannot see one password
			// sprayed across many accounts.
			recordSource := throttleSource(conn.RemoteAddr())
			if s.bridge == nil {
				recordSource(false)
				errResp("authentication unavailable")
				continue
			}
			okAuth, aerr := s.bridge.AuthUser(user, strings.TrimSpace(arg))
			if aerr != nil || !okAuth {
				recordSource(false)
				errResp("[AUTH] invalid credentials")
				continue
			}
			recordSource(true)
			authed = true
			localUser = user
			if i := strings.IndexByte(user, '@'); i >= 0 {
				localUser = user[:i]
			}
			localDomain = mailboxDomainFor(user, s.cfg.Domain)
			msgs = s.loadInbox(localDomain, localUser)
			ok(fmt.Sprintf("mailbox ready, %d messages", len(msgs)))
		case "STAT":
			if !authed {
				errResp("not authenticated")
				continue
			}
			n, total := pop3Totals(msgs)
			ok(fmt.Sprintf("%d %d", n, total))
		case "LIST":
			if !authed {
				errResp("not authenticated")
				continue
			}
			s.cmdList(w, ok, errResp, msgs, arg, false)
		case "UIDL":
			if !authed {
				errResp("not authenticated")
				continue
			}
			s.cmdList(w, ok, errResp, msgs, arg, true)
		case "RETR":
			if !authed {
				errResp("not authenticated")
				continue
			}
			s.cmdRetr(w, ok, errResp, localDomain, localUser, msgs, arg, -1)
		case "TOP":
			if !authed {
				errResp("not authenticated")
				continue
			}
			idxArg, linesArg := cutSpace(arg)
			nLines, perr := strconv.Atoi(strings.TrimSpace(linesArg))
			if perr != nil || nLines < 0 {
				errResp("bad TOP arguments")
				continue
			}
			s.cmdRetr(w, ok, errResp, localDomain, localUser, msgs, idxArg, nLines)
		case "DELE":
			if !authed {
				errResp("not authenticated")
				continue
			}
			idx, ferr := pop3Index(arg, msgs)
			if ferr != nil {
				errResp(ferr.Error())
				continue
			}
			msgs[idx].deleted = true
			ok(fmt.Sprintf("message %d deleted", idx+1))
		case "RSET":
			for i := range msgs {
				msgs[i].deleted = false
			}
			n, total := pop3Totals(msgs)
			ok(fmt.Sprintf("%d %d", n, total))
		case "NOOP":
			ok("")
		case "QUIT":
			// UPDATE state: physically remove messages marked for deletion.
			for _, m := range msgs {
				if m.deleted {
					_ = s.maildir.deleteMessage(localDomain, localUser, "Inbox", m.id)
				}
			}
			ok("VayuMail POP3 signing off")
			return
		default:
			errResp("command not recognized")
		}
	}
}

// loadInbox snapshots the account's INBOX for the session (oldest first, the
// conventional POP3 order). The UIDL is the immutable Maildir base name, which
// is unique and stable across sessions.
func (s *POP3Server) loadInbox(localDomain, localUser string) []pop3Msg {
	list, err := s.maildir.List(localDomain, localUser)
	if err != nil {
		return nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Date.Before(list[j].Date) })
	out := make([]pop3Msg, 0, len(list))
	for _, sm := range list {
		out = append(out, pop3Msg{id: sm.ID, uidl: idBaseName(sm.ID), size: sm.Size})
	}
	return out
}

func pop3Totals(msgs []pop3Msg) (count int, octets int64) {
	for _, m := range msgs {
		if m.deleted {
			continue
		}
		count++
		octets += m.size
	}
	return count, octets
}

// pop3Index resolves a 1-based message number argument to a slice index,
// rejecting out-of-range or deleted messages.
func pop3Index(arg string, msgs []pop3Msg) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || n < 1 || n > len(msgs) {
		return 0, fmt.Errorf("no such message")
	}
	if msgs[n-1].deleted {
		return 0, fmt.Errorf("message %d already deleted", n)
	}
	return n - 1, nil
}

// cmdList serves LIST and UIDL, in both scan (all messages) and single-message
// forms. When uidl is true it prints the unique-id, otherwise the octet size.
func (s *POP3Server) cmdList(w *bufio.Writer, ok func(string), errResp func(string), msgs []pop3Msg, arg string, uidl bool) {
	field := func(m pop3Msg) string {
		if uidl {
			return m.uidl
		}
		return strconv.FormatInt(m.size, 10)
	}
	if strings.TrimSpace(arg) != "" {
		idx, ferr := pop3Index(arg, msgs)
		if ferr != nil {
			errResp(ferr.Error())
			return
		}
		ok(fmt.Sprintf("%d %s", idx+1, field(msgs[idx])))
		return
	}
	ok("listing follows")
	for i, m := range msgs {
		if m.deleted {
			continue
		}
		_, _ = w.WriteString(fmt.Sprintf("%d %s\r\n", i+1, field(m)))
	}
	_, _ = w.WriteString(".\r\n")
	_ = w.Flush()
}

// cmdRetr serves RETR (maxLines < 0) and TOP (maxLines >= 0 body lines). The
// message is PGP-decrypted (best-effort) and dot-stuffed per RFC 1939.
func (s *POP3Server) cmdRetr(w *bufio.Writer, ok func(string), errResp func(string), localDomain, localUser string, msgs []pop3Msg, arg string, maxLines int) {
	idx, ferr := pop3Index(arg, msgs)
	if ferr != nil {
		errResp(ferr.Error())
		return
	}
	raw, err := s.maildir.ReadRawFolder(localDomain, localUser, "Inbox", msgs[idx].id)
	if err != nil {
		errResp("cannot read message")
		return
	}
	if s.decrypt != nil {
		raw = s.decrypt(localUser+"@"+localDomain, raw)
	}
	payload := raw
	if maxLines >= 0 {
		payload = topBytes(raw, maxLines)
	}
	if maxLines >= 0 {
		ok("top of message follows")
	} else {
		ok(fmt.Sprintf("%d octets", len(raw)))
	}
	writeDotStuffed(w, payload)
	_, _ = w.WriteString(".\r\n")
	_ = w.Flush()
}

// topBytes returns the message header plus the first nLines body lines (for TOP).
func topBytes(raw []byte, nLines int) []byte {
	hdr, body := splitHeaderBody(raw)
	if nLines <= 0 {
		return hdr
	}
	lines := strings.SplitAfter(string(body), "\n")
	if nLines < len(lines) {
		lines = lines[:nLines]
	}
	return append(append([]byte{}, hdr...), []byte(strings.Join(lines, ""))...)
}

// writeDotStuffed writes payload with RFC 1939 byte-stuffing: any line starting
// with '.' is prefixed with an extra '.', and line endings are normalised to
// CRLF so the terminating ".CRLF" is unambiguous.
func writeDotStuffed(w *bufio.Writer, payload []byte) {
	text := strings.ReplaceAll(string(payload), "\r\n", "\n")
	for _, ln := range strings.Split(text, "\n") {
		if strings.HasPrefix(ln, ".") {
			_, _ = w.WriteString(".")
		}
		_, _ = w.WriteString(ln)
		_, _ = w.WriteString("\r\n")
	}
}
