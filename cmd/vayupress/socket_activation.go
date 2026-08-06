// SPDX-License-Identifier: Apache-2.0

package main

// socket_activation.go — ADR-0155 P5. Make a restart stop being an outage.
//
// # The problem this solves, and the one it does not
//
// P1 and P2 removed the restarts that were never needed. One remains and always
// will: an in-app update replaces the binary, and that requires a new process.
// While the app is down, nginx — which has no queue in front of :8080 — answers
// every request with 502.
//
// Socket activation moves the listening socket up one level. systemd binds and
// holds it; the service inherits it. Across a restart the socket STAYS OPEN, so
// connections arriving in the gap sit in the kernel's accept backlog instead of
// being refused. nginx sees a slow response rather than a connection error, and
// the visitor sees latency rather than an error page. The outage becomes a pause.
//
// It does not make startup faster, and it does not hide a service that fails to
// come back — a queue that is never drained still ends in a timeout. What it
// removes is the 502 for a restart that DOES complete, which is every restart
// this product performs on purpose.
//
// # Why this is safe to ship to installs that have never heard of it
//
// The binary only ever ACCEPTS an inherited socket. It never asks for one, never
// requires one, and binds exactly as it always has when none is passed. An
// install without the socket unit sees no behavioural change whatsoever, which
// is the only responsible way to alter how a service acquires its listener.

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/update"
)

// listenFDsStart is the first file descriptor systemd passes. Fixed by the
// protocol: 0/1/2 are stdio, so an inherited socket begins at 3.
const listenFDsStart = 3

// socketActivated records whether this process is serving on an inherited
// socket. It is read by the update path, which must NOT re-exec itself when true
// — see shouldExitForSupervisor.
var socketActivated bool

// inheritedListener returns the socket systemd passed, if it passed one.
//
// Every branch that returns false leaves the caller binding normally, which is
// the behaviour every existing install already has.
//
// The environment is cleared on the way out whatever the outcome. That is not
// tidiness: LISTEN_FDS and LISTEN_PID are inherited by children, and this binary
// spawns one — the Tor Space supervisor. A child that believed it had been
// handed a socket would try to serve on a descriptor belonging to its parent.
func inheritedListener() (net.Listener, bool) {
	pidRaw, fdsRaw := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")
	defer func() {
		_ = os.Unsetenv("LISTEN_PID")
		_ = os.Unsetenv("LISTEN_FDS")
		_ = os.Unsetenv("LISTEN_FDNAMES")
	}()

	if pidRaw == "" || fdsRaw == "" {
		return nil, false
	}

	// THE check that makes reading fd 3 safe. LISTEN_PID names the process the
	// descriptors were passed to; if it is not us, the variables are stale —
	// inherited from a parent, or left in a shell — and fd 3 is somebody else's,
	// which in this binary could be the SQLite database. net.FileListener would
	// reject a non-socket anyway, but a guard that relies on the next layer
	// failing is not a guard.
	pid, err := strconv.Atoi(strings.TrimSpace(pidRaw))
	if err != nil || pid != os.Getpid() {
		return nil, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(fdsRaw))
	if err != nil || n < 1 {
		return nil, false
	}
	if n > 1 {
		// Only the first is used, and the rest are named rather than silently
		// ignored: a unit with several ListenStream= directives is a
		// configuration whose author expected all of them to be served.
		logging.LogInfo("main", fmt.Sprintf(
			"socket activation passed %d descriptors; this service serves the first and "+
				"nothing accepts on the others", n))
	}

	f := os.NewFile(uintptr(listenFDsStart), "systemd-listen-fd")
	if f == nil {
		return nil, false
	}
	// FileListener dups the descriptor, so the original is closed here and the
	// listener owns its own copy.
	ln, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		logging.LogError("main", "socket activation: inherited descriptor is not a listening socket",
			err.Error())
		return nil, false
	}
	return ln, true
}

// isLoopbackAddr reports whether a listener address is loopback-only.
func isLoopbackAddr(addr net.Addr) bool {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// serveListener returns the listener to serve on, and a description for the log.
//
// onionMode is passed rather than read, because it changes the answer and this
// is the one place where getting it wrong would be a privacy failure rather than
// an availability one.
func serveListener(addr string, onionMode bool) (net.Listener, string, error) {
	ln, ok := inheritedListener()
	if !ok {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, "", err
		}
		return l, "bound " + addr, nil
	}

	// IN TOR MODE THE BIND ADDRESS IS A GUARANTEE, NOT A SETTING.
	//
	// onionSafeBindAddr exists so a Tor-mode install listens on loopback only and
	// is reachable exclusively through the onion service. An inherited socket was
	// bound by systemd from a unit file this process did not write, so accepting
	// it blindly would let a `ListenStream=0.0.0.0:8080` in that unit publish a
	// Tor-mode install to the open internet — silently, and looking like working
	// code. That is the exact class of defect ADR-0141's anti-leak work exists to
	// prevent, arriving through a performance feature.
	//
	// So in Tor mode a non-loopback inherited socket is REFUSED. Nothing accepts
	// on it, and this process binds its own loopback listener as it always did.
	if onionMode && !isLoopbackAddr(ln.Addr()) {
		_ = ln.Close()
		logging.LogError("main", "socket activation REFUSED in Tor mode",
			"the inherited socket is bound to "+ln.Addr().String()+", which is not loopback. "+
				"A Tor-mode install must be reachable only through its onion service, so this "+
				"socket is not served. Binding loopback directly instead — fix the .socket unit "+
				"to ListenStream=127.0.0.1:<port> to use socket activation in this mode.")
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return nil, "", err
		}
		return l, "bound " + addr + " (inherited socket refused: not loopback in Tor mode)", nil
	}

	socketActivated = true
	return ln, "inherited socket " + ln.Addr().String() +
		" (systemd holds it across restarts, so a restart queues rather than refuses)", nil
}

// shouldExitForSupervisor reports whether a restart must be performed by exiting
// rather than by re-exec.
//
// This pairs with socket activation and is not optional alongside it. The
// updater's normal path is execve on the replaced binary, and the environment
// this file deliberately cleared does not survive into the new image — so the
// re-execed process would find no inherited socket, try to bind :8080 itself,
// and fail with "address in use" because systemd is still holding it. The
// service would then crash-loop into a restart that finally works, turning a
// smooth update into a worse outage than the one socket activation removed.
//
// Exiting 0 instead lets systemd do what it is already holding the socket for.
// The existing restart path already treats a clean exit as a supported outcome:
// its own comment says the process exits 0 on a failed re-exec so a supervisor
// can restart it. This makes that the FIRST choice when a supervisor is
// demonstrably present rather than the fallback.
func shouldExitForSupervisor() bool { return socketActivated }

// init wires the update package's restart path to this file's answer. Done here
// rather than at a call site so it cannot be forgotten on one of the three
// places that schedule a restart.
func init() { update.ExitForSupervisor = shouldExitForSupervisor }
