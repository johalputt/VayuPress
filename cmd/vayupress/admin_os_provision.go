// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// admin_os_provision.go — the unprivileged half of one-click subdomain setup.
//
// THE PROBLEM THIS SOLVES
// The service runs as www-data with NoNewPrivileges=yes, so it can swap its own
// binary but can never obtain a TLS certificate or reload nginx. Both need root.
// That is correct hardening, and it left a hole worth naming plainly: an
// operator who used only VayuOS → Update now received a current binary and no
// subdomains, with nothing anywhere saying so. Certificates silently absent,
// PGP key discovery silently dead, and the only diagnosis path was SSH — in a
// product whose promise is that you never need the terminal again.
//
// HOW THE PRIVILEGE BOUNDARY IS CROSSED
// It is not crossed. This code creates an EMPTY file. A root-side systemd .path
// unit notices it and runs a fixed, root-owned script (see
// scripts/provision-subdomains.sh). The request carries no arguments and its
// contents are never read, so the only thing this process can express is "go" —
// there is no channel through which a compromised web session could influence
// what root executes.
//
// That sentence is about EXECUTION, and it stayed true while a second question
// went unasked: where does root WRITE? The worker used to put its result, lock
// and log inside the state directory, which the installer gives to the service
// user — so a compromised web process could not change the command, but could
// replace those names with symlinks and choose the file root truncated. Root's
// output now lives in a root-owned directory (provisionOutputDir) where the
// unprivileged side cannot create names at all.
//
// A daily .timer runs the same worker regardless, so a DNS record pointed later
// is picked up without anyone asking.

// provisionInstallCommand is the single privileged step an operator ever needs.
// It is rendered verbatim and copyable, because the previous instruction —
// "re-run scripts/deploy-vayupress.sh" — assumed knowledge of where that
// checkout lives, and a full redeploy rebuilds the binary and restarts a live
// site merely to install three small files.
const provisionInstallCommand = "curl -sSL https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/install-provisioning.sh | sudo bash"

const (
	provisionRequestFile = "provision.request"
	// provisionAuditAction names the record written whenever the root unit is
	// asked to run. Both doors — this handler and the MCP tool — write it, so one
	// query answers "who made root do this".
	provisionAuditAction = "vayudomains.provision.request"
	provisionResultFile  = "provision.result"
	// provisionRequestTTL bounds how long a pending request is considered live in
	// the UI. If the units are not installed nothing consumes the flag, and
	// showing "in progress" forever would be a lie that hides the real problem.
	provisionRequestTTL = 5 * time.Minute
)

// provisionResult mirrors the JSON the root worker writes.
type provisionResult struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Ran        int    `json:"ran"`
	Failed     int    `json:"failed"`
	// Skipped counts helpers that exited 0 without doing anything — the state
	// that used to be reported as success and sent operators looking elsewhere.
	Skipped int    `json:"skipped"`
	Details string `json:"details"`
}

// The root half's locations. Package variables rather than literals so the
// tests can drive the real check against a fixture tree — /usr and /etc are not
// writable from a test, and a check nobody can exercise is how the data-dir
// mismatch below survived unnoticed in the first place.
var (
	provisionWorkerPath = "/usr/local/lib/vayupress/provision-subdomains.sh"
	provisionUnitPath   = "/etc/systemd/system/vayupress-provision.path"
)

// provisionOutputDir is where the ROOT worker writes its result and log.
//
// Deliberately not provisionStateDir(). That directory belongs to the service
// user so the panel can create its request in it, and anything root writes into
// a directory the unprivileged user owns can be pointed elsewhere first with a
// symlink. Root's own files live where the web process cannot create names.
const provisionOutputDir = "/var/lib/vayupress-provision"

