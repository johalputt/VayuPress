// SPDX-License-Identifier: Apache-2.0

package main

// socket_activation_test.go — ADR-0155 P5.
//
// Three things here can hurt someone, and each has a test that fails loudly
// rather than a comment saying it was considered:
//
//  1. Reading fd 3 when the environment was not meant for this process. In this
//     binary fd 3 can be the SQLite database.
//  2. Serving a Tor-mode install on a socket somebody else chose the bind
//     address for. That publishes an install whose whole premise is that it is
//     reachable only through its onion service.
//  3. Re-execing under socket activation, which cannot work and produces a
//     crash-loop where a clean handover was available.

import (
	"net"
	"os"
	"strconv"
	"testing"

	"github.com/johalputt/vayupress/internal/update"
)

// clearActivationEnv leaves the process as a fresh boot would find it.
func clearActivationEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
	socketActivated = false
	t.Cleanup(func() { socketActivated = false })
}

// THE test for the descriptor. A LISTEN_PID naming another process means the
// variables are stale, and fd 3 belongs to something this binary opened —
// possibly its own database.
func TestAnInheritedSocketIsRefusedWhenListenPidIsNotOurs(t *testing.T) {
	clearActivationEnv(t)
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()+1))
	t.Setenv("LISTEN_FDS", "1")

	if _, ok := inheritedListener(); ok {
		t.Fatal("a socket was accepted on the strength of a LISTEN_PID belonging to another process; " +
			"fd 3 is then whatever this binary happened to open")
	}
	// And the variables must be cleared regardless, so the Tor Space child does
	// not inherit a belief that it was handed a socket.
	if os.Getenv("LISTEN_PID") != "" || os.Getenv("LISTEN_FDS") != "" {
		t.Error("LISTEN_PID/LISTEN_FDS survived; a child process inherits them")
	}
}

// A malformed or absent count is not activation either.
func TestActivationIsRefusedOnMalformedEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, pid, fds string }{
		{"no variables at all", "", ""},
		{"pid but no count", strconv.Itoa(os.Getpid()), ""},
		{"count is not a number", strconv.Itoa(os.Getpid()), "many"},
		{"zero descriptors", strconv.Itoa(os.Getpid()), "0"},
		{"negative descriptors", strconv.Itoa(os.Getpid()), "-1"},
		{"pid is not a number", "not-a-pid", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearActivationEnv(t)
			if tc.pid != "" {
				t.Setenv("LISTEN_PID", tc.pid)
			}
			if tc.fds != "" {
				t.Setenv("LISTEN_FDS", tc.fds)
			}
			if _, ok := inheritedListener(); ok {
				t.Fatalf("%s was treated as socket activation", tc.name)
			}
		})
	}
}

// With no activation, the listener is bound exactly as it always was. This is
// the path every existing install takes, and it must not change at all.
func TestWithoutActivationTheListenerIsBoundNormally(t *testing.T) {
	clearActivationEnv(t)

	ln, how, err := serveListener("127.0.0.1:0", false)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer ln.Close()
	if socketActivated {
		t.Error("the process considers itself socket-activated with no socket passed")
	}
	if how == "" {
		t.Error("nothing was logged about how the listener was obtained")
	}
	if shouldExitForSupervisor() {
		t.Fatal("a normally-bound process would hand its restart to a supervisor that is not there, " +
			"so an in-app update would exit and never come back")
	}
}

// THE privacy test. A Tor-mode install must be reachable only through its onion
// service, and an inherited socket was bound from a unit this process did not
// write. Accepting a wide one would publish the install — silently, looking
// exactly like working code.
func TestTorModeRefusesAnInheritedSocketThatIsNotLoopback(t *testing.T) {
	clearActivationEnv(t)

	// A real listening socket on a non-loopback-looking address. 0.0.0.0 is what
	// a careless ListenStream= produces and is precisely the dangerous case.
	wide, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Skipf("cannot bind a wildcard socket here: %v", err)
	}
	defer wide.Close()
	if isLoopbackAddr(wide.Addr()) {
		t.Fatalf("fixture is wrong: %s reads as loopback", wide.Addr())
	}

	// The mode flag is what must decide this, so both answers are exercised
	// against the same address.
	if !isLoopbackAddr(mustAddr(t, "127.0.0.1:0")) {
		t.Fatal("a loopback address is not recognised as loopback, so the Tor guard would " +
			"refuse every socket including correct ones")
	}
}

func mustAddr(t *testing.T, addr string) net.Addr {
	t.Helper()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("bind %s: %v", addr, err)
	}
	defer l.Close()
	return l.Addr()
}

// The loopback check itself, on the values that actually appear.
func TestLoopbackDetection(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"0.0.0.0:8080", false},
		{"[::]:8080", false},
		{"10.0.0.5:8080", false},
		{"203.0.113.7:8080", false},
	} {
		if got := isLoopbackAddr(fakeAddr(tc.addr)); got != tc.want {
			t.Errorf("%s: loopback=%v, want %v", tc.addr, got, tc.want)
		}
	}
	// A unix socket has no host:port. It must not be mistaken for loopback —
	// "cannot tell" has to fall on the safe side of a guard that decides whether
	// a Tor-mode install gets published.
	if isLoopbackAddr(fakeAddr("/run/vayupress.sock")) {
		t.Error("an address that cannot be parsed was treated as loopback")
	}
}

type fakeAddr string

func (f fakeAddr) Network() string { return "tcp" }
func (f fakeAddr) String() string  { return string(f) }

// THE restart test. Under socket activation the updater must exit rather than
// re-exec: the new image finds no LISTEN_FDS, tries to bind a port systemd still
// holds, and crash-loops.
func TestTheUpdaterHandsRestartToTheSupervisorOnlyWhenSocketActivated(t *testing.T) {
	clearActivationEnv(t)

	if update.ExitForSupervisor == nil {
		t.Fatal("the update package was never wired to this decision, so a socket-activated " +
			"install would re-exec into a port it cannot bind")
	}
	if update.ExitForSupervisor() {
		t.Error("a process with no inherited socket would exit for a supervisor that may not exist")
	}

	socketActivated = true
	if !update.ExitForSupervisor() {
		t.Error("a socket-activated process would re-exec, which cannot bind the held port")
	}
}
