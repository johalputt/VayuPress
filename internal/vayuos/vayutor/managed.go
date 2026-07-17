package vayutor

// managed.go — a VayuPress-owned tor daemon. When no external tor control port
// is configured or reachable, VayuTor runs its OWN tor as a child process under
// the (unprivileged) service user, with a private DataDirectory, a
// cookie-authenticated unix control socket, and no SOCKS/relay surface. This
// needs only the `tor` binary on PATH — no root, no systemd, no torrc editing,
// no group membership — so VayuTor "just works" after any update (including the
// in-app binary-only self-update) the moment the binary is present. The onions
// are still bound to VayuPress's lifetime: killing this child tears them down.

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// managedBootTimeout bounds how long we wait for our tor child's control socket
// to accept a connection. tor opens the control port early in startup (well
// before full network bootstrap), so this is generous but rarely approached.
const managedBootTimeout = 25 * time.Second

// managedTor supervises the VayuPress-owned tor process.
type managedTor struct {
	binary string // resolved tor executable (from PATH or an explicit path)
	dir    string // writable base dir we fully own, e.g. <data>/tor

	mu      sync.Mutex
	cmd     *exec.Cmd
	alive   bool
	last    string   // last spawn/boot error, for the admin status line
	strict  bool     // restrict outbound to relay ports 80/443 (firewall-friendly)
	bridges []string // normalized Bridge lines; non-empty ⇒ UseBridges block
	ptPath  string   // resolved obfs4proxy/lyrebird path; "" ⇒ vanilla bridges only

	// Managed-download of a current tor (see dist.go): used when the *system* tor
	// is too old to validate the live consensus (e.g. Debian 10's 0.4.2.x). These
	// are guarded by their own distMu, independent of mu, so a download never
	// blocks an admin status read.
	distEnabled bool
	distMu      sync.Mutex
	distBin     string    // cached path to a validated, downloaded tor ("" = none)
	distLibDir  string    // dir holding that build's bundled libs (LD_LIBRARY_PATH)
	distErr     string    // last download failure (host-libc-too-old, network, …)
	distNextTry time.Time // cooldown: don't re-download every reconcile on failure
}

// setBridges configures the bridge lines (and obfs4 PT path) tor uses on its
// next (re)start. Empty clears them.
func (m *managedTor) setBridges(lines []string, ptPath string) {
	m.mu.Lock()
	m.bridges, m.ptPath = lines, ptPath
	m.mu.Unlock()
}

// usingBridges reports whether bridge mode is currently configured.
func (m *managedTor) usingBridges() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bridges) > 0
}

// bridgesEqual reports whether the configured bridge lines + PT path already
// match, so the caller can avoid a needless tor restart.
func (m *managedTor) bridgesEqual(lines []string, ptPath string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ptPath != ptPath || len(m.bridges) != len(lines) {
		return false
	}
	for i := range lines {
		if m.bridges[i] != lines[i] {
			return false
		}
	}
	return true
}

// resolvePT lazily locates the obfs4 pluggable-transport binary, mirroring
// resolveBinary()'s PATH-first approach so installing obfs4proxy after start is
// picked up with no restart. Debian ships it as `obfs4proxy`; newer distros as
// `lyrebird`.
func (m *managedTor) resolvePT() string {
	for _, name := range []string{"obfs4proxy", "lyrebird"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range []string{"/usr/bin/obfs4proxy", "/usr/bin/lyrebird", "/usr/sbin/obfs4proxy"} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	// No system pluggable transport — fall back to VayuPress's OWN embedded obfs4
	// (tor execs this same binary with the transport subcommand). This is what
	// makes obfs4 bridges work with nothing installed on the server.
	if selfObfs4 != "" {
		return selfObfs4
	}
	return ""
}

// selfObfs4 is the exec spec ("<vayupress> __run-obfs4") for VayuPress's built-in
// obfs4 transport, injected by the host at startup so this package stays free of
// the transport implementation. Empty when the host doesn't provide one.
var selfObfs4 string

// SetEmbeddedObfs4 registers the host's built-in obfs4 transport exec spec, used
// as the pluggable transport when no system obfs4proxy is installed.
func SetEmbeddedObfs4(execSpec string) { selfObfs4 = execSpec }

// setStrict enables (or disables) firewall-friendly mode: tor only reaches
// relays on ports 80/443, which punches through the restrictive egress firewalls
// common on locked-down VPS hosts. Applied on the next (re)start.
func (m *managedTor) setStrict(v bool) {
	m.mu.Lock()
	m.strict = v
	m.mu.Unlock()
}

func (m *managedTor) isStrict() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.strict
}

