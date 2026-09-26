// SPDX-License-Identifier: Apache-2.0

package render

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The cache directory holds more than rendered pages. The pre-update database
// backups (update-backups/), the search index, the VayuShield intel and
// verified-bot lists, and the sitemap, feed and robots files all live beside
// them, and none of those can be rebuilt by a page request. So what "clearing
// the cache" counts is an allow-list, not everything under CACHE_DIR:
// renderedCacheDir names exactly the directories the renderer writes pages
// into.
//
// Nothing here deletes pages any more. Deleting every rendered page at once
// made every request a database render (johal.in, 2026-09-26); pages are now
// marked stale (CachePurgeAll) and rebuilt at a pace the host can carry.
func renderedCacheDir(name string) bool {
	switch name {
	case "home", "posts", "tags":
		return true
	}
	// Per-domain pages: d_<domain>/tags/… (handlers_tags.go). A per-domain
	// home lives under home/, which is already listed.
	return strings.HasPrefix(name, "d_") && len(name) > 2
}

// CacheUsage reports the files and bytes held in rendered pages under root (a
// CACHE_DIR). It walks the tree, so it is for the footprint refresher, not a
// request path.
func CacheUsage(root string) (files int, bytes int64) {
	for _, dir := range renderedCacheDirs(root) {
		n, b := treeUsage(dir)
		files += n
		bytes += b
	}
	return files, bytes
}

// renderedCacheDirs lists the real (non-symlink) directories directly under
// root that hold rendered pages.
func renderedCacheDirs(root string) []string {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		// DirEntry.IsDir reports the entry itself, so a symlink to a directory
		// is not a directory here and is skipped.
		if e.IsDir() && renderedCacheDir(e.Name()) {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

// treeUsage counts the regular files under dir and their total size.
func treeUsage(dir string) (files int, bytes int64) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil //nolint:nilerr // an unreadable entry is simply not counted
		}
		if fi, err := d.Info(); err == nil {
			files++
			bytes += fi.Size()
		}
		return nil
	})
	return files, bytes
}
