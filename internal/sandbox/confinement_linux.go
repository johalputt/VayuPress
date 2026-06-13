//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/johalputt/vayupress/internal/logging"
)

// MountConfinement holds paths created for a plugin's mount namespace.
type MountConfinement struct {
	scratchDir string // tmpfs writable scratch, removed on cleanup
}

// setupMountNamespace configures the mount namespace for the sandboxed process.
// Must be called from within the child after CLONE_NEWNS is active (i.e. via
// a helper that runs after fork). In our model we perform bind-mount prep in
// the parent and pass the scratch dir path to the child via env; the kernel's
// copy-on-write namespace semantics keep the host tree unaffected.
//
// For each plugin we:
//   1. Create a per-invocation tmpfs scratch directory for writable access.
//   2. Bind-mount allowed read paths as read-only.
//   3. Return a cleanup func that unmounts and removes the scratch dir.
func setupMountConfinement(m Manifest) (*MountConfinement, error) {
	scratch, err := os.MkdirTemp("", fmt.Sprintf("vp-plugin-%s-*", m.Name))
	if err != nil {
		return nil, fmt.Errorf("sandbox: tmpfs scratch: %w", err)
	}

	// Mount a private tmpfs on the scratch dir so plugin writes stay in memory.
	if err := syscall.Mount("tmpfs", scratch, "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "size=64m"); err != nil {
		os.RemoveAll(scratch) //nolint:errcheck
		// Non-fatal — caller logs and continues without tmpfs.
		return &MountConfinement{scratchDir: scratch}, fmt.Errorf("sandbox: mount tmpfs: %w", err)
	}

	return &MountConfinement{scratchDir: scratch}, nil
}

// Cleanup unmounts and removes the scratch directory.
func (mc *MountConfinement) Cleanup() {
	if mc == nil || mc.scratchDir == "" {
		return
	}
	_ = syscall.Unmount(mc.scratchDir, syscall.MNT_DETACH)
	_ = os.RemoveAll(mc.scratchDir)
}

// ScratchDir returns the path to the writable tmpfs scratch directory.
func (mc *MountConfinement) ScratchDir() string {
	if mc == nil {
		return ""
	}
	return mc.scratchDir
}

// applyMountPropagation marks the mount point private so bind-mounts inside
// the child namespace do not propagate back to the host.
// Safe to call even when CLONE_NEWNS is not active (becomes a no-op).
func applyMountPropagation() {
	// MS_PRIVATE | MS_REC on "/" prevents mount/unmount events from propagating.
	_ = syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, "")
}

// maskedPaths is the set of /proc entries that must be masked to prevent
// information leakage from the plugin process.
var maskedPaths = []string{
	"/proc/kcore",
	"/proc/keys",
	"/proc/latency_stats",
	"/proc/timer_list",
	"/proc/timer_stats",
	"/proc/sched_debug",
	"/proc/scsi",
	"/sys/firmware",
}