// newManagedTor prepares (but does not start) a managed instance. binary may be
// "" — ensure then reports a clear "tor not installed" error instead of a
// cryptic dial failure.
func newManagedTor(binary, dir string) *managedTor {
	return &managedTor{binary: binary, dir: dir}
}

func (m *managedTor) dataDir() string       { return filepath.Join(m.dir, "data") }
func (m *managedTor) torrcPath() string     { return filepath.Join(m.dir, "torrc") }
func (m *managedTor) controlSocket() string { return filepath.Join(m.dir, "control.sock") }
func (m *managedTor) cookiePath() string    { return filepath.Join(m.dataDir(), "control_auth_cookie") }
func (m *managedTor) logPath() string       { return filepath.Join(m.dir, "tor.log") }
func (m *managedTor) pidPath() string       { return filepath.Join(m.dir, "tor.pid") }
func (m *managedTor) lockPath() string      { return filepath.Join(m.dataDir(), "lock") }

// controlAddr is what dialControl expects for a unix control socket.
func (m *managedTor) controlAddr() string { return "unix:" + m.controlSocket() }

// resolveBinary returns the tor executable, resolving it from PATH the first
// time it is actually needed (and caching the result). Resolving lazily — rather
// than once at process start — means installing tor AFTER VayuPress is already
// running is picked up on the next reconcile, with no restart.
func (m *managedTor) resolveBinary() string {
	m.mu.Lock()
	override := strings.TrimSpace(m.binary)
	m.mu.Unlock()
	if override != "" {
		return override // operator-configured explicit path always wins
	}
	// A current tor VayuPress previously downloaded into its own state dir.
	if p := m.cachedDistBinary(); p != "" {
		return p
	}
	sys, serr := exec.LookPath("tor")
	if !m.distIsEnabled() {
		if serr == nil {
			return sys // legacy behaviour: use whatever tor is on PATH
		}
		return ""
	}
	// Managed-download path: prefer a system tor only when it is new enough to
	// validate today's consensus; otherwise fetch a current one ourselves.
	if serr == nil {
		if v, ok := torBinVersion(sys, ""); ok && v.atLeast(torMinVersion) {
			return sys
		}
	}
	if p, err := m.ensureDist(); err == nil && p != "" {
		return p
	}
	// Last resort: an old (or unversioned) system tor still boots and lets the
	// admin page show the precise "your tor is too old — here's the fix" guidance
	// rather than a bare "tor not installed".
	if serr == nil {
		return sys
	}
	return ""
}

// running reports whether our tor child is currently alive.
func (m *managedTor) running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.alive
}

// lastError returns the last spawn/boot failure, if any.
func (m *managedTor) lastError() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

