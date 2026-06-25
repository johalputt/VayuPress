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
	"strings"
	"sync"
	"time"
)

// InboundHandler receives a fully-assembled inbound message for local delivery.
// from is the envelope sender; rcpts are the (local) envelope recipients.
type InboundHandler func(from string, rcpts []string, raw []byte) error

// AuthFunc verifies submission credentials (delegated to VayuPress accounts).
type AuthFunc func(username, password string) (bool, error)

// SMTPServer is a minimal RFC 5321 server. In its default (receive) mode it
// accepts mail for the configured domain's local accounts and hands each
// message to a delivery handler. In submission mode (RFC 6409) it requires
// STARTTLS + AUTH and relays authenticated users' mail outbound. STARTTLS is
// offered whenever a TLS config is attached.
type SMTPServer struct {
	cfg        Config
	handler    InboundHandler
	listenAddr string
	tls        *tls.Config
	submission bool
	auth       AuthFunc

	ln     net.Listener
	wg     sync.WaitGroup
	mu     sync.Mutex
	closed bool
}

// NewSMTPServer creates a receive server bound to cfg.SMTPListen.
func NewSMTPServer(cfg Config, handler InboundHandler) *SMTPServer {
	return &SMTPServer{cfg: cfg, handler: handler, listenAddr: cfg.SMTPListen}
}

// WithTLS attaches a TLS config, enabling the STARTTLS command. Returns the
// server for chaining.
func (s *SMTPServer) WithTLS(t *tls.Config) *SMTPServer {
	s.tls = t
	return s
}

// NewSubmissionServer creates an authenticated mail-submission server (RFC 6409)
// bound to cfg.SubmissionListen. STARTTLS is required before AUTH, and only
// authenticated senders may relay; relay delivers each accepted message.
func NewSubmissionServer(cfg Config, t *tls.Config, auth AuthFunc, relay InboundHandler) *SMTPServer {
	return &SMTPServer{cfg: cfg, handler: relay, listenAddr: cfg.SubmissionListen, tls: t, submission: true, auth: auth}
}

// Addr returns the actual listen address (useful when binding to :0 in tests).
func (s *SMTPServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Start begins listening and serving connections until Stop is called.
func (s *SMTPServer) Start(_ context.Context) error {
	ln, err := net.Listen("tcp", s.listenAddr)
	if err != nil {
		return fmt.Errorf("vayumail: smtp listen %s: %w", s.listenAddr, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Stop shuts the listener down and waits for in-flight connections.
func (s *SMTPServer) Stop(_ context.Context) error {
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

func (s *SMTPServer) acceptLoop() {
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
			s.handle(conn)
		}()
	}
}

func (s *SMTPServer) hostname() string {
	if s.cfg.Hostname != "" {
		return s.cfg.Hostname
	}
	return "localhost"
}

// handle implements the SMTP conversation for one connection, supporting
// STARTTLS upgrade and (in submission mode) AUTH PLAIN/LOGIN.
func (s *SMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))

	var (
		br     *bufio.Reader
		bw     *bufio.Writer
		onTLS  bool
		authed bool
		helo   string
		from   string
		rcpts  []string
	)
	var clientIP net.IP
	if ta, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		clientIP = ta.IP
	}
	setup := func(c net.Conn) {
		br = bufio.NewReader(io.LimitReader(c, s.cfg.MaxMessageBytes+1<<16))
		bw = bufio.NewWriter(c)
	}
	setup(conn)
	write := func(str string) { _, _ = bw.WriteString(str + "\r\n"); _ = bw.Flush() }
	reset := func() { from, rcpts = "", nil }

	role := "ESMTP"
	if s.submission {
		role = "Submission"
	}
	write("220 " + s.hostname() + " VayuMail " + role + " ready")

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg := splitCmd(line)
		switch cmd {
		case "HELO":
			helo = arg
			write("250 " + s.hostname())
		case "EHLO":
			helo = arg
			_, _ = bw.WriteString("250-" + s.hostname() + "\r\n")
			_, _ = bw.WriteString("250-8BITMIME\r\n")
			_, _ = bw.WriteString(fmt.Sprintf("250-SIZE %d\r\n", s.cfg.MaxMessageBytes))
			if s.tls != nil && !onTLS {
				_, _ = bw.WriteString("250-STARTTLS\r\n")
			}
			if s.submission && onTLS {
				_, _ = bw.WriteString("250-AUTH PLAIN LOGIN\r\n")
			}
			_, _ = bw.WriteString("250 PIPELINING\r\n")
			_ = bw.Flush()
		case "STARTTLS":
			if s.tls == nil {
				write("454 4.7.0 TLS not available")
				continue
			}
			if onTLS {
				write("503 5.5.1 Already using TLS")
				continue
			}
			write("220 2.0.0 Ready to start TLS")
			tconn := tls.Server(conn, s.tls)
			if herr := tconn.Handshake(); herr != nil {
				return
			}
			conn = tconn
			_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
			setup(conn) // RFC 3207: discard prior state after the upgrade
			onTLS, authed = true, false
			reset()
		case "AUTH":
			if !s.submission {
				write("502 5.5.1 AUTH not supported")
				continue
			}
			if !onTLS {
				write("538 5.7.11 Encryption required for AUTH")
				continue
			}
			if authed {
				write("503 5.5.1 Already authenticated")
				continue
			}
			if s.runAuth(br, write, arg) {
				authed = true
				write("235 2.7.0 Authentication successful")
			} else {
				write("535 5.7.8 Authentication credentials invalid")
			}
		case "MAIL":
			if s.submission && !authed {
				write("530 5.7.0 Authentication required")
				continue
			}
			from = extractAddr(arg)
			rcpts = nil
			write("250 2.1.0 Ok")
		case "RCPT":
			addr := extractAddr(arg)
			if addr == "" {
				write("501 5.1.3 Bad recipient")
				continue
			}
			if s.submission {
				if !authed {
					write("530 5.7.0 Authentication required")
					continue
				}
				// Authenticated submitters may relay to any recipient.
			} else if !s.recipientAccepted(addr) {
				write("550 5.7.1 Relay denied — recipient not local")
				continue
			}
			rcpts = append(rcpts, addr)
			write("250 2.1.5 Ok")
		case "DATA":
			if len(rcpts) == 0 {
				write("503 5.5.1 RCPT first")
				continue
			}
			write("354 End data with <CR><LF>.<CR><LF>")
			raw, derr := readData(br, s.cfg.MaxMessageBytes)
			if derr != nil {
				write("552 5.3.4 Message too big or read error")
				return
			}
			if s.handler != nil {
				msg := raw
				// Inbound (not submission): authenticate the sender and stamp an
				// Authentication-Results header. A DMARC failure under an
				// enforcing policy is flagged for the junk filter.
				if !s.submission {
					v := verifyInbound(s.hostname(), clientIP, helo, from, raw)
					pre := v.authResultsHeader()
					if v.Quarantine {
						pre += "X-VayuMail-Auth-Quarantine: yes\r\n"
					}
					msg = append([]byte(pre), raw...)
				}
				if herr := s.handler(from, rcpts, msg); herr != nil {
					write("451 4.3.0 Message handling failed")
					continue
				}
			}
			write("250 2.0.0 Ok: queued")
			reset()
		case "RSET":
			reset()
			write("250 2.0.0 Ok")
		case "NOOP":
			write("250 2.0.0 Ok")
		case "VRFY":
			write("252 2.5.2 Cannot VRFY user")
		case "QUIT":
			write("221 2.0.0 Bye")
			return
		default:
			write("502 5.5.2 Command not recognized")
		}
	}
}

