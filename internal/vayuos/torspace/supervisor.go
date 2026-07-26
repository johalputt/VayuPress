// SPDX-License-Identifier: Apache-2.0

package torspace

// supervisor.go — supervises the Tor-Space child vayupress instance (the second,
// isolated anonymous world). It reuses the managed-Tor supervision shape
// (internal/vayuos/vayutor/managed.go): a DETACHED child that outlives a single
// reconcile, a cmd.Wait() goroutine that flips an alive flag, and a tailable
// log. It adds the guarantees the ADR-0141 anonymity review required:
//
//   - a CURATED child environment (see BuildChildEnv) — never the parent's, so
//     no secret leaks and the worlds don't correlate;
//   - crash-loop BACKOFF so a child that fails to boot can't hot-spin a second
//     full VayuPress in a tight respawn loop;
//   - a GRACEFUL stop (SIGTERM → drain window → SIGKILL) because this child owns
//     a live SQLite DB and a hard kill mid-WAL risks corruption;
//   - reaping a prior-run orphan by an ENVIRON role marker (VAYUOS_SPACE_CHILD=1
//     + our own DB path), never by binary name — parent and child are both
//     `vayupress`, so a name match would risk killing the parent.
//
// It spawns NOTHING until Ensure(true) is called. This file is not yet wired to
// a live toggle (that lands with the graceful-drain verification + review).

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	childBootTimeout   = 30 * time.Second
	healthPollInterval = 500 * time.Millisecond
	stopDrainTimeout   = 20 * time.Second // SIGTERM grace for a SQLite WAL checkpoint before SIGKILL
	backoffBase        = 2 * time.Second
	backoffMax         = 5 * time.Minute
	backoffResetAfter  = 60 * time.Second // a child alive at least this long clears the failure count
)

// Supervisor manages the Tor-Space child instance.
type Supervisor struct {
	exePath      string // this binary, resolved once (self-exec)
	parentDBPath string // parent DB path → child root under its directory

	// Injectable seams so the lifecycle is unit-testable without the real binary.
	spawn        func(env []string) (*exec.Cmd, error)
	health       func(port int) bool
	bootTimeout  time.Duration
	pollInterval time.Duration

	// startMu serialises the whole Ensure body (start AND stop) so two callers —
	// the reconcile ticker and the toggle POST's `go reconcileTorSpace()` — can
	// never both pass the Running() gate and double-spawn, and reapOrphan can
	// never fire against a child a concurrent Ensure is actively starting.
	startMu sync.Mutex

	mu           sync.Mutex
	cmd          *exec.Cmd
	alive        bool
	shuttingDown bool // latched by Shutdown(): Ensure(true) becomes a permanent no-op
	port         int
	onion        string
	apiKey       string
	last         string
	startedAt    time.Time
	fails        int
	nextTry      time.Time
}

// New prepares (does not start) a supervisor. apiKey must be the child's stable,
// DISTINCT key (persisted by the caller so the child's identity survives
// restarts). port is the FIXED loopback port the child binds and the dedicated
// onion targets — they must agree, so it is chosen once by the caller. exePath ""
// resolves to this process's own binary.
func New(exePath, parentDBPath, apiKey string, port int) *Supervisor {
	s := &Supervisor{exePath: cleanExePath(exePath), parentDBPath: parentDBPath, apiKey: apiKey, port: port}
	s.spawn = s.realSpawn
	s.health = httpHealth
	s.bootTimeout = childBootTimeout
	s.pollInterval = healthPollInterval
	return s
}

// SetOnion records the child's minted .onion (its DOMAIN), applied on the next
// (re)start. The onion engine calls this once the dedicated onion is up.
func (s *Supervisor) SetOnion(onion string) {
	s.mu.Lock()
	s.onion = strings.TrimSpace(onion)
	s.mu.Unlock()
}

// Root is the child's isolated data root (under the parent's writable tree).
func (s *Supervisor) Root() string { return ChildSpaceRoot(s.parentDBPath) }

// Running reports whether the child is currently alive.
func (s *Supervisor) Running() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.alive }

// Port returns the child's loopback port (0 until started).
func (s *Supervisor) Port() int { s.mu.Lock(); defer s.mu.Unlock(); return s.port }