// buildTorrc renders a minimal, onion-services-only tor configuration. No SOCKS
// proxy, no relaying, no exit — this tor exists solely to publish VayuTor's
// onion services and expose a loopback control socket to VayuPress.
func (m *managedTor) buildTorrc() string {
	var b strings.Builder
	b.WriteString("# Generated by VayuTor — do not edit; regenerated on every start.\n")
	b.WriteString("DataDirectory " + m.dataDir() + "\n")
	b.WriteString("ControlPort unix:" + m.controlSocket() + "\n")
	b.WriteString("CookieAuthentication 1\n")
	// Client-only: no SOCKS listener, no relaying, no directory service. Onion
	// service publication does not need any of them.
	b.WriteString("SocksPort 0\n")
	b.WriteString("RunAsDaemon 0\n")
	// Firewall-friendly mode: only reach relays on 80/443, which gets through the
	// restrictive egress firewalls common on locked-down hosts. Enabled
	// automatically after a stalled bootstrap.
	if m.isStrict() {
		b.WriteString("ReachableAddresses *:80,*:443\n")
	}
	// Bridges: the terminal escalation for hosts that null-route/block direct
	// relay IPs. UseBridges makes tor reach the network ONLY through these
	// unlisted bridges (whose IPs aren't on the public-relay blocklist); obfs4
	// additionally disguises the traffic to defeat DPI. No SOCKS/relay surface is
	// added — obfs4proxy is a client-side managed transport tor spawns itself,
	// with its pt_state under DataDirectory (already writable). SafeLogging (tor
	// default) keeps bridge IPs/fingerprints out of the log the admin page tails.
	m.mu.Lock()
	bridges, pt := m.bridges, m.ptPath
	m.mu.Unlock()
	if len(bridges) > 0 {
		if pt != "" {
			b.WriteString("ClientTransportPlugin obfs4 exec " + pt + "\n")
		}
		b.WriteString("UseBridges 1\n")
		for _, ln := range bridges {
			b.WriteString("Bridge " + ln + "\n")
		}
	}
	// A pidfile lets a restarted VayuPress find and reap an orphaned tor from a
	// previous run (e.g. after an in-place self-update re-exec) that still holds
	// this DataDirectory's lock and control socket.
	b.WriteString("PidFile " + m.pidPath() + "\n")
	// Persist tor's notice log so bootstrap/network problems are inspectable.
	b.WriteString("Log notice file " + m.logPath() + "\n")
	return b.String()
}

// ensure starts the tor child if it is not already running and blocks until its
// control socket is reachable (or a timeout). Idempotent: a live child is a
// no-op. It never uses the caller's context for the process lifetime (tor must
// outlive a single reconcile) — ctx only bounds the boot wait.
func (m *managedTor) ensure(ctx context.Context) error {
	if m.running() {
		return nil
	}
	bin := m.resolveBinary()
	if bin == "" {
		if de := m.distDownloadErr(); de != "" {
			return m.fail("VayuTor tried to download its own Tor but couldn't: " + de)
		}
		return m.fail("tor is not installed on the server — run one command as root to add it: `sudo apt-get install -y tor` (or re-run the VayuPress updater). VayuTor then comes up automatically within a minute; no restart needed")
	}
	// tor refuses to start unless its DataDirectory is private (0700) and owned
	// by the running user. We create it fresh under our own writable tree.
	if err := os.MkdirAll(m.dataDir(), 0o700); err != nil {
		return m.fail("cannot create tor data dir: " + err.Error())
	}
	_ = os.Chmod(m.dataDir(), 0o700)
	// Reap an orphaned tor from a previous VayuPress run (common after an in-place
	// self-update re-exec, where the old child survives but this new process lost
	// its handle). It still holds the DataDirectory lock + control socket, so a
	// fresh tor cannot start and the control connection resets — clear it first.
	m.reapOrphan()
	// Stale lock/socket from an unclean prior exit would block startup / bind.
	_ = os.Remove(m.controlSocket())
	_ = os.Remove(m.lockPath())
	if err := os.WriteFile(m.torrcPath(), []byte(m.buildTorrc()), 0o600); err != nil {
		return m.fail("cannot write torrc: " + err.Error())
	}

	// Deliberately NOT exec.CommandContext: the daemon must survive the reconcile
	// context. Its lifetime is managed by stop()/the wait goroutine below.
	cmd := exec.Command(bin, "-f", m.torrcPath()) //nolint:gosec // fixed binary + our own torrc
	// A VayuPress-downloaded tor bundles its own libssl/libevent/… next to the
	// binary; point the loader at that dir so it runs without touching the host's
	// system libraries. A system tor (libDir "") keeps the default environment.
	if lib := m.libDirFor(bin); lib != "" {
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+lib)
	}
	// Capture tor's own log to a file so bootstrap/network failures are
	// diagnosable ("why is my onion offline?") instead of being silently
	// discarded. Truncated each start; tor also mirrors this via `Log notice`.
	if lf, lerr := os.OpenFile(m.logPath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600); lerr == nil {
		cmd.Stdout = lf
		cmd.Stderr = lf
	}
	if err := cmd.Start(); err != nil {
		return m.fail("cannot start tor: " + err.Error())
	}
	m.mu.Lock()
	m.cmd = cmd
	m.alive = true
	m.last = ""
	m.mu.Unlock()
	// Reap the child and record when it exits so running() stays accurate and the
	// next reconcile respawns it.
	go func() {
		_ = cmd.Wait()
		m.mu.Lock()
		m.alive = false
		m.mu.Unlock()
	}()

	if err := m.waitControl(ctx); err != nil {
		return m.fail("tor started but control socket never came up: " + err.Error())
	}
	return nil
}

