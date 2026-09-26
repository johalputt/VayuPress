// SPDX-License-Identifier: Apache-2.0

package torspace

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// childEnv makes this test binary stand in for a Tor-Space child: started with
// it set, it makes itself undumpable as every VayuPress process does at start
// (vayuveil.ApplyProcessHardening), then waits for SIGTERM instead of running
// the tests.
const childEnv = "TORSPACE_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		<-sig
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// containsExact reports whether ss contains s exactly.
func containsExact(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// newTestSup returns a supervisor with fast timeouts and injected seams so the
// lifecycle runs without the real vayupress binary.
func newTestSup(t *testing.T) *Supervisor {
	t.Helper()
	s := New("/bin/does-not-matter", t.TempDir()+"/vayupress.db", "child-key", 8347)
	s.bootTimeout = 2 * time.Second
	s.pollInterval = 20 * time.Millisecond
	return s
}

// TestSupervisorLifecycle: a healthy child comes up (Running, Port), and a
// graceful Stop brings it down.
func TestSupervisorLifecycle(t *testing.T) {
	s := newTestSup(t)
	var gotEnv []string
	s.spawn = func(env []string) (*exec.Cmd, error) {
		gotEnv = env
		c := exec.Command("sleep", "30") // a benign long-lived stand-in child
		if err := c.Start(); err != nil {
			return nil, err
		}
		return c, nil
	}
	s.health = func(int) bool { return true }

	if err := s.Ensure(true); err != nil {
		t.Fatalf("Ensure(true) failed: %v (last=%q)", err, s.LastError())
	}
	if !s.Running() {
		t.Fatal("child should be running after a healthy Ensure(true)")
	}
	if s.Port() != 8347 {
		t.Errorf("Port() = %d, want the fixed port 8347", s.Port())
	}
	// The child got the curated env (its own Tor mode + sentinel), not ours.
	if !containsExact(gotEnv, "VAYUOS_MODE=tor") || !containsExact(gotEnv, EnvSpaceChild+"=1") {
		t.Error("spawn did not receive the curated Tor-Space child env")
	}

	// Idempotent: a second Ensure(true) while running is a no-op.
	if err := s.Ensure(true); err != nil {
		t.Errorf("second Ensure(true) should be a no-op, got %v", err)
	}

	s.Ensure(false) // graceful stop
	// Give the wait goroutine a moment to observe the exit.
	deadline := time.Now().Add(2 * time.Second)
	for s.Running() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.Running() {
		t.Error("child should be stopped after Ensure(false)")
	}
}

// TestSupervisorBackoff: a child that never becomes healthy trips the crash-loop
// backoff, so an immediate retry is refused without spawning again.
func TestSupervisorBackoff(t *testing.T) {
	s := newTestSup(t)
	s.bootTimeout = 150 * time.Millisecond
	spawns := 0
	s.spawn = func(env []string) (*exec.Cmd, error) {
		spawns++
		c := exec.Command("sleep", "30")
		if err := c.Start(); err != nil {
			return nil, err
		}
		return c, nil
	}
	s.health = func(int) bool { return false } // never healthy

	if err := s.Ensure(true); err == nil {
		t.Fatal("Ensure(true) should fail when the child never becomes healthy")
	}
	if spawns != 1 {
		t.Fatalf("expected exactly 1 spawn, got %d", spawns)
	}
	// Immediate retry must be refused by the backoff (no second spawn).
	if err := s.Ensure(true); err == nil {
		t.Error("Ensure(true) should back off after a boot failure")
	}
	if spawns != 1 {
		t.Errorf("backoff must prevent a second spawn, got %d spawns", spawns)
	}
}

// TestSupervisorSpawnError: a spawn error is recorded and does not mark running.
func TestSupervisorSpawnError(t *testing.T) {
	s := newTestSup(t)
	s.spawn = func(env []string) (*exec.Cmd, error) { return nil, errors.New("exec boom") }
	if err := s.Ensure(true); err == nil {
		t.Fatal("Ensure(true) should surface a spawn error")
	}
	if s.Running() {
		t.Error("must not be marked running after a spawn error")
	}
	if s.LastError() == "" {
		t.Error("spawn error should be recorded for the status line")
	}
}

// TestSupervisorShutdownLatch: after Shutdown() the supervisor is latched off, so
// a racing Ensure(true) (a reconcile tick firing during process shutdown) can no
// longer respawn the child.
func TestSupervisorShutdownLatch(t *testing.T) {
	s := newTestSup(t)
	spawns := 0
	s.spawn = func(env []string) (*exec.Cmd, error) {
		spawns++
		c := exec.Command("sleep", "30")
		if err := c.Start(); err != nil {
			return nil, err
		}
		return c, nil
	}
	s.health = func(int) bool { return true }

	if err := s.Ensure(true); err != nil {
		t.Fatalf("Ensure(true) failed: %v", err)
	}
	s.Shutdown()
	if s.Running() {
		t.Error("child should be stopped after Shutdown()")
	}
	// A stray reconcile tick after shutdown must NOT respawn.
	if err := s.Ensure(true); err != nil {
		t.Errorf("Ensure(true) after Shutdown should be a silent no-op, got %v", err)
	}
	if s.Running() {
		t.Error("Shutdown() must latch the supervisor off — no respawn")
	}
	if spawns != 1 {
		t.Errorf("expected exactly 1 spawn across shutdown, got %d", spawns)
	}
}

func TestCleanExePath(t *testing.T) {
	if got := cleanExePath("/usr/local/bin/vayupress (deleted)"); got != "/usr/local/bin/vayupress" {
		t.Errorf("cleanExePath did not strip the (deleted) suffix: %q", got)
	}
	if got := cleanExePath("/usr/local/bin/vayupress"); got != "/usr/local/bin/vayupress" {
		t.Errorf("cleanExePath mangled a clean path: %q", got)
	}
}

// The child a previous image of this process left running across a self-update
// is stopped and reaped, while a copy of the binary with another parent and a
// child started with arguments are left alone. The children are undumpable, as
// every VayuPress process is, so nothing here may depend on their environ.
func TestReapStopsOnlyThePreviousImagesChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func(cmd *exec.Cmd) *exec.Cmd {
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	gone := func(pid int) bool { _, err := os.Stat("/proc/" + strconv.Itoa(pid)); return os.IsNotExist(err) }

	leftover := exec.Command(exe) // exactly realSpawn's shape
	start(leftover)
	withArgs := start(exec.Command(exe, "-test.run=^$"))
	defer func() { _ = withArgs.Process.Kill(); _, _ = withArgs.Process.Wait() }()
	// A copy of the binary whose parent is a shell, not this process.
	shell := exec.Command("sh", "-c", `"$0" & echo $!; wait`, exe)
	shell.Env = append(os.Environ(), childEnv+"=1")
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, _ := out.Read(buf)
	grandchild, _ := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	defer func() { _ = syscall.Kill(grandchild, syscall.SIGTERM); _ = shell.Wait() }()
	if grandchild == 0 {
		t.Fatal("no grandchild pid")
	}
	time.Sleep(200 * time.Millisecond) // let each one reach its signal wait
	// The condition this reaper exists for, stated rather than assumed: to a
	// same-user parent, an undumpable child's environ cannot be read. As root it
	// can, which hides the fault the environ-based reaper had; CI runs as a user.
	if os.Geteuid() != 0 {
		if _, err := os.ReadFile("/proc/" + strconv.Itoa(leftover.Process.Pid) + "/environ"); err == nil {
			t.Fatal("the stand-in child's environ is readable: it is not undumpable, so this test is not the real condition")
		}
	}

	s := &Supervisor{exePath: exe}
	reaped := make(chan struct{})
	go func() { s.reapOrphan(); close(reaped) }()
	select {
	case <-reaped:
	case <-time.After(stopDrainTimeout + 10*time.Second):
		t.Fatal("reapOrphan never returned: a stopped child was never reaped, and a zombie still answers kill(pid, 0)")
	}

	if !gone(leftover.Process.Pid) {
		t.Error("the previous image's child was not stopped and reaped (still in /proc, running or a zombie)")
	}
	// A process that is not ours is not waited for, so one wrongly signalled
	// would still be exiting when reapOrphan returns: allow it the time to go
	// before judging that it was left alone.
	time.Sleep(500 * time.Millisecond)
	if gone(withArgs.Process.Pid) {
		t.Error("a child started with arguments is not a Tor-Space child and must be left alone")
	}
	if gone(grandchild) {
		t.Error("a copy of the binary with another parent must be left alone")
	}
}

// The parent PID is read after the last ')' of /proc/<pid>/stat, so a command
// name holding ") " cannot shift the fields.
func TestParentPIDOfThisProcess(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc")
	}
	if got := parentPID(os.Getpid()); got != os.Getppid() {
		t.Errorf("parentPID(self) = %d, want %d", got, os.Getppid())
	}
}