// APIKey returns the child's distinct API key, so the parent can authenticate as
// admin when it proxies the operator's console into the Tor world. It is the
// child's own key (never the parent's), matching the key the child boots with.
func (s *Supervisor) APIKey() string { s.mu.Lock(); defer s.mu.Unlock(); return s.apiKey }

// LastError returns the last spawn/boot failure for the admin status line.
func (s *Supervisor) LastError() string { s.mu.Lock(); defer s.mu.Unlock(); return s.last }

// Ensure converges the child to the desired running state. on=false stops it;
// on=true starts it if not alive, respecting crash-loop backoff. Idempotent.
// The entire body is serialised by startMu so concurrent callers can never
// double-spawn and a stop can never interleave with an in-flight start.
func (s *Supervisor) Ensure(on bool) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if !on {
		s.Stop()
		return nil
	}
	s.mu.Lock()
	down := s.shuttingDown
	running := s.alive
	backoff := !s.nextTry.IsZero() && time.Now().Before(s.nextTry)
	onion, apiKey := s.onion, s.apiKey
	s.mu.Unlock()
	if down {
		// Parent is shutting down — never respawn (a stray reconcile tick must not
		// resurrect the child after the shutdown drain).
		return nil
	}
	if running {
		return nil
	}
	if backoff {
		return errors.New("torspace: backing off after repeated boot failures")
	}

	if err := os.MkdirAll(s.Root(), 0o700); err != nil {
		return s.fail("cannot create Tor-Space data dir: " + err.Error())
	}
	// Reap an orphaned child from a previous parent run (e.g. after a self-update
	// re-exec) so it can't hold a DB lock or its loopback port.
	s.reapOrphan()

	s.mu.Lock()
	port := s.port
	s.mu.Unlock()
	cmd, err := s.spawn(BuildChildEnv(s.Root(), onion, port, apiKey))
	if err != nil {
		return s.recordFailure("cannot start Tor Space: " + err.Error())
	}
	s.mu.Lock()
	s.cmd = cmd
	s.alive = true
	s.last = ""
	s.startedAt = time.Now()
	s.mu.Unlock()
	go s.waitReap(cmd)

	if err := s.waitHealthy(port); err != nil {
		s.Stop()
		return s.recordFailure("Tor Space started but never became healthy: " + err.Error())
	}
	s.mu.Lock()
	s.fails = 0
	s.nextTry = time.Time{}
	s.mu.Unlock()
	return nil
}

// waitReap records the child's exit and grows the crash-loop backoff when it
// dies quickly.
func (s *Supervisor) waitReap(cmd *exec.Cmd) {
	_ = cmd.Wait()
	s.mu.Lock()
	s.alive = false
	if time.Since(s.startedAt) < backoffResetAfter {
		s.fails++
		shift := s.fails - 1
		if shift > 8 {
			shift = 8
		}
		d := backoffBase << uint(shift)
		if d > backoffMax || d <= 0 {
			d = backoffMax
		}
		s.nextTry = time.Now().Add(d)
	}
	s.mu.Unlock()
}

func (s *Supervisor) waitHealthy(port int) error {
	deadline := time.Now().Add(s.bootTimeout)
	for {
		if s.isShuttingDown() {
			// Shutdown() latched while we were booting — abort fast so it can grab
			// startMu and drain the child instead of blocking on the full timeout.
			return errors.New("shutting down")
		}
		if !s.Running() {
			return errors.New("process exited during startup")
		}
		if s.health(port) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout")
		}
		time.Sleep(s.pollInterval)
	}
}

// isShuttingDown reports whether Shutdown() has latched the supervisor off.
func (s *Supervisor) isShuttingDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shuttingDown
}

// Shutdown permanently stops the child and latches the supervisor off so a
// racing Ensure(true) (a reconcile tick firing during process shutdown) can no
// longer respawn it. It sets the latch first (unblocking any in-flight boot's
// waitHealthy), then serialises on startMu to perform the final graceful stop.
// Idempotent; call from the parent's shutdown sequence to drain the detached
// child deterministically (it is not otherwise signalled when the parent exits).
func (s *Supervisor) Shutdown() {
	s.mu.Lock()
	s.shuttingDown = true
	s.mu.Unlock()
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.Stop()
}

