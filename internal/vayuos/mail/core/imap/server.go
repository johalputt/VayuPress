// Package imap provides the IMAP protocol engine for VayuMail.
//
// Based on Mox IMAP engine (MIT license). Serves mailboxes over TLS on
// port 993. Delegates authentication to VayuPress user store through
// the Bridge interface. Messages are stored in Maildir format.
//
// RFC 3501 compliance is the target.
package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
)

// Server is the IMAP listener.
type Server struct {
	Port      int
	Hostname  string
	TLSConfig *tls.Config

	listener net.Listener
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	connectionsAccepted int64
	activeConnections   int64
}

func NewServer(port int, hostname string) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		Port:     port,
		Hostname: hostname,
		ctx:      ctx,
		cancel:   cancel,
	}
}

func (s *Server) Start() error {
	// IMAP requires TLS
	cert, err := tls.LoadX509KeyPair(
		fmt.Sprintf("/var/lib/vayupress/certs/%s/fullchain.pem", s.Hostname),
		fmt.Sprintf("/var/lib/vayupress/certs/%s/privkey.pem", s.Hostname),
	)
	if err != nil {
		return fmt.Errorf("load TLS cert for %s: %w", s.Hostname, err)
	}
	s.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	s.listener, err = tls.Listen("tcp", fmt.Sprintf(":%d", s.Port), s.TLSConfig)
	if err != nil {
		return fmt.Errorf("imap listen :%d: %w", s.Port, err)
	}
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() error {
	s.cancel()
	if s.listener != nil { s.listener.Close() }
	s.wg.Wait()
	return nil
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.ctx.Done():
				return
			default:
				continue
			}
		}
		atomic.AddInt64(&s.connectionsAccepted, 1)
		atomic.AddInt64(&s.activeConnections, 1)
		go func(c net.Conn) {
			defer c.Close()
			defer atomic.AddInt64(&s.activeConnections, -1)
			s.handleSession(c)
		}(conn)
	}
}

func (s *Server) handleSession(conn net.Conn) {
	// IMAP session handling stub
	// Full implementation covers: LOGIN, SELECT, FETCH, STORE, SEARCH, etc.
	_ = conn
}

type Stats struct {
	ConnectionsAccepted int64
	ActiveConnections   int64
}

func (s *Server) GetStats() Stats {
	return Stats{
		ConnectionsAccepted: atomic.LoadInt64(&s.connectionsAccepted),
		ActiveConnections:   atomic.LoadInt64(&s.activeConnections),
	}
}