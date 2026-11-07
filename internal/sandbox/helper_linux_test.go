// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// The tests here start real plugins the way the host does: this test binary
// is the helper (TestMain calls RunHelperIfAsked), which confines itself and
// execs the plugin. Plugins are built static (CGO_ENABLED=0) because a
// dynamically linked one needs the loader's file calls, which a plugin that
// declares no path is refused.

// buildStatic compiles src (a main package, or a directory) into a static
// binary.
func buildStatic(t *testing.T, name, src string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build %s (go build): %v\n%s", name, err, out)
	}
	return bin
}

// probeSrc is a plugin that answers every hook with what it finds about
// itself, as a refused response so the host hands the text back: the
// kernel's own account of its confinement (/proc/self/status) and what a
// call outside the base allowlist gets. With the argument "exec" it first
// execs itself, so what it reports is what survived an exec.
const probeSrc = `package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		err := syscall.Exec("/proc/self/exe", []string{"probe", "again"}, os.Environ())
		os.Stdout.WriteString("{\"ok\":false,\"error\":\"exec failed: " + err.Error() + "\"}\n")
		return
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var out []string
		b, err := os.ReadFile("/proc/self/status")
		if err != nil {
			out = append(out, "status="+err.Error())
		}
		for _, l := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(l, ":")
			switch k {
			case "NoNewPrivs", "Seccomp", "CapEff", "CapPrm", "CapBnd", "CapInh":
				if ok {
					out = append(out, k+"="+strings.TrimSpace(v))
				}
			}
		}
		_, _, errno := syscall.RawSyscall(syscall.SYS_GETUID, 0, 0, 0)
		out = append(out, "getuid="+strconv.Itoa(int(errno)))
		j, _ := json.Marshal(map[string]any{"ok": false, "error": "probe: " + strings.Join(out, " ")})
		os.Stdout.Write(append(j, '\n'))
	}
}
`

// probeReport runs the probe once and returns what it found, by key.
func probeReport(t *testing.T, bin string, args ...string) map[string]string {
	t.Helper()
	p := NewSubprocessPlugin(Manifest{
		Name: "probe", Executable: bin, Args: args, Timeout: 20 * time.Second,
		// A read path, so the probe may open /proc/self/status.
		AllowedReadPaths: []string{"/proc/self"},
	})
	defer p.Shutdown()
	err := p.Invoke(context.Background(), "probe", nil)
	if err == nil || !strings.Contains(err.Error(), "probe: ") {
		t.Fatalf("the probe did not report: %v", err)
	}
	got := map[string]string{}
	for _, f := range strings.Fields(err.Error()[strings.Index(err.Error(), "probe: ")+len("probe: "):]) {
		if k, v, ok := strings.Cut(f, "="); ok {
			got[k] = v
		}
	}
	return got
}

// raiseInheritable makes this thread's permitted capabilities inheritable,
// so a child forked from it starts with them.
func raiseInheritable(t *testing.T) {
	t.Helper()
	hdr := capHeader{version: linuxCapV3}
	var data [2]capData
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPGET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		t.Fatalf("capget: %v", e)
	}
	for i := range data {
		data[i].inheritable = data[i].permitted
	}
	if _, _, e := syscall.RawSyscall(syscall.SYS_CAPSET, uintptr(unsafe.Pointer(&hdr)), uintptr(unsafe.Pointer(&data[0])), 0); e != 0 {
		t.Fatalf("capset: %v", e)
	}
}