// provisionOutputPath resolves one of the worker's output files, preferring the
// root-owned directory and falling back to the old in-state-directory location.
//
// The fallback is not indefinite tolerance of the unsafe path; it is for the
// window where a binary has updated and the root worker has not yet upgraded
// itself. Without it the panel would show "never run" on a working install and
// send an operator looking for a fault that is not there. Reading a file the
// service user could have written is safe in a way WRITING to one is not: the
// worst case is a wrong status on a host whose web process is already
// compromised, which is not a boundary this can defend anyway.
func provisionOutputPath(name string) string {
	if p := filepath.Join(provisionOutputDir, name); fileExists(p) {
		return p
	}
	return filepath.Join(provisionStateDir(), name)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func provisionStateDir() string {
	if d := strings.TrimSpace(os.Getenv("VAYU_DATA_DIR")); d != "" {
		return d
	}
	return "/var/lib/vayupress"
}

// provisionUnitsInstalled reports whether the root-side worker exists. Without
// it a request would sit unconsumed forever, so the UI must say so rather than
// offering a button that does nothing — a dead button is worse than no button,
// because it converts a fixable setup gap into a mystery.
func provisionUnitsInstalled() bool {
	// BOTH halves, because either alone is a trap. The worker script without the
	// .path unit is the worse one: the button renders enabled, creates a request
	// nothing consumes, and reports "running" until it times out — a control that
	// appears to work and does nothing, which is precisely what this check exists
	// to prevent. The shell updater installs the script but does not write the
	// units, so that combination is reachable in practice, not hypothetical.
	if _, err := os.Stat(provisionWorkerPath); err != nil {
		return false
	}
	unit, err := os.ReadFile(provisionUnitPath)
	if err != nil {
		return false
	}

	// THE THIRD TRAP, and the one the two checks above were blind to: the unit
	// may exist and watch a DIFFERENT FILE from the one this process writes.
	//
	// provisionStateDir() honours VAYU_DATA_DIR; the unit's PathExists= is
	// written as a literal by the installer and knows nothing about it. Set that
	// variable — a supported configuration — and the panel writes its request to
	// one path while systemd watches another. Both halves are installed, so the
	// button renders enabled, reports success, and is consumed by nothing:
	// exactly the failure described above, reached through configuration rather
	// than a partial install.
	//
	// Compared against what is actually on disk rather than assumed, because the
	// installer's literal is the only authority on what systemd will watch.
	want := filepath.Join(provisionStateDir(), provisionRequestFile)
	for _, line := range strings.Split(string(unit), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "PathExists="); ok {
			return strings.TrimSpace(v) == want
		}
	}
	return false
}

func readProvisionResult() (provisionResult, bool) {
	var res provisionResult
	b, err := os.ReadFile(provisionOutputPath(provisionResultFile))
	if err != nil {
		return res, false
	}
	if json.Unmarshal(b, &res) != nil {
		return res, false
	}
	return res, true
}

// provisionPending reports whether a request is still waiting to be consumed.
func provisionPending() bool {
	fi, err := os.Stat(filepath.Join(provisionStateDir(), provisionRequestFile))
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < provisionRequestTTL
}

// handleOSProvisionRequest asks the root worker to provision subdomain
// certificates and vhosts. Admin-only and CSRF-protected: it triggers privileged
// work, and although that work takes no input from here, the ability to make a
// server run certbot on demand is not something an anonymous caller should have
// (Let's Encrypt rate limits are a finite resource an attacker could burn).
func (a *App) handleOSProvisionRequest(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os")
		return
	}
	if !provisionUnitsInstalled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "provision-unavailable",
			"One-click provisioning is not installed on this server",
			"Re-run scripts/deploy-vayupress.sh once to install the privileged helper, then this button works.")
		return
	}
	path := filepath.Join(provisionStateDir(), provisionRequestFile)

	// Remove any existing request BEFORE writing a fresh one.
	//
	// This is the difference between a button that works and one that silently
	// does nothing, and it is a systemd detail: the watcher is a `.path` unit
	// with `PathExists=`, which fires when the file APPEARS. If a previous
	// request was never consumed — the worker failed to start, or its start
	// limit tripped — the file stays, the condition never goes false, and no
	// amount of rewriting the same path produces another trigger. The install
	// enters a state where the button is enabled, reports success, and can never
	// cause a run again.
	//
	// Deleting first forces the disappear→appear transition the unit needs. It
	// costs nothing when there was no stale request, and it is the only thing
	// the unprivileged side can do to unstick a watcher it cannot restart.
	staleCleared := false
	if _, err := os.Stat(path); err == nil {
		if os.Remove(path) == nil {
			staleCleared = true
		}
	}
	// Empty on purpose. The worker never reads the contents, and writing
	// anything here would start a channel from an unprivileged process into a
	// root one — the thing this design exists to avoid.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "provision-request-failed",
			"Could not create the provisioning request", err.Error())
		return
	}
	dbpkg.AuditLog(provisionAuditAction, dbpkg.AuditActor(r), "provision", "requested via the panel")

	note := "Provisioning runs in the background; it usually finishes within a minute."
	if staleCleared {
		note = "An earlier request was still waiting — it was cleared and a fresh one made, " +
			"which is what re-arms the watcher."
	}
	writeJSON(w, r, http.StatusOK, map[string]any{
		"status": "requested", "rearmed": staleCleared, "note": note,
	})
}

// handleOSProvisionStatus reports the last run so the console can show what
// happened without anyone reading a log over SSH.
func (a *App) handleOSProvisionStatus(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os")
		return
	}
	res, have := readProvisionResult()
	writeJSON(w, r, http.StatusOK, map[string]any{
		"installed": provisionUnitsInstalled(),
		"pending":   provisionPending(),
		"have_run":  have,
		"result":    res,
	})
}