// runAuth handles AUTH PLAIN / LOGIN, reading any continuation lines. It returns
// true only when the bridge verifies the credentials.
func (s *SMTPServer) runAuth(br *bufio.Reader, write func(string), arg string) bool {
	if s.auth == nil {
		return false
	}
	parts := strings.SplitN(strings.TrimSpace(arg), " ", 2)
	mech := strings.ToUpper(strings.TrimSpace(parts[0]))
	readB64 := func() string {
		l, err := br.ReadString('\n')
		if err != nil {
			return ""
		}
		b, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(l))
		if derr != nil {
			return ""
		}
		return string(b)
	}
	switch mech {
	case "PLAIN":
		var raw string
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1])); err == nil {
				raw = string(b)
			}
		} else {
			write("334 ")
			raw = readB64()
		}
		f := strings.Split(raw, "\x00") // authzid \0 authcid \0 passwd
		if len(f) != 3 {
			return false
		}
		return s.verify(f[1], f[2])
	case "LOGIN":
		write("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
		user := readB64()
		write("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
		pass := readB64()
		return s.verify(user, pass)
	default:
		return false
	}
}

func (s *SMTPServer) verify(user, pass string) bool {
	if user == "" || s.auth == nil {
		return false
	}
	ok, err := s.auth(user, pass)
	return err == nil && ok
}

// recipientAccepted enforces no open relay: only local domain recipients.
func (s *SMTPServer) recipientAccepted(addr string) bool {
	_, domain := splitAddress(addr)
	return strings.EqualFold(domain, s.cfg.Domain)
}

func splitCmd(line string) (cmd, arg string) {
	line = strings.TrimSpace(line)
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return strings.ToUpper(line[:i]), strings.TrimSpace(line[i+1:])
	}
	return strings.ToUpper(line), ""
}

// extractAddr pulls the address from "FROM:<a@b>" / "TO:<a@b>" / bare forms.
func extractAddr(arg string) string {
	if i := strings.IndexByte(arg, ':'); i >= 0 {
		arg = arg[i+1:]
	}
	arg = strings.TrimSpace(arg)
	if i := strings.IndexByte(arg, '<'); i >= 0 {
		if j := strings.IndexByte(arg, '>'); j > i {
			return strings.TrimSpace(arg[i+1 : j])
		}
	}
	// Strip any ESMTP params after the address.
	if i := strings.IndexByte(arg, ' '); i >= 0 {
		arg = arg[:i]
	}
	return strings.TrimSpace(arg)
}

// readData reads the DATA payload until the terminating "." line, undoing
// dot-stuffing, with a hard size cap.
func readData(br *bufio.Reader, max int64) ([]byte, error) {
	var sb strings.Builder
	var total int64
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			break
		}
		// Undo dot-stuffing.
		if strings.HasPrefix(trimmed, "..") {
			trimmed = trimmed[1:]
		}
		total += int64(len(trimmed)) + 2
		if total > max {
			return nil, errors.New("message too large")
		}
		sb.WriteString(trimmed)
		sb.WriteString("\r\n")
	}
	return []byte(sb.String()), nil
}
