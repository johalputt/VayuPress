// SPDX-License-Identifier: Apache-2.0

package plugins

// loader.go — the plugins an operator installs. A plugin is a directory in
// the plugins directory holding a plugin.json (internal/vcb) and the binary it
// names; at start-up each one is checked and, if it passes, started as a
// sandboxed subprocess pool subscribed to its hooks. Nothing is downloaded:
// a plugin is on this machine because someone who can write there put it
// there.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/johalputt/vayupress/internal/logging"
	"github.com/johalputt/vayupress/internal/vcb"
	"github.com/johalputt/vayupress/internal/vcb/validate"
)

// subprocessWorkers is how many processes each loaded plugin runs. One: a
// hook is a short call, and the host is a small server.
const subprocessWorkers = 1

// LoadResult is what happened to one plugin directory.
type LoadResult struct {
	Name  string   // the directory's name, or the manifest's once read
	Hooks []string // the hooks it was subscribed to, when loaded
	Err   error    // why it was not loaded, or nil
}

var (
	loadMu   sync.RWMutex
	loadedAt []LoadResult
)

// LoadResults is what the last LoadDir did, one entry per plugin directory.
func LoadResults() []LoadResult {
	loadMu.RLock()
	defer loadMu.RUnlock()
	return append([]LoadResult(nil), loadedAt...)
}

// LoadDir loads every plugin under dir into reg and records each result
// (LoadResults). A plugin that fails any check is not started; the others
// are. A missing dir is no plugins, not an error.
func LoadDir(reg *Registry, dir, hostVersion string) []LoadResult {
	var out []LoadResult
	defer func() {
		loadMu.Lock()
		loadedAt = out
		loadMu.Unlock()
	}()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return out
	}
	if err == nil {
		err = private(dir, true)
	}
	if err != nil {
		out = append(out, LoadResult{Name: filepath.Base(dir), Err: fmt.Errorf("the plugins directory: %w", err)})
		return out
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		res := loadOne(reg, filepath.Join(dir, e.Name()), hostVersion)
		if res.Err != nil {
			logging.LogError("plugins", "plugin "+res.Name+" not loaded", res.Err.Error())
		}
		out = append(out, res)
	}
	return out
}

// loadOne checks and starts the plugin in pkg.
func loadOne(reg *Registry, pkg, hostVersion string) LoadResult {
	res := LoadResult{Name: filepath.Base(pkg)}
	manifestPath := filepath.Join(pkg, vcb.ManifestFilename)
	for _, p := range []struct {
		path string
		dir  bool
	}{{pkg, true}, {manifestPath, false}} {
		if err := private(p.path, p.dir); err != nil {
			res.Err = err
			return res
		}
	}
	m, err := vcb.LoadPluginManifest(manifestPath)
	if err != nil {
		res.Err = err
		return res
	}
	res.Name = m.Name
	if v := validate.Plugin(m, validate.Options{HostVersion: hostVersion, BaseDir: pkg, CheckFiles: true}); !v.OK() {
		var why []string
		for _, f := range v.Findings {
			if f.Severity == validate.Error {
				why = append(why, f.Message)
			}
		}
		res.Err = fmt.Errorf("plugin.json: %s", strings.Join(why, "; "))
		return res
	}
	// The binary is pinned: the sandbox checks it against this hash before
	// every start, so a binary changed after it was reviewed does not run.
	if m.ExecutableSHA256 == "" {
		res.Err = fmt.Errorf("plugin.json names no executable_sha256; a plugin runs only the binary its manifest pins")
		return res
	}
	if len(m.Hooks) == 0 {
		res.Err = fmt.Errorf("plugin.json subscribes to no hook, so it would never run")
		return res
	}
	sm := m.ToSandboxManifest(pkg)
	if err := private(sm.Executable, false); err != nil {
		res.Err = err
		return res
	}
	if err := RegisterSubprocess(reg, sm, m.Hooks, subprocessWorkers); err != nil {
		res.Err = err
		return res
	}
	res.Hooks = append([]string(nil), m.Hooks...)
	return res
}

// private refuses a path that someone other than this server could change:
// a symbolic link, a file owned by anyone but this process's user or root,
// or one any group or other user may write. Whoever can write a plugin's
// files runs code as the server.
func private(path string, dir bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link", path)
	case dir && !fi.IsDir():
		return fmt.Errorf("%s is not a directory", path)
	case !dir && !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("%s may be written by others (mode %04o)", path, fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by uid %d, not by this server or root", path, st.Uid)
	}
	return nil
}
