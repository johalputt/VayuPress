// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/plugins"
)

// installWordcount puts the wordcount example, built static, in a plugins
// directory with a valid plugin.json, and points PLUGINS_DIR at it.
func installWordcount(t *testing.T) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "plugins")
	pkg := filepath.Join(root, "wordcount")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(pkg, "wordcount")
	cmd := exec.Command("go", "build", "-o", bin, "../../docs/plugins/examples/wordcount")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the wordcount example: %v\n%s", err, out)
	}
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	raw, _ := json.Marshal(map[string]any{
		"vcb": 1, "name": "wordcount", "version": "1.0.0", "description": "counts words", "license": "Apache-2.0",
		"hooks": []string{"article.create"}, "executable": "wordcount", "executable_sha256": hex.EncodeToString(sum[:]),
	})
	if err := os.WriteFile(filepath.Join(pkg, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGINS_DIR", root)
	t.Setenv("VAYUSANDBOX_ALLOW_NO_NAMESPACES", "1") // unprivileged CI kernels refuse namespaces
}

// TestTheServerLoadsTheInstalledPlugins — the plugin runtime was built and
// nothing loaded a plugin into it, so the Tools page always said none were
// installed. The server's start-up loads them when plugins are on, and the
// page lists each one.
func TestTheServerLoadsTheInstalledPlugins(t *testing.T) {
	installWordcount(t)
	t.Setenv("VAYU_PLUGINS_ENABLED", "true")
	a := &App{pluginRegistry: plugins.NewRegistry()}
	a.pluginManager = plugins.New(a.pluginRegistry)
	a.startPlugins()
	t.Cleanup(func() { a.pluginManager.Shutdown(); plugins.ShutdownSubprocesses() })
	if n := len(a.pluginRegistry.Handlers("article.create")); n != 1 {
		t.Fatalf("the installed plugin is subscribed to article.create %d times, want once", n)
	}
	if page := pluginRegistryHTML(); !strings.Contains(page, "wordcount") || !strings.Contains(page, "Running") {
		t.Errorf("Tools and plugins does not list the loaded plugin as running:\n%s", page)
	}
}

// TestPluginsStayOffUnlessTheInstallTurnsThemOn — a plugin on disk runs only
// on an install started with VAYU_PLUGINS_ENABLED=true, and the page says
// how they are turned on.
func TestPluginsStayOffUnlessTheInstallTurnsThemOn(t *testing.T) {
	installWordcount(t)
	t.Setenv("VAYU_PLUGINS_ENABLED", "")
	a := &App{pluginRegistry: plugins.NewRegistry()}
	a.pluginManager = plugins.New(a.pluginRegistry)
	a.startPlugins()
	if n := len(a.pluginRegistry.Handlers("article.create")); n != 0 {
		t.Fatalf("a plugin was loaded on an install with plugins off (%d handlers)", n)
	}
	if page := pluginRegistryHTML(); !strings.Contains(page, "Plugins are off") || !strings.Contains(page, "VAYU_PLUGINS_ENABLED=true") {
		t.Errorf("Tools and plugins does not say plugins are off and how to turn them on:\n%s", page)
	}
}
