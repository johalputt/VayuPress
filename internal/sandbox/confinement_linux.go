// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

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

// ──────────────────────────────────────────────────────────────────────────────
// Seccomp-BPF syscall allowlist
// ──────────────────────────────────────────────────────────────────────────────

// seccompAction constants
const (
	// Typed uint32, not untyped: SECCOMP_RET_KILL_PROCESS does not fit in an
	// int on a 32-bit build, so an untyped constant here fails to compile the
	// moment anything passes it to a variadic interface{} parameter.
	seccompActAllow uint32 = 0x7fff0000 // SECCOMP_RET_ALLOW
	// SECCOMP_RET_KILL_PROCESS. This was 0x00000000, which is
	// SECCOMP_RET_KILL_THREAD — the two differ by more than a name. Killing
	// the thread leaves the rest of a Go process running with one of its
	// threads gone mid-syscall, which does not terminate: it deadlocks on the
	// scheduler holding a dead M. A confinement breach has to end the process.
	seccompActKill  uint32 = 0x80000000
	seccompActErrno uint32 = 0x00050000 // SECCOMP_RET_ERRNO | EPERM
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

// portableSyscalls is the minimal syscall allowlist for sandboxed plugins, in
// the spellings every Linux ABI we support carries. Any syscall not in the
// final list results in EPERM (graceful denial).
//
// Architecture-specific additions live in legacyPollSyscalls; use
// allowedSyscalls() rather than this slice.
var portableSyscalls = []uint32{
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
	syscall.SYS_EPOLL_CREATE1,
	syscall.SYS_EPOLL_CTL,
	// epoll_pwait, not epoll_wait, is what the Go runtime's netpoller issues —
	// on every Linux architecture, x86-64 included. Allowing only epoll_wait
	// left a sandboxed Go plugin's netpoller taking EPERM on the one
	// architecture this product ships.
	syscall.SYS_EPOLL_PWAIT,
	// The netpoller wakes itself through an eventfd (Go 1.21 on), and a
	// refused eventfd2 is a fatal runtime error: without it a plugin died the
	// first time it opened a file, which is what starts the netpoller.
	syscall.SYS_EVENTFD2,
	syscall.SYS_PIPE2,
	syscall.SYS_PPOLL,
	// Time
	syscall.SYS_CLOCK_GETTIME,
	syscall.SYS_GETTIMEOFDAY,
}

// startSyscalls are what the helper and any plugin need to begin: execve,
// which the helper calls after the filter is installed, and the call that
// sets thread-local storage where the architecture has one (tlsSyscalls).
// Allowing execve does not open the filter: a filter, NoNewPrivs and the
// dropped capabilities are all kept across exec, so whatever a plugin runs
// runs under the same three (TestTheFilterOutlivesExec).
var startSyscalls = []uint32{syscall.SYS_EXECVE}

// fileSyscalls are a plugin's file calls, for a plugin that declares paths
// to read or write, or the network (whose resolver and TLS read files). They
// are the spellings every supported ABI shares; a stat by name is not among
// them (its number differs per ABI), so such a plugin opens and fstats.
// Which paths it reaches is the job of its RunAs identity and mount tree;
// the filter decides only which kinds of call it may make.
var fileSyscalls = []uint32{
	syscall.SYS_OPENAT,
	syscall.SYS_LSEEK,
	syscall.SYS_PREAD64,
	syscall.SYS_GETDENTS64,
	syscall.SYS_READLINKAT,
}

// writeSyscalls are added for a plugin that declares paths to write.
var writeSyscalls = []uint32{
	syscall.SYS_MKDIRAT,
	syscall.SYS_UNLINKAT,
	syscall.SYS_FTRUNCATE,
	syscall.SYS_FSYNC,
}

// syscallClass is which groups of calls beyond the base a plugin's filter
// allows, from what its manifest declares.
type syscallClass uint8

const (
	classFiles syscallClass = 1 << iota
	classWrite
	classNetwork
)

// syscallClassOf is the groups m declares.
func syscallClassOf(m Manifest) syscallClass {
	var c syscallClass
	if len(m.AllowedReadPaths) > 0 || len(m.AllowedWritePaths) > 0 || m.AllowNetwork {
		c |= classFiles
	}
	if len(m.AllowedWritePaths) > 0 {
		c |= classWrite
	}
	if m.AllowNetwork {
		c |= classNetwork
	}
	return c
}

// allowedSyscalls returns the allowlist for class on this architecture.
func allowedSyscalls(class syscallClass) []uint32 {
	out := make([]uint32, 0, 64)
	out = append(out, portableSyscalls...)
	out = append(out, statSyscalls...)
	out = append(out, legacyPollSyscalls...)
	out = append(out, startSyscalls...)
	out = append(out, tlsSyscalls...)
	if class&classFiles != 0 {
		out = append(out, fileSyscalls...)
	}
	if class&classWrite != 0 {
		out = append(out, writeSyscalls...)
	}
	if class&classNetwork != 0 {
		out = append(out, networkSyscalls...)
	}
	return out
}

// errSeccompUnsupportedArch reports that no filter can be built here. It is
// deliberately not a "filter disabled" path: the caller must fail rather than
// run the plugin unconfined.
var errSeccompUnsupportedArch = fmt.Errorf("sandbox: seccomp unsupported on linux/%s", runtime.GOARCH)

// buildSeccompFilter constructs a BPF program that allows only the syscalls in
// allowedSyscalls(class) and returns EPERM for all others.
func buildSeccompFilter(class syscallClass) ([]bpfInstruction, error) {
	// Layout: load arch, validate, load syscall number, compare each allowed,
	// default deny with ERRNO(EPERM).
	const (
		// offsetof(struct seccomp_data, arch)
		archOffset = 4
		// offsetof(struct seccomp_data, nr)
		nrOffset = 0
	)

	arch := auditArch()
	if arch == 0 {
		return nil, errSeccompUnsupportedArch
	}

	insns := []bpfInstruction{
		// Load architecture word.
		{bpfLD | bpfW | bpfABS, 0, 0, archOffset},
		// If the ABI is not the one these syscall numbers belong to, kill. A
		// task can enter the kernel under a second ABI at runtime — the
		// int 0x80 compat gate on x86-64 — where the same numbers name
		// different calls.
		{bpfJMP | bpfJEQ | bpfK, 1, 0, arch},
		{bpfRET | bpfK, 0, 0, seccompActKill},
		// Load syscall number.
		{bpfLD | bpfW | bpfABS, 0, 0, nrOffset},
	}

	for _, nr := range allowedSyscalls(class) {
		// jeq nr, allow, next
		insns = append(insns, bpfInstruction{bpfJMP | bpfJEQ | bpfK, 0, 1, nr})
		insns = append(insns, bpfInstruction{bpfRET | bpfK, 0, 0, seccompActAllow})
	}
	// Default: return ERRNO (EPERM) so the plugin gets an error, not a signal.
	insns = append(insns, bpfInstruction{bpfRET | bpfK, 0, 0, seccompActErrno | uint32(syscall.EPERM)})
	return insns, nil
}

// applySeccompFilter sets NoNewPrivs and installs the filter for class on
// the calling thread via prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER). Linux
// requires no-new-privs before a filter is loaded without CAP_SYS_ADMIN, and
// both are kept across execve. Called by the helper just before it execs
// the plugin (helper_linux.go).
func applySeccompFilter(class syscallClass) error {
	insns, err := buildSeccompFilter(class)
	if err != nil {
		return err
	}
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

// linuxCapV3 is _LINUX_CAPABILITY_VERSION_3. The call numbers come from the
// syscall package, per architecture: they were written as x86-64's 125 and
// 126, and 126 on arm64 is another call, so the drop "succeeded" there and
// dropped nothing.
const linuxCapV3 = 0x20080522

// DropCapabilities empties the calling thread's effective, permitted and
// inheritable capabilities. With NoNewPrivs set after it, an exec cannot
// grant any back, not even to uid 0. Called by the helper before execve.
func DropCapabilities() error {
	hdr := capHeader{version: linuxCapV3, pid: 0}
	// Two capdata structs for v3 (covers caps 0-63).
	data := [2]capData{}
	_, _, errno := syscall.RawSyscall(syscall.SYS_CAPSET,
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

// PluginConfinement is the parent's half of a plugin's confinement: the
// scratch directory the helper mounts a tmpfs on inside the plugin's own
// mount namespace (confineMounts). The parent mounts nothing.
type PluginConfinement struct {
	scratchDir string
}

// SetupConfinement makes the scratch directory for a plugin that confines
// its mounts. Cleanup removes it.
func SetupConfinement(m Manifest) (*PluginConfinement, error) {
	if !m.ConfineMounts {
		return &PluginConfinement{}, nil
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("vp-plugin-%s-*", m.Name))
	if err != nil {
		return nil, fmt.Errorf("sandbox: scratch directory: %w", err)
	}
	return &PluginConfinement{scratchDir: dir}, nil
}

// ScratchDir returns the plugin's scratch directory, or "".
func (c *PluginConfinement) ScratchDir() string {
	if c == nil {
		return ""
	}
	return c.scratchDir
}

// Cleanup removes the scratch directory. The tmpfs on it lived in the
// plugin's mount namespace and went with the plugin.
func (c *PluginConfinement) Cleanup() {
	if c == nil || c.scratchDir == "" {
		return
	}
	_ = os.RemoveAll(c.scratchDir)
	c.scratchDir = ""
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
