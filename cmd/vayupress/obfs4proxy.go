// SPDX-License-Identifier: Apache-2.0

package main

// obfs4proxy.go — VayuPress runs the obfs4 pluggable transport ITSELF, so
// VayuTor can use obfs4 bridges on a Tor-hostile network with NO `obfs4proxy`
// package installed on the server (nothing to apt-install, nothing to do outside
// VayuOS). The managed tor is told `ClientTransportPlugin obfs4 exec <vayupress>
// __run-obfs4` (see managedTor.resolvePT); tor execs this same binary with the
// pluggable-transport env vars, and the code below speaks the PT protocol: a
// SOCKS5 listener that dials out through obfs4. It mirrors lyrebird's client
// setup, obfs4-only, and adds no inbound surface (client transport only).

import (
	"io"
	"net"
	"os"
	"sync"

	pt "gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/goptlib"
	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/lyrebird/common/socks5"
	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/lyrebird/transports/base"
	"gitlab.torproject.org/tpo/anti-censorship/pluggable-transports/lyrebird/transports/obfs4"
)

// obfs4Subcommand is the argv[1] sentinel tor uses to exec us as the transport.
const obfs4Subcommand = "__run-obfs4"

// runEmbeddedObfs4 is the entry point when VayuPress is exec'd as tor's obfs4
// client transport. It never returns until tor shuts us down (our stdin — a pipe
// from tor — reaches EOF when tor exits, even on kill).
func runEmbeddedObfs4() {
	stateDir := os.Getenv("TOR_PT_STATE_LOCATION")
	if stateDir == "" {
		stateDir = "."
	}
	ci, err := pt.ClientSetup([]string{"obfs4"})
	if err != nil {
		os.Exit(1)
	}
	// We never configure an upstream proxy for the managed tor, so decline one
	// rather than silently ignoring it (keeps the PT handshake correct).
	if os.Getenv("TOR_PT_PROXY") != "" {
		_ = pt.ProxyError("upstream proxy is not supported by the embedded obfs4 transport")
		os.Exit(1)
	}

	t := &obfs4.Transport{}
	var listeners []net.Listener
	for _, name := range ci.MethodNames {
		if name != "obfs4" {
			_ = pt.CmethodError(name, "only obfs4 is supported")
			continue
		}
		f, ferr := t.ClientFactory(stateDir)
		if ferr != nil {
			_ = pt.CmethodError(name, "failed to get client factory")
			continue
		}
		ln, lerr := net.Listen("tcp", "127.0.0.1:0")
		if lerr != nil {
			_ = pt.CmethodError(name, lerr.Error())
			continue
		}
		go obfs4AcceptLoop(f, ln)
		pt.Cmethod(name, socks5.Version(), ln.Addr())
		listeners = append(listeners, ln)
	}
	pt.CmethodsDone()

	// Block until tor closes our stdin (it does on shutdown; the pipe also EOFs if
	// tor dies), then tear the listeners down.
	_, _ = io.Copy(io.Discard, os.Stdin)
	for _, ln := range listeners {
		_ = ln.Close()
	}
}

// obfs4AcceptLoop accepts SOCKS5 connections from tor and hands each to obfs4.
// It exits when the listener is closed (VayuTor shutting the transport down) or
// on any unrecoverable accept error — the process is short-lived and tor-managed.
func obfs4AcceptLoop(f base.ClientFactory, ln net.Listener) {
	defer func() { _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go obfs4Handler(f, conn)
	}
}

// obfs4Handler completes the SOCKS handshake, dials the bridge through obfs4, and
// pipes bytes both ways.
func obfs4Handler(f base.ClientFactory, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	req, err := socks5.Handshake(conn)
	if err != nil {
		return
	}
	args, err := f.ParseArgs(&req.Args)
	if err != nil {
		_ = req.Reply(socks5.ReplyGeneralFailure)
		return
	}
	// base.DialFunc has the exact signature of net.Dial.
	remote, err := f.Dial("tcp", req.Target, net.Dial, args)
	if err != nil {
		_ = req.Reply(socks5.ErrorToReplyCode(err))
		return
	}
	defer func() { _ = remote.Close() }()
	if err := req.Reply(socks5.ReplySucceeded); err != nil {
		return
	}
	obfs4Copy(conn, remote)
}

// obfs4Copy pipes bytes in both directions until either side closes.
func obfs4Copy(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if c, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = c.CloseWrite()
		}
	}
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}