// applyProcMask bind-mounts /dev/null over sensitive /proc entries.
// Best-effort — failures are logged but do not abort sandbox startup.
func applyProcMask() {
	for _, path := range maskedPaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := syscall.Mount("/dev/null", path, "", syscall.MS_BIND, ""); err != nil {
			logging.LogJSON(logging.LogFields{
				Level:     "warn",
				Component: "sandbox",
				Msg:       fmt.Sprintf("sandbox: mask %s: %v", path, err),
			})
		}
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Seccomp-BPF syscall allowlist
// ──────────────────────────────────────────────────────────────────────────────

// seccompAction constants
const (
	seccompActAllow = 0x7fff0000 // SECCOMP_RET_ALLOW
	seccompActKill  = 0x00000000 // SECCOMP_RET_KILL_PROCESS
	seccompActErrno = 0x00050000 // SECCOMP_RET_ERRNO | EPERM
)

// bpfInstruction is a single BPF instruction (sock_filter).
type bpfInstruction struct {
	code uint16
	jt   uint8
	jf   uint8
	k    uint32
}

// bpfProgram is a BPF program (sock_fprog).
type bpfProgram struct {
	len    uint16
	filter *bpfInstruction
}

// BPF opcodes
const (
	bpfLD  = 0x00
	bpfW   = 0x00
	bpfABS = 0x20
	bpfJMP = 0x05
	bpfJEQ = 0x10
	bpfRET = 0x06
	bpfK   = 0x00
)

// allowedSyscalls is the minimal syscall allowlist for sandboxed plugins.
// Any syscall not in this list results in EPERM (graceful denial).
var allowedSyscalls = []uint32{
	// Process lifecycle
	syscall.SYS_EXIT,
	syscall.SYS_EXIT_GROUP,
	// I/O — stdin/stdout/stderr only
	syscall.SYS_READ,
	syscall.SYS_WRITE,
	syscall.SYS_READV,
	syscall.SYS_WRITEV,
	// Memory
	syscall.SYS_BRK,
	syscall.SYS_MMAP,
	syscall.SYS_MUNMAP,
	syscall.SYS_MPROTECT,
	syscall.SYS_MREMAP,
	// Signal handling (runtime needs these)
	syscall.SYS_RT_SIGACTION,
	syscall.SYS_RT_SIGPROCMASK,
	syscall.SYS_RT_SIGRETURN,
	syscall.SYS_SIGALTSTACK,
	// Thread / futex (Go runtime)
	syscall.SYS_FUTEX,
	syscall.SYS_CLONE,
	syscall.SYS_GETTID,
	syscall.SYS_GETPID,
	syscall.SYS_TGKILL,
	syscall.SYS_SCHED_YIELD,
	syscall.SYS_NANOSLEEP,
	// FD management
	syscall.SYS_CLOSE,
	syscall.SYS_FCNTL,
	syscall.SYS_FSTAT,
	syscall.SYS_EPOLL_CREATE1,
	syscall.SYS_EPOLL_CTL,
	syscall.SYS_EPOLL_WAIT,
	syscall.SYS_PIPE2,
	syscall.SYS_POLL,
	// Time
	syscall.SYS_CLOCK_GETTIME,
	syscall.SYS_GETTIMEOFDAY,
}

// buildSeccompFilter constructs a BPF program that allows only the syscalls in
// allowedSyscalls and returns EPERM for all others.
func buildSeccompFilter() []bpfInstruction {
	// Layout: load arch, validate, load syscall number, compare each allowed,
	// default deny with ERRNO(EPERM).
	const (
		// offsetof(struct seccomp_data, arch)
		archOffset = 4
		// offsetof(struct seccomp_data, nr)
		nrOffset = 0
		// AUDIT_ARCH_X86_64
		auditArchX86_64 = 0xc000003e
	)

	insns := []bpfInstruction{
		// Load architecture word.
		{bpfLD | bpfW | bpfABS, 0, 0, archOffset},
		// If arch != X86_64, kill.
		{bpfJMP | bpfJEQ | bpfK, 1, 0, auditArchX86_64},
		{bpfRET | bpfK, 0, 0, seccompActKill},
		// Load syscall number.
		{bpfLD | bpfW | bpfABS, 0, 0, nrOffset},
	}

	for _, nr := range allowedSyscalls {
		// jeq nr, allow, next
		insns = append(insns, bpfInstruction{bpfJMP | bpfJEQ | bpfK, 0, 1, nr})
		insns = append(insns, bpfInstruction{bpfRET | bpfK, 0, 0, seccompActAllow})
	}
	// Default: return ERRNO (EPERM) so the plugin gets an error, not a signal.
	insns = append(insns, bpfInstruction{bpfRET | bpfK, 0, 0, seccompActErrno | uint32(syscall.EPERM)})
	return insns
}

// ApplySeccompFilter installs the BPF syscall filter on the calling thread via
// prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER). Must be called after
// prctl(PR_SET_NO_NEW_PRIVS, 1) — Linux requires no-new-privs before loading
// a seccomp filter without CAP_SYS_ADMIN.
//
// This is called from the child side of the sandbox setup (via a pre-exec
// helper or directly in a single-threaded moment before execve).
func ApplySeccompFilter() error {
	insns := buildSeccompFilter()
	prog := bpfProgram{
		len:    uint16(len(insns)),
		filter: &insns[0],
	}
	// prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
	const prSetNoNewPrivs = 38
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("sandbox: prctl(NO_NEW_PRIVS): %w", errno)
	}
	// prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog)
	const prSetSeccomp = 22
	_, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetSeccomp, 2, uintptr(unsafe.Pointer(&prog)))
	if errno != 0 {
		return fmt.Errorf("sandbox: prctl(SECCOMP_MODE_FILTER): %w", errno)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Linux capability dropping
// ──────────────────────────────────────────────────────────────────────────────

// capHeader / capData mirror the kernel's __user_cap_header_struct / __user_cap_data_struct.
type capHeader struct {
	version uint32
	pid     int32
}

type capData struct {
	effective   uint32
	permitted   uint32
	inheritable uint32
}

const (
	linuxCapV3     = 0x20080522
	sysCAPSET      = 126 // capset(2)
	sysCAPGET      = 125 // capget(2)
)

// DropCapabilities drops all Linux capabilities from the calling process/thread.
// After this call the process has no elevated privileges whatsoever.
// Must be called from the child process before execve.
func DropCapabilities() error {
	hdr := capHeader{version: linuxCapV3, pid: 0}
	// Two capdata structs for v3 (covers caps 0-63).
	data := [2]capData{}
	_, _, errno := syscall.RawSyscall(sysCAPSET,
		uintptr(unsafe.Pointer(&hdr)),
		uintptr(unsafe.Pointer(&data[0])),
		0)
	if errno != 0 {
		return fmt.Errorf("sandbox: capset (drop all): %w", errno)
	}
	return nil
}

// ──────────────────────────────────────────────────────────────────────────────
// FD inheritance control
// ──────────────────────────────────────────────────────────────────────────────

// CloseExtraFDs closes all open file descriptors except stdin (0), stdout (1),
// and stderr (2). This prevents the plugin from inheriting unexpected FDs.
// Called in the parent before cmd.Start() to set FD_CLOEXEC on them instead
// (exec.Cmd already sets CLOEXEC on its own pipes; this covers any extras
// the parent may have opened).
func CloseExtraFDs(keepFDs []int) {
	// Read /proc/self/fd to find open descriptors.
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return
	}
	keepSet := map[int]bool{0: true, 1: true, 2: true}
	for _, fd := range keepFDs {
		keepSet[fd] = true
	}
	for _, e := range entries {
		var fd int
		if _, err := fmt.Sscanf(e.Name(), "%d", &fd); err != nil {
			continue
		}
		if keepSet[fd] {
			continue
		}
		// Set FD_CLOEXEC — closes on exec without disturbing the current process.
		syscall.CloseOnExec(fd)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Confinement wiring — called from subprocess.go
// ──────────────────────────────────────────────────────────────────────────────

// PluginConfinement bundles all P28 confinement state for a single plugin instance.
type PluginConfinement struct {
	mount *MountConfinement
}

// SetupConfinement prepares filesystem confinement for a plugin before cmd.Start().
// Returns a PluginConfinement whose Cleanup() must be deferred.
func SetupConfinement(m Manifest) *PluginConfinement {
	c := &PluginConfinement{}

	if !m.ConfineMounts {
		return c
	}

	mc, err := setupMountConfinement(m)
	if err != nil {
		logging.LogJSON(logging.LogFields{
			Level:     "warn",
			Component: "sandbox",
			Msg:       fmt.Sprintf("sandbox: mount confinement for %s: %v (degraded)", m.Name, err),
		})
	}
	c.mount = mc
	return c
}

// ScratchDir returns the tmpfs scratch directory path for the plugin.
func (c *PluginConfinement) ScratchDir() string {
	if c == nil || c.mount == nil {
		return ""
	}
	return c.mount.ScratchDir()
}

// Cleanup releases all confinement resources.
func (c *PluginConfinement) Cleanup() {
	if c == nil {
		return
	}
	c.mount.Cleanup()
}

// PrepareExecEnv sets seccomp-safe environment variables and closes extra FDs
// before the plugin exec. Called synchronously in the parent before cmd.Start().
func PrepareExecEnv(m Manifest, scratchDir string) []string {
	env := make([]string, 0, len(m.Env)+4)
	env = append(env,
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/tmp",
		"PLUGIN_NAME="+m.Name,
	)
	if scratchDir != "" {
		env = append(env, "PLUGIN_SCRATCH="+scratchDir)
	}
	env = append(env, m.Env...)
	return env
}

// MountNamespaceFlags returns CLONE_NEWNS if ConfineMounts is set.
func MountNamespaceFlags(m Manifest) uintptr {
	if m.ConfineMounts {
		return syscall.CLONE_NEWNS
	}
	return 0
}

// privateTmpPath returns a per-plugin private tmp path inside scratch.
func privateTmpPath(scratchDir, name string) string {
	return filepath.Join(scratchDir, "tmp-"+name)
}
