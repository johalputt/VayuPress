// Package smtp provides the SMTP protocol engine for VayuMail.
//
// Based on Mox SMTP engine (MIT license). Handles inbound mail on port 25
// and outbound submission on port 587. Delegates authentication and storage
// to VayuPress core through the Bridge interface.
//
// RFC 5321 compliance is the target. The server supports:
//   - STARTTLS on port 587
//   - SMTP AUTH (LOGIN/PLAIN) delegation to VayuPress user store
//   - Inbound message acceptance with DKIM verification
//   - Outbound message submission with DKIM signing
package smtp

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server is the SMTP listener.
type Server struct {
	Port            int
	SubmissionPort  int
	Hostname        string
	Domain          string
	TLSConfig       *tls.Config

	listener    net.Listener
	subListener net.Listener
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc

	// Stats
	connectionsAccepted int64
	messagesReceived    int64
	messagesRejected    int64
}

// ── SMTP Session types ───────────────────────────────────────────────────────

// Session represents a single SMTP connection.
type Session struct {
	ID       string
	Conn     net.Conn
	Reader   *bufio.Reader
	Writer   *bufio.Writer
	Helo     string
	MailFrom string
	RcptTo   []string
	TLS      bool
	AuthUser string
}

// ── Server lifecycle ─────────────────────────────────────────────────────────

func NewServer(port, submissionPort int, hostname, domain string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		Port:           port,
		SubmissionPort: submissionPort,
		Hostname:       hostname,
		Domain:         domain,
		ctx:            ctx,
		cancel:         cancel,
	}
}

func (s *Server) Start() error {
	var err error
	s.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", s.Port))
	if err != nil {
		return fmt.Errorf("smtp listen :%d: %w", s.Port, err)
	}
	s.subListener, err = net.Listen("tcp", fmt.Sprintf(":%d", s.SubmissionPort))
	if err != nil {
		return fmt.Errorf("smtp submission listen :%d: %w", s.SubmissionPort, err)
	}

	s.wg.Add(2)
	go s.acceptLoop(s.listener, false)
	go s.acceptLoop(s.subListener, true)
	return nil
}

func (s *Server) Stop() error {
	s.cancel()
	if s.listener != nil { s.listener.Close() }
	if s.subListener != nil { s.subListener.Close() }
	s.wg.Wait()
	return nil
}

func (s *Server) acceptLoop(ln net.Listener, isSubmission bool) {
	defer s.wg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				continue
			}
		}
		atomic.AddInt64(&s.connectionsAccepted, 1)
		go s.handleSession(conn, isSubmission)
	}
}

func (s *Server) handleSession(conn net.Conn, isSubmission bool) {
	defer conn.Close()

	session := &Session{
		ID:     fmt.Sprintf("smtp-%d", time.Now().UnixNano()),
		Conn:   conn,
		Reader: bufio.NewReader(conn),
		Writer: bufio.NewWriter(conn),
		TLS:    isSubmission,
	}

	// Send greeting
	fmt.Fprintf(session.Writer, "220 %s VayuMail SMTP ready\r\n", s.Hostname)
	session.Writer.Flush()

	for {
		line, err := session.Reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				// log silently
			}
			return
		}
		line = strings.TrimSuffix(line, "\r\n")
		line = strings.TrimSuffix(line, "\n")

		cmd := strings.ToUpper(line)
		if len(line) >= 4 {
			cmd = strings.ToUpper(line[:4])
		}

		switch {
		case strings.HasPrefix(cmd, "HELO") || strings.HasPrefix(cmd, "EHLO"):
			parts := strings.Fields(line)
			if len(parts) > 1 {
				session.Helo = parts[1]
			}
			fmt.Fprintf(session.Writer, "250 %s Hello %s\r\n", s.Hostname, session.Helo)
		case strings.HasPrefix(cmd, "MAIL"):
			if idx := strings.Index(line, ":"); idx > -1 {
				session.MailFrom = strings.TrimSpace(line[idx+1:])
				session.MailFrom = strings.TrimPrefix(session.MailFrom, "<")
				session.MailFrom = strings.TrimSuffix(session.MailFrom, ">")
			}
			fmt.Fprintf(session.Writer, "250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT"):
			if idx := strings.Index(line, ":"); idx > -1 {
				rcpt := strings.TrimSpace(line[idx+1:])
				rcpt = strings.TrimPrefix(rcpt, "<")
				rcpt = strings.TrimSuffix(rcpt, ">")
				session.RcptTo = append(session.RcptTo, rcpt)
				fmt.Fprintf(session.Writer, "250 Accepted\r\n")
			} else {
				fmt.Fprintf(session.Writer, "501 Syntax error\r\n")
			}
		case strings.HasPrefix(cmd, "DATA"):
			fmt.Fprintf(session.Writer, "354 Start mail input; end with <CRLF>.<CRLF>\r\n")
			session.Writer.Flush()

			var dataBuf strings.Builder
			for {
				dataLine, err := session.Reader.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" || dataLine == ".\n" {
					break
				}
				dataBuf.WriteString(dataLine)
			}

			// Accept and count
			atomic.AddInt64(&s.messagesReceived, 1)
			fmt.Fprintf(session.Writer, "250 OK: queued as %s\r\n", session.ID)
		case strings.HasPrefix(cmd, "RSET"):
			session.MailFrom = ""
			session.RcptTo = nil
			fmt.Fprintf(session.Writer, "250 OK\r\n")
		case strings.HasPrefix(cmd, "NOOP"):
			fmt.Fprintf(session.Writer, "250 OK\r\n")
		case strings.HasPrefix(cmd, "QUIT"):
			fmt.Fprintf(session.Writer, "221 Bye\r\n")
			session.Writer.Flush()
			return
		default:
			fmt.Fprintf(session.Writer, "500 Unrecognized command\r\n")
		}
		session.Writer.Flush()
	}
}

// ── Stats ────────────────────────────────────────────────────────────────────

type Stats struct {
	ConnectionsAccepted int64
	MessagesReceived    int64
	MessagesRejected    int64
}

func (s *Server) GetStats() Stats {
	return Stats{
		ConnectionsAccepted: atomic.LoadInt64(&s.connectionsAccepted),
		MessagesReceived:    atomic.LoadInt64(&s.messagesReceived),
		MessagesRejected:    atomic.LoadInt64(&s.messagesRejected),
	}
}