// Stop gracefully terminates the child: SIGTERM (let it close its listener and
// WAL-checkpoint its SQLite), wait up to the drain timeout, then SIGKILL.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	cmd := s.cmd
	s.cmd = nil
	s.alive = false
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(stopDrainTimeout)
	for time.Now().Before(deadline) {
		if cmd.Process.Signal(syscall.Signal(0)) != nil {
			return // exited cleanly
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
}

// realSpawn starts a detached child vayupress with the curated env. The child
// must outlive a single reconcile, so it is NOT bound to a context.
func (s *Supervisor) realSpawn(env []string) (*exec.Cmd, error) {
	if s.exePath == "" {
		return nil, errors.New("own binary path unknown")
	}
	cmd := exec.Command(s.exePath) //nolint:gosec // self-exec of our own resolved binary with a curated env
	cmd.Env = env
	cmd.Dir = s.Root()
	if lf, err := os.OpenFile(filepath.Join(s.Root(), "child.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); err == nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// httpHealth reports whether the child answers 200 on its loopback /health.
func httpHealth(port int) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/health") //nolint:gosec // fixed loopback host, int port
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// reapOrphan kills a Tor-Space child left over from a previous parent run,
// identified by its ENVIRON role marker (VAYUOS_SPACE_CHILD=1 AND our own child
// DB path) — never by binary name, which the parent shares. Linux-only,
// best-effort; no /proc → skip.
func (s *Supervisor) reapOrphan() {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	self := os.Getpid()
	dbMarker := "DB_PATH=" + filepath.Join(s.Root(), "vayupress.db")
	for _, e := range entries {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil || pid == self {
			continue
		}
		raw, rerr := os.ReadFile("/proc/" + e.Name() + "/environ")
		if rerr != nil {
			continue
		}
		vars := strings.Split(string(raw), "\x00")
		if containsExact(vars, EnvSpaceChild+"=1") && containsExact(vars, dbMarker) {
			killGraceful(pid)
		}
	}
}

// killGraceful SIGTERMs a pid, waits for it to exit, then SIGKILLs.
func killGraceful(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
	for i := 0; i < 30; i++ {
		if p.Signal(syscall.Signal(0)) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.Signal(syscall.SIGKILL)
}

// TailLog returns the last few non-empty lines of the child's log for the admin
// status panel. Best-effort; "" if unavailable.
func (s *Supervisor) TailLog(maxLines int) string {
	f, err := os.Open(filepath.Join(s.Root(), "child.log")) //nolint:gosec // fixed path under our own data root
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	const window = 4096
	start := info.Size() - window
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return ""
	}
	buf := make([]byte, info.Size()-start)
	n, _ := f.Read(buf)
	lines := strings.Split(strings.TrimRight(string(buf[:n]), "\n"), "\n")
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			out = append([]string{t}, out...)
		}
	}
	return strings.Join(out, "\n")
}

// fail records an error for the status line and returns it (no backoff bump —
// used for setup failures that are not a child crash).
func (s *Supervisor) fail(msg string) error {
	s.mu.Lock()
	s.last = msg
	s.mu.Unlock()
	return errors.New("torspace: " + msg)
}

// recordFailure records an error AND bumps the crash-loop backoff (a real
// spawn/health failure).
func (s *Supervisor) recordFailure(msg string) error {
	s.mu.Lock()
	s.last = msg
	s.fails++
	shift := s.fails - 1
	if shift > 8 {
		shift = 8
	}
	d := backoffBase << uint(shift)
	if d > backoffMax || d <= 0 {
		d = backoffMax
	}
	s.nextTry = time.Now().Add(d)
	s.mu.Unlock()
	return errors.New("torspace: " + msg)
}

// cleanExePath resolves the current binary path, stripping the " (deleted)"
// suffix Linux appends after an in-place self-update so the child execs the real
// file.
func cleanExePath(p string) string {
	if strings.TrimSpace(p) == "" {
		if exe, err := os.Executable(); err == nil {
			p = exe
		}
	}
	return strings.TrimSuffix(p, " (deleted)")
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