// provisionCardHTML renders the Update-page card.
func provisionCardHTML() string {
	installed := provisionUnitsInstalled()
	res, haveRun := readProvisionResult()

	var status string
	switch {
	case !installed:
		status = `<span class="badge badge--warn">helper not installed</span>`
	case provisionPending():
		status = `<span class="badge badge--info">running…</span>`
	case haveRun && res.Failed > 0:
		status = `<span class="badge badge--warn">last run had ` + strconv.Itoa(res.Failed) + ` problem(s)</span>`
	case haveRun && res.Ran == 0:
		// Nothing was provisioned. Previously this rendered as "clean", which is
		// how a run that did absolutely nothing came to look like success.
		status = `<span class="badge badge--warn">last run provisioned nothing</span>`
	case haveRun && res.Skipped > 0:
		status = `<span class="badge badge--ok">` + strconv.Itoa(res.Ran) + ` provisioned, ` +
			strconv.Itoa(res.Skipped) + ` skipped</span>`
	case haveRun:
		status = `<span class="badge badge--ok">last run clean</span>`
	default:
		status = `<span class="badge badge--muted">never run</span>`
	}

	var detail string
	if haveRun {
		// "5 provisioned" counts HELPERS, not subdomains — the detail string right
		// beside it lists them by filename. Reading it as five subdomains is the
		// natural mistake, and it is how a run where one helper covered no domains
		// at all read as five domains provisioned.
		detail = `<p class="text-xs muted">Last run ` + html.EscapeString(res.FinishedAt) +
			` — of ` + strconv.Itoa(res.Ran+res.Skipped+res.Failed) + ` helpers: ` +
			strconv.Itoa(res.Ran) + ` did work, ` + strconv.Itoa(res.Skipped) + ` had nothing to do, ` +
			strconv.Itoa(res.Failed) + ` reported a problem. ` +
			`<span class="mono">` + html.EscapeString(res.Details) + `</span></p>`
		if strings.Contains(res.Details, "nginx-config-broken") {
			detail += `<p class="text-xs muted"><strong>nginx's configuration was already invalid ` +
				`before provisioning ran</strong>, so every helper aborted without changing anything. ` +
				`One stale vhost anywhere under <code>sites-enabled</code> does this — most often one ` +
				`written while <code>DOMAIN</code> was still <code>localhost</code>, naming a certificate ` +
				`that cannot exist. Find it with ` +
				`<code>sudo nginx -t</code>, then remove the offending symlink and run this again.</p>`
		}
		if res.Ran == 0 {
			detail += `<p class="text-xs muted">A helper skips when its DNS is not pointed — or when <code>DOMAIN</code> ` +
				`cannot be read from <code>/etc/vayupress/env</code>, which skips every one of them at once. ` +
				`Check <code>/var/lib/vayupress-provision/provision.log</code> for the reason.</p>`
		}
	}

	action := `<button type="button" class="btn btn--primary btn--sm" data-provision-run>Provision subdomains</button>`
	if !installed {
		// Show the EXACT command, copyable. The previous wording said "re-run
		// scripts/deploy-vayupress.sh", which assumes the operator knows where
		// that checkout is — and a full redeploy rebuilds the binary and restarts
		// a live site to install three small files. Both are why this step got
		// postponed and the certificate never appeared. This command installs only
		// the privileged helper, runs one pass immediately, and touches neither
		// the binary nor the database.
		action = `<p class="text-sm muted">Installing a <code>systemd</code> unit needs root, and this service ` +
			`deliberately cannot become root. That makes this the <strong>one</strong> command you ever have to run ` +
			`— it installs only the helper and provisions immediately, leaving the binary and database untouched:</p>` +
			`<div class="vm-row"><code class="mono text-xs vm-pgp__wkd" data-provision-cmd>` +
			html.EscapeString(provisionInstallCommand) + `</code>` +
			`<button type="button" class="btn btn--sm" data-provision-copy>Copy</button></div>` +
			`<p class="text-xs muted">After it finishes, every subdomain is provisioned from this page and from a ` +
			`daily sweep, with no further terminal use.</p>`
	}

	return `<div class="section-head"><span class="section-head__title">Subdomains &amp; certificates</span>` +
		`<span class="section-head__hint">Issue TLS and write vhosts for mail, PGP discovery, chat, MCP and the API</span></div>
<div class="card">
<p class="text-sm">` + status + `</p>
<p class="text-sm muted">Installing an update swaps the <strong>binary only</strong> — the service runs unprivileged and cannot obtain a certificate or reload nginx by itself. This runs that privileged step for every subdomain whose DNS is pointed: <code>mail.</code>, <code>openpgpkey.</code>, <code>talk.</code>, <code>mcp.</code> and <code>api.</code>. A subdomain that is not pointed is skipped, so it is always safe to run.</p>
<p class="text-sm muted">It also runs <strong>daily on its own</strong>, so a DNS record you point later is picked up without you doing anything.</p>
` + detail + `
<div class="vm-row">` + action + `<span class="text-sm muted" data-provision-status></span></div>
<p class="text-xs muted">Requesting a run creates an empty flag file that a root-side service watches. No argument is passed and its contents are never read, so this console can ask for provisioning and cannot influence what the privileged step does.</p>
</div>`
}
