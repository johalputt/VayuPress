// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sandbox

// helper_linux.go — the step a plugin process takes between fork and exec.
//
// The seccomp filter, NoNewPrivs and the capability drop each act on the
// thread that sets them and are inherited across execve, so they have to be
// set inside the child, after the fork and before the plugin's own code
// runs. Go's os/exec gives no hook there, so the host starts itself
// (/proc/self/exe) with HelperArg; that process confines itself and then
// execs the plugin. The mount work happens here too: done in the parent it
// changed the host's own mount tree (made "/" private and covered /proc
// entries for every process), where in the child it changes only the
// plugin's new mount namespace.
//
// Any step that fails stops the plugin from starting: the host is told why
// on the plugin's stdout, as a refused response, and nothing runs
// unconfined.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// HelperArg is the argument with which the host binary runs as a plugin's
// child step. cmd/vayupress hands it to RunHelperIfAsked before anything
// else.
const HelperArg = "__vayupress-sandbox-exec"

// helperSelf is the host binary as the kernel holds it, so a binary
// replaced on disk during an update still starts the matching helper.
const helperSelf = "/proc/self/exe"

// helperSpec is what the child step does before exec.
type helperSpec struct {
	confine  bool   // the child is in its own mount namespace: mount the scratch tmpfs, mask /proc
	scratch  string // the scratch directory, made by the parent
	runAs    string // "uid:gid", or ""
	syscalls syscallClass
}

// helperArgv is the helper's argument list for spec, the plugin and its args.
func helperArgv(spec helperSpec, exe string, args []string) []string {
	out := []string{
		HelperArg,
		"confine=" + strconv.FormatBool(spec.confine),
		"scratch=" + spec.scratch,
		"runas=" + spec.runAs,
		"syscalls=" + strconv.Itoa(int(spec.syscalls)),
		"--", exe,
	}
	return append(out, args...)
}

// parseHelperArgv reads what helperArgv wrote (without HelperArg).
func parseHelperArgv(argv []string) (helperSpec, string, []string, error) {
	var spec helperSpec
	for i, a := range argv {
		if a == "--" {
			if i+1 >= len(argv) {
				return spec, "", nil, errors.New("no plugin to run")
			}
			return spec, argv[i+1], argv[i+2:], nil
		}
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return spec, "", nil, fmt.Errorf("malformed argument %q", a)
		}
		switch k {
		case "confine":
			spec.confine = v == "true"
		case "scratch":
			spec.scratch = v
		case "runas":
			spec.runAs = v
		case "syscalls":
			// Parsed at the class's own width and checked against the known
			// classes: a value that wrapped or named an unknown class would
			// widen the filter without anyone having declared it.
			n, err := strconv.ParseUint(v, 10, 8)
			if err != nil || syscallClass(n)&^(classFiles|classWrite|classNetwork) != 0 {
				return spec, "", nil, fmt.Errorf("malformed syscalls %q", v)
			}
			spec.syscalls = syscallClass(n)
		default:
			return spec, "", nil, fmt.Errorf("unknown argument %q", k)
		}
	}
	return spec, "", nil, errors.New("no plugin to run")
}

// RunHelperIfAsked runs the child step and never returns when this process
// was started as one; otherwise it returns at once.
func RunHelperIfAsked() {
	if len(os.Args) < 2 || os.Args[1] != HelperArg {
		return
	}
	// Every step below acts on the calling thread, and execve keeps only
	// that thread: it must stay the same one throughout.
	runtime.LockOSThread()
	spec, exe, args, err := parseHelperArgv(os.Args[2:])
	if err == nil {
		err = confineSelf(spec)
	}
	if err == nil {
		err = syscall.Exec(exe, append([]string{exe}, args...), os.Environ())
	}
	helperRefuse(err)
}

// confineSelf applies spec to this process, in the order each step needs:
// mounts and the bounding set while still privileged, then the identity,
// then the capability drop, then NoNewPrivs and the filter, which also
// forbid what the earlier steps used.
func confineSelf(spec helperSpec) error {
	if spec.confine {
		if err := confineMounts(spec.scratch); err != nil {
			return err
		}
	}
	dropBoundingSet()
	if spec.runAs != "" {
		if err := becomeRunAs(spec.runAs); err != nil {
			return err
		}
	}
	if err := DropCapabilities(); err != nil {
		return err
	}
	return applySeccompFilter(spec.syscalls)
}

// confineMounts makes the plugin's mount namespace private, puts a tmpfs on
// its scratch directory, and covers the /proc entries in maskedPaths. The
// caller has the child in a new mount namespace (CLONE_NEWNS), so none of
// it reaches the host.
func confineMounts(scratch string) error {
	if err := syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""); err != nil {
		return fmt.Errorf("sandbox: make the mount tree private: %w", err)
	}
	if scratch != "" {
		if err := syscall.Mount("tmpfs", scratch, "tmpfs", syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "size=64m"); err != nil {
			return fmt.Errorf("sandbox: mount the scratch tmpfs: %w", err)
		}
	}
	for _, path := range maskedPaths {
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		// A file is covered with /dev/null; a directory, which /dev/null
		// cannot be bound over, with an empty read-only tmpfs.
		if fi.IsDir() {
			err = syscall.Mount("tmpfs", path, "tmpfs", syscall.MS_RDONLY|syscall.MS_NOEXEC|syscall.MS_NOSUID|syscall.MS_NODEV, "size=0")
		} else {
			err = syscall.Mount("/dev/null", path, "", syscall.MS_BIND, "")
		}
		if err != nil {
			return fmt.Errorf("sandbox: mask %s: %w", path, err)
		}
	}
	return nil
}

// dropBoundingSet removes every capability from the bounding set, so no
// later exec can bring one back. It needs CAP_SETPCAP; a host without it
// holds no capability a plugin could regain, and NoNewPrivs covers the rest.
func dropBoundingSet() {
	const prCapbsetDrop = 24
	for c := uintptr(0); c < 64; c++ {
		if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prCapbsetDrop, c, 0); errno == syscall.EINVAL {
			return // past the last capability this kernel knows
		}
	}
}

// becomeRunAs sets the plugin's identity from "uid:gid", groups first: a
// process that has changed its uid can no longer change its groups.
func becomeRunAs(runAs string) error {
	u, g, ok := strings.Cut(runAs, ":")
	uid, errU := strconv.Atoi(u)
	gid, errG := strconv.Atoi(g)
	if !ok || errU != nil || errG != nil || uid < 0 || gid < 0 {
		return fmt.Errorf("sandbox: RunAs %q is not uid:gid", runAs)
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("sandbox: clear groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("sandbox: setgid %d: %w", gid, err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("sandbox: setuid %d: %w", uid, err)
	}
	return nil
}

// helperRefuse tells the host why the plugin did not start, as the refused
// response its first invocation reads, and exits.
func helperRefuse(err error) {
	if err == nil {
		err = errors.New("the plugin exec returned")
	}
	line, _ := json.Marshal(Response{OK: false, Error: "sandbox: plugin not started: " + err.Error()})
	_, _ = os.Stdout.Write(append(line, '\n'))
	os.Exit(126)
}