// waitControl polls the control socket until it accepts a connection.
func (m *managedTor) waitControl(ctx context.Context) error {
	deadline := time.Now().Add(managedBootTimeout)
	for {
		if !m.running() {
			return errors.New("tor process exited during startup")
		}
		c, err := net.DialTimeout("unix", m.controlSocket(), time.Second)
		if err == nil {
			_ = c.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// reapOrphan kills a tor process left over from a previous VayuPress run that
// this (restarted) process no longer tracks but which still holds our
// DataDirectory lock and control socket. It only runs when we believe we have no
// live child. Every kill target is verified to be a `tor` process started
// against OUR config, so PID reuse can never make it kill something unrelated.
// Best-effort and Linux-oriented (VayuPress's server target); no /proc → skip.
func (m *managedTor) reapOrphan() {
	// Fast path: the pidfile our own tor writes.
	if data, err := os.ReadFile(m.pidPath()); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && m.isOurTor(pid) {
			m.killTor(pid)
		}
	}
	// Catch-all: scan /proc for any tor launched with `-f <our torrc>`. This
	// covers an orphan from a version that predates the pidfile.
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, e := range entries {
			pid, perr := strconv.Atoi(e.Name())
			if perr != nil {
				continue
			}
			cl, rerr := os.ReadFile("/proc/" + e.Name() + "/cmdline")
			if rerr != nil {
				continue
			}
			args := strings.ReplaceAll(string(cl), "\x00", " ")
			if strings.Contains(args, m.torrcPath()) && m.isOurTor(pid) {
				m.killTor(pid)
			}
		}
	}
	_ = os.Remove(m.pidPath())
}

// isOurTor reports whether pid is a live process named "tor". The cmdline match
// in reapOrphan already scoped it to our torrc; this is the identity backstop.
func (m *managedTor) isOurTor(pid int) bool {
	if pid <= 1 || pid == os.Getpid() {
		return false
	}
	comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return true // no /proc to check — trust the caller's cmdline/pidfile scoping
	}
	return strings.TrimSpace(string(comm)) == "tor"
}

// killTor SIGTERMs a pid, waits briefly for it to release the DataDirectory
// lock, then SIGKILLs if still alive.
func (m *managedTor) killTor(pid int) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Signal(syscall.SIGTERM)
	for i := 0; i < 20; i++ {
		if p.Signal(syscall.Signal(0)) != nil {
			return // gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = p.Signal(syscall.SIGKILL)
}

// stop terminates the tor child (which tears down its onions). Best-effort.
func (m *managedTor) stop() {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.alive = false
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = os.Remove(m.controlSocket())
}

// tailLog returns the last few non-empty lines of tor's log, so the admin page
// can show WHY a bootstrap is stuck (e.g. "Stuck at 5%: Connecting to a relay")
// without the operator needing shell access. Best-effort; "" if unavailable.
func (m *managedTor) tailLog(maxLines int) string {
	f, err := os.Open(m.logPath())
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
	// Keep the last maxLines non-empty lines.
	var out []string
	for i := len(lines) - 1; i >= 0 && len(out) < maxLines; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			out = append([]string{s}, out...)
		}
	}
	return strings.Join(out, "\n")
}

// fail records the latest error for the admin status line and returns it as an
// error whose message the engine surfaces verbatim.
func (m *managedTor) fail(msg string) error {
	m.mu.Lock()
	m.last = msg
	m.mu.Unlock()
	return errors.New("vayutor: " + msg)
}