// hostCapEff is this test process's own effective capability set.
func hostCapEff(t *testing.T) string {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "CapEff:"); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TestAPluginRunsConfined — the seccomp filter, NoNewPrivs and the
// capability drop were written and never applied to a plugin. The kernel's
// account of the plugin is read from inside it, one line per control, so a
// step left out names itself.
func TestAPluginRunsConfined(t *testing.T) {
	t.Setenv("VAYUSANDBOX_ALLOW_NO_NAMESPACES", "1") // unprivileged CI kernels refuse namespaces
	src := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(src, []byte(probeSrc), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := buildStatic(t, "probe", src)
	for _, how := range []string{"as started", "after an exec"} {
		t.Run(how, func(t *testing.T) {
			// The plugin is forked from this thread with every capability it
			// holds made inheritable too (a subtest runs on its own
			// goroutine, so this is done here). With an empty inheritable
			// set, the usual case, the bounding set alone keeps an exec from
			// regaining anything and the capability drop goes unseen.
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			raiseInheritable(t)
			var args []string
			if how == "after an exec" {
				args = []string{"exec"}
			}
			got := probeReport(t, bin, args...)
			if got["Seccomp"] != "2" {
				t.Errorf("the plugin runs with Seccomp %q, want 2 (a filter): %v", got["Seccomp"], got)
			}
			if got["NoNewPrivs"] != "1" {
				t.Errorf("the plugin runs with NoNewPrivs %q, want 1: %v", got["NoNewPrivs"], got)
			}
			if got["getuid"] != "1" { // EPERM
				t.Errorf("a call outside the allowlist got %q, want EPERM: %v", got["getuid"], got)
			}
			if hostCapEff(t) == "0000000000000000" {
				t.Log("this test process holds no capability, so the capability drop is not seen to do anything here")
				return
			}
			if got["CapEff"] != "0000000000000000" || got["CapPrm"] != "0000000000000000" {
				t.Errorf("the plugin keeps capabilities: CapEff %s, CapPrm %s", got["CapEff"], got["CapPrm"])
			}
			if got["CapInh"] != "0000000000000000" {
				t.Errorf("the plugin keeps inheritable capabilities %s", got["CapInh"])
			}
			if got["CapBnd"] != "0000000000000000" {
				t.Errorf("the plugin's bounding set is %s; an exec could regain what it holds", got["CapBnd"])
			}
		})
	}
}

// TestARealPluginWorksUnderTheFilter — an example plugin from docs/plugins
// answers a hook under its filter. The Go runtime's first call on x86-64
// (arch_prctl) was not on the allowlist, so the filter, had anything
// applied it, would have stopped every Go plugin before main.
func TestARealPluginWorksUnderTheFilter(t *testing.T) {
	t.Setenv("VAYUSANDBOX_ALLOW_NO_NAMESPACES", "1")
	bin := buildStatic(t, "wordcount", "../../docs/plugins/examples/wordcount")
	p := NewSubprocessPlugin(Manifest{Name: "wordcount", Executable: bin, Timeout: 20 * time.Second})
	defer p.Shutdown()
	if err := p.Invoke(context.Background(), "article.create", map[string]interface{}{"content": "one two three", "slug": "x"}); err != nil {
		t.Fatalf("the wordcount example does not run under its filter: %v", err)
	}
}

// TestAPluginsMountsStayInItsOwnNamespace — the parent made "/" private and
// covered /proc entries in the host's own mount tree on every plugin start.
// The helper mounts only inside the plugin's namespace, so the host's mount
// table is the same after a confined plugin starts as before.
func TestAPluginsMountsStayInItsOwnNamespace(t *testing.T) {
	probe := exec.Command("unshare", "--mount", "true")
	if probe.Run() != nil {
		t.Skip("this kernel or user may not make a mount namespace; the confined path cannot run here")
	}
	before, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	bin := buildStatic(t, "wordcount", "../../docs/plugins/examples/wordcount")
	p := NewSubprocessPlugin(Manifest{Name: "confined", Executable: bin, ConfineMounts: true, Timeout: 20 * time.Second})
	defer p.Shutdown()
	if err := p.Invoke(context.Background(), "article.create", map[string]interface{}{"content": "a b"}); err != nil {
		t.Fatalf("a plugin with its own mount tree does not run: %v", err)
	}
	after, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("starting a confined plugin changed the host's mounts:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestEachDeclarationEarnsItsCalls — the filter's extra calls follow what
// the manifest declares, and nothing else: a plugin that declares nothing
// opens no file. One declaration per case.
func TestEachDeclarationEarnsItsCalls(t *testing.T) {
	for _, c := range []struct {
		name string
		m    Manifest
		want syscallClass
	}{
		{"nothing", Manifest{}, 0},
		{"a path to read", Manifest{AllowedReadPaths: []string{"/srv"}}, classFiles},
		{"a path to write", Manifest{AllowedWritePaths: []string{"/srv"}}, classFiles | classWrite},
		{"the network", Manifest{AllowNetwork: true}, classFiles | classNetwork},
	} {
		if got := syscallClassOf(c.m); got != c.want {
			t.Errorf("a plugin that declares %s gets class %03b, want %03b", c.name, got, c.want)
		}
	}
}
