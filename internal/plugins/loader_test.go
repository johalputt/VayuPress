// SPDX-License-Identifier: Apache-2.0

//go:build linux

package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/sandbox"
)

// TestMain lets this test binary be the plugin's child step, as the server
// is (sandbox.RunHelperIfAsked).
func TestMain(m *testing.M) {
	sandbox.RunHelperIfAsked()
	os.Exit(m.Run())
}

var wordcountBin string

// wordcount builds the wordcount example once, static.
func wordcount(t *testing.T) []byte {
	t.Helper()
	if wordcountBin == "" {
		dir, err := os.MkdirTemp("", "vp-wordcount")
		if err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "wordcount")
		cmd := exec.Command("go", "build", "-o", bin, "../../docs/plugins/examples/wordcount")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("cannot build the wordcount example: %v\n%s", err, out)
		}
		wordcountBin = bin
	}
	b, err := os.ReadFile(wordcountBin)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// installPlugin writes a plugins directory holding one wordcount package,
// lets edit change the manifest, and returns the plugins directory.
func installPlugin(t *testing.T, edit func(m map[string]any)) (root, pkg string) {
	t.Helper()
	bin := wordcount(t)
	root = filepath.Join(t.TempDir(), "plugins")
	pkg = filepath.Join(root, "wordcount")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "wordcount"), bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bin)
	m := map[string]any{
		"vcb": 1, "name": "wordcount", "version": "1.0.0", "description": "counts words", "license": "Apache-2.0",
		"hooks": []string{"article.create"}, "executable": "wordcount", "executable_sha256": hex.EncodeToString(sum[:]),
		"sandbox": map[string]any{"timeout_ms": 20000},
	}
	if edit != nil {
		edit(m)
	}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root, pkg
}

// loadOnly loads root into a fresh registry and returns the one result.
func loadOnly(t *testing.T, root string) (LoadResult, *Registry) {
	t.Helper()
	t.Setenv("VAYUSANDBOX_ALLOW_NO_NAMESPACES", "1") // unprivileged CI kernels refuse namespaces
	t.Cleanup(ShutdownSubprocesses)
	reg := NewRegistry()
	res := LoadDir(reg, root, "3.18.32")
	if len(res) != 1 {
		t.Fatalf("LoadDir gave %d results, want 1: %+v", len(res), res)
	}
	return res[0], reg
}

// TestAnInstalledPluginIsLoadedAndRuns — nothing called RegisterSubprocess,
// so no plugin an operator installed ever ran. A package that passes every
// check is started and answers the hook it subscribes to.
func TestAnInstalledPluginIsLoadedAndRuns(t *testing.T) {
	root, _ := installPlugin(t, nil)
	res, reg := loadOnly(t, root)
	if res.Err != nil {
		t.Fatalf("a valid plugin was refused: %v", res.Err)
	}
	hs := reg.Handlers("article.create")
	if len(hs) != 1 {
		t.Fatalf("the plugin is subscribed to %d article.create handlers, want 1", len(hs))
	}
	if err := hs[0](context.Background(), map[string]interface{}{"content": "one two", "slug": "x"}); err != nil {
		t.Fatalf("the loaded plugin did not answer its hook: %v", err)
	}
	if got := LoadResults(); len(got) != 1 || got[0].Err != nil {
		t.Errorf("LoadResults does not record the load: %+v", got)
	}
}

// TestAPluginThatFailsACheckIsNotStarted — one check per case, each named
// by the reason the Tools page shows.
func TestAPluginThatFailsACheckIsNotStarted(t *testing.T) {
	for _, c := range []struct {
		name, why string
		edit      func(m map[string]any)
		after     func(t *testing.T, root, pkg string)
	}{
		{name: "no pinned hash", why: "pins", edit: func(m map[string]any) { delete(m, "executable_sha256") }},
		{name: "a wrong hash", why: "does not match", edit: func(m map[string]any) { m["executable_sha256"] = strings.Repeat("0", 64) }},
		{name: "no hook", why: "no hook", edit: func(m map[string]any) { delete(m, "hooks") }},
		{name: "a hook that is never fired", why: "catalogue", edit: func(m map[string]any) { m["hooks"] = []string{"article.created.v1"} }},
		{name: "a manifest others may write", why: "written by others", after: func(t *testing.T, _, pkg string) {
			chmod(t, filepath.Join(pkg, "plugin.json"), 0o666)
		}},
		{name: "a binary others may write", why: "written by others", after: func(t *testing.T, _, pkg string) {
			chmod(t, filepath.Join(pkg, "wordcount"), 0o777)
		}},
		{name: "a package directory others may write", why: "written by others", after: func(t *testing.T, _, pkg string) {
			chmod(t, pkg, 0o777)
		}},
		{name: "a plugins directory others may write", why: "written by others", after: func(t *testing.T, root, _ string) {
			chmod(t, root, 0o777)
		}},
		// A link inside the package passes the validator, which refuses only
		// one that leaves it; the loader refuses any link.
		{name: "a binary that is a link", why: "symbolic link", after: func(t *testing.T, _, pkg string) {
			real := filepath.Join(pkg, "elsewhere")
			if err := os.Rename(filepath.Join(pkg, "wordcount"), real); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("elsewhere", filepath.Join(pkg, "wordcount")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, pkg := installPlugin(t, c.edit)
			if c.after != nil {
				c.after(t, root, pkg)
			}
			res, reg := loadOnly(t, root)
			if res.Err == nil || !strings.Contains(res.Err.Error(), c.why) {
				t.Fatalf("%s: got %v, want a refusal naming %q", c.name, res.Err, c.why)
			}
			if n := len(reg.Handlers("article.create")); n != 0 {
				t.Errorf("%s: the refused plugin is subscribed anyway (%d handlers)", c.name, n)
			}
		})
	}
}

// TestAPluginOwnedByAnotherUserIsNotStarted — a file owned by someone other
// than the server or root could be replaced by that user.
func TestAPluginOwnedByAnotherUserIsNotStarted(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("only root can give a file to another user")
	}
	root, pkg := installPlugin(t, nil)
	if err := os.Lchown(filepath.Join(pkg, "plugin.json"), 4242, 4242); err != nil {
		t.Fatal(err)
	}
	res, _ := loadOnly(t, root)
	if res.Err == nil || !strings.Contains(res.Err.Error(), "owned by uid 4242") {
		t.Fatalf("got %v, want a refusal naming the owner", res.Err)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
