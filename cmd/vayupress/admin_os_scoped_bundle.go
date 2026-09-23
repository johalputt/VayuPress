// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_bundle.go — a whole hand-built website for one hosted domain
// (ADR-0154 D12).
//
// The template editor fills eight fields on a design somebody else drew. What
// was asked for is a site of the kind vayupress.com is — a real, authored page —
// and that is what a custom bundle already was, for the primary only.
//
// The storage layer needed nothing: customsite.Deploy confines every write to an
// os.Root, refuses traversal in archive entries, bounds decompressed size by
// the disk, keeps the previous release for rollback, and is tested against
// hostile archives. customSiteDirFor already gives each domain its own directory with
// the scope validated as hex rather than trusted into a path.
//
// What was missing was the same thing missing everywhere else in this ADR: the
// admin side resolved by REQUEST HOST. `a.customSiteDir(r)` goes through
// contentScope(r), and an operator's admin request carries no secondary host, so
// the upload always landed on the primary. Here the directory comes from the
// domain in the PATH.

import (
	"archive/zip"
	"bytes"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/johalputt/vayupress/internal/customsite"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/domain"
)

// scopedBundleDir is this site's own bundle directory, addressed by the path.
func scopedBundleDir(d domain.Domain) string { return customSiteDirFor(d.ID) }

// handleOSScopedBundleRollback restores this site's previous bundle.
func (a *App) handleOSScopedBundleRollback(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		writeAPIError(w, r, http.StatusForbidden, "forbidden", "admin role required", "")
		return
	}
	d, ok := osScopedDomain(r)
	if !ok {
		writeAPIError(w, r, http.StatusNotFound, "unknown-domain", "no such site", "")
		return
	}
	if err := customsite.Rollback(scopedBundleDir(d)); err != nil {
		writeAPIError(w, r, http.StatusBadRequest, "rollback_failed", err.Error(), "")
		return
	}
	dbpkg.AuditLog("vayudomains.website.bundle", dbpkg.AuditActor(r), d.Host, "rolled back")
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "rolled_back"})
}

// zipFromFiles builds a deployable archive from files an assistant authored.
//
// It exists so "build me a site" through the connector produces a REAL site
// rather than eight filled-in fields. Every path goes through the same
// customsite.Deploy that an uploaded zip does — one deployment path, one set of
// confinement rules, one place traversal is refused. Writing files directly to
// disk here would have been a second implementation of the part that must never
// be wrong.
//
// It needs no size bound of its own: the files arrived in one connector
// request, which the MCP transport caps (mcp.maxRequestBytes), so the archive
// cannot outgrow what was already held in memory. Deploy applies the disk
// budget.
func zipFromFiles(files map[string]string) ([]byte, error) {
	if len(files) == 0 {
		return nil, bundleError("no files were supplied — send at least index.html")
	}

	// Sorted, so the same input always produces the same archive — a deploy that
	// differs byte-for-byte between identical calls makes "did anything change"
	// unanswerable.
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		clean := strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(name, "\\", "/")), "/")
		if clean == "" || clean == "." || strings.HasPrefix(clean, "../") {
			return nil, bundleError("refusing the path " + name)
		}
		f, err := zw.Create(clean)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type bundleError string

func (e bundleError) Error() string { return string(e) }
