package torspace

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

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

func TestContainsExact(t *testing.T) {
	ss := []string{"A=1", "B=2"}
	if !containsExact(ss, "A=1") || containsExact(ss, "A=") || containsExact(ss, "C=3") {
		t.Error("containsExact must match whole entries only")
	}
}
