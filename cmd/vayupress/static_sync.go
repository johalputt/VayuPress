package main

// static_sync.go — keep STATIC_DIR in lock-step with the assets compiled into
// the binary, so a one-click self-update (which replaces only the executable)
// also refreshes the VayuOS admin CSS/JS with no separate file-copy step and no
// stale-asset window. See ADR-0099.

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"

	rootassets "github.com/johalputt/vayupress"
	"github.com/johalputt/vayupress/internal/logging"
)

// embeddedStaticFS is the repository static/ tree compiled into the binary,
// re-rooted so lookups use web-relative paths like "css/admin-os.css" and
// "js/admin-os.js" (matching the serveAdminOSAsset rel argument).
var embeddedStaticFS = staticSub()

func staticSub() fs.FS {
	sub, err := fs.Sub(rootassets.StaticFS, "static")
	if err != nil {
		// Should never happen — the directory is embedded at build time. Fall back
		// to the un-rooted FS so callers still degrade gracefully.
		return rootassets.StaticFS
	}
	return sub
}

// syncEmbeddedStatic writes every asset embedded in the binary into staticDir,
// (re)writing a file only when its bytes differ from what is already on disk.
// It is safe to call on every boot: files VayuPress does not ship are left
// untouched, and unchanged files are skipped so mtimes (and the asset cache
// busters derived from content) stay stable.
//
// It MUST run before render.Init, which writes the authoritative minified
// public-site CSS (article/admin/high-contrast/custom). Running first lets
// render.Init win for those four files while this refreshes everything else —
// notably admin-os.css and every admin-os-*.js.
func syncEmbeddedStatic(staticDir string) {
	if staticDir == "" {
		return
	}
	written := 0
	_ = fs.WalkDir(embeddedStaticFS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(embeddedStaticFS, p)
		if err != nil {
			return nil
		}
		dest := filepath.Join(staticDir, filepath.FromSlash(p))
		if sameFileContent(dest, data) {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			logging.LogError("static", "mkdir for embedded asset "+dest, err.Error())
			return nil
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			logging.LogError("static", "write embedded asset "+dest, err.Error())
			return nil
		}
		written++
		return nil
	})
	if written > 0 {
		logging.LogInfo("static", "refreshed embedded admin assets in STATIC_DIR")
	}
}

// sameFileContent reports whether the file at path already holds exactly want.
func sameFileContent(path string, want []byte) bool {
	have, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if len(have) != len(want) {
		return false
	}
	return sha256.Sum256(have) == sha256.Sum256(want)
}
