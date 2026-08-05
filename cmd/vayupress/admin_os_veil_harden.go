// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_veil_harden.go — ADR-0150 §5 S6, the unprivileged half.
//
// The posture report can tell an operator that NoNewPrivileges is not in force
// for this process. Until now the only thing it could do about that was describe
// the problem, and this project's standing rule is explicit that describing a repair the
// operator then performs by hand is a product failure being narrated rather than
// fixed. Editing a systemd unit needs root and this service deliberately cannot
// become root, so the panel does the only correct thing available: it REQUESTS,
// and it REPORTS WHAT HAPPENED.
//
// The privilege boundary is crossed exactly the way admin_os_provision.go
// crosses it, and for the same reason: this code creates an EMPTY file. A
// root-side .path unit notices it and runs a fixed, root-owned script. The
// request carries no arguments and its contents are never read, so the only
// thing an unprivileged process — or anything that has compromised it — can
// express is "go". There is no channel through which a web session could
// influence which directives root writes; that list is compiled into the script
// and into HardenBaseline, and both have to be changed by someone with root.
//
// WHAT MAKES THIS DIFFERENT FROM THE PROVISIONING BUTTON
// Provisioning either worked or it did not, and the result file says which. A
// hardening drop-in has a third state that matters more than either: written,
// and not in force, because systemd applies unit directives at exec and this
// process started before the file existed. Reporting that as success would be
// true about the file and false about the machine. So the worker's report is
// never the verdict here — the kernel is, read back on every page load.
//
// The timestamp that separates "not restarted yet" from "written and did not
// take" comes from the DROP-IN, not from that report, and the difference is not
// academic: the worker writes the drop-in, restarts the service, watches for
// twenty seconds, and only then reports. On every successful run the restarted
// process therefore predates the report, so keying on it excused a directive
// that had not taken as one waiting for a restart that had already happened.

import (
	"encoding/json"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/vayuveil"
)

const (
	veilHardenRequestFile = "veilharden.request"
	veilHardenResultFile  = "veilharden.result"
	// veilHardenRequestTTL bounds how long a pending request reads as live. The
	// worker restarts the service, so a request that is still pending well past
	// this is a watcher that never fired — and "in progress" forever is a lie
	// that hides the real problem.
	veilHardenRequestTTL = 5 * time.Minute

	veilHardenWorkerPath = "/usr/local/lib/vayupress/vayuveil-harden.sh"
	veilHardenUnitPath   = "/etc/systemd/system/vayupress-veilharden.path"
)

// veilHardenDropInPath is the file the worker writes and systemd reads at exec.
// A var rather than a const so a test can point it at a temp file — the verdict
// turns on this file's timestamp, and a path no test can reach is a comparison
// no test can check.
var veilHardenDropInPath = "/etc/systemd/system/vayupress.service.d/20-vayuveil-hardening.conf"

// veilHardenResult mirrors the JSON the root worker writes.
type veilHardenResult struct {
	StartedAt  string   `json:"started_at"`
	FinishedAt string   `json:"finished_at"`
	Wrote      []string `json:"wrote"`
	Skipped    []string `json:"skipped"`
	Reverted   bool     `json:"reverted"`
	Failed     bool     `json:"failed"`
	Detail     string   `json:"detail"`
}

// veilHardenUnitsInstalled reports whether the root side exists.
//
// BOTH halves, because either alone is a trap — the same trap admin_os_provision
// documents. The worker script without the .path unit renders an enabled button
// that creates a request nothing consumes and reports "running" until it times
// out: a control that appears to work and does nothing.
func veilHardenUnitsInstalled() bool {
	if _, err := os.Stat(veilHardenWorkerPath); err != nil {
		return false
	}
	_, err := os.Stat(veilHardenUnitPath)
	return err == nil
}

// readVeilHardenState assembles what the unprivileged side can observe.
func readVeilHardenState() vayuveil.HardenState {
	st := vayuveil.HardenState{Installed: veilHardenUnitsInstalled()}

	if fi, err := os.Stat(filepath.Join(provisionStateDir(), veilHardenRequestFile)); err == nil {
		st.Pending = time.Since(fi.ModTime()) < veilHardenRequestTTL
	}

	// The drop-in itself, which is what the verdict turns on. Read separately
	// from the worker's report and BEFORE it, because a missing report does not
	// mean a missing drop-in — a result file lost to a disk wipe would otherwise
	// hide a unit that is still carrying directives.
	if fi, err := os.Stat(veilHardenDropInPath); err == nil {
		st.DropInPresent, st.DropInAt = true, fi.ModTime()
	}

	path := filepath.Join(provisionStateDir(), veilHardenResultFile)
	b, err := os.ReadFile(path) //nolint:gosec // fixed state-dir path, not operator input
	if err != nil {
		return st
	}
	var res veilHardenResult
	if json.Unmarshal(b, &res) != nil {
		return st
	}
	st.HaveResult = true
	st.Wrote, st.Skipped = res.Wrote, res.Skipped
	st.Reverted, st.Failed, st.Detail = res.Reverted, res.Failed, res.Detail
	return st
}

// handleOSVeilHardenRequest asks the root worker to write the hardening drop-in.
//
// Admin-only and CSRF-protected. The work takes no input from here, but it
// restarts the service, and the ability to make a live install bounce on demand
// is not something an anonymous caller should have.
func (a *App) handleOSVeilHardenRequest(w http.ResponseWriter, r *http.Request) {
	if !a.isAdminRequest(r) {
		a.denyAccess(w, r, "/os")
		return
	}
	if !veilHardenUnitsInstalled() {
		writeAPIError(w, r, http.StatusServiceUnavailable, "veilharden-unavailable",
			"The root-side hardening worker is not installed on this server",
			"Run the one-time helper installer shown on this page, then this button works.")
		return
	}
	path := filepath.Join(provisionStateDir(), veilHardenRequestFile)

	// Remove any existing request BEFORE writing a fresh one. The watcher is a
	// .path unit with PathExists=, which fires when the file APPEARS: a stale
	// request that was never consumed leaves the condition permanently true, and
	// no amount of rewriting the same path produces another trigger. Deleting
	// first forces the disappear→appear transition the unit needs.
	rearmed := false
	if _, err := os.Stat(path); err == nil {
		if os.Remove(path) == nil {
			rearmed = true
		}
	}
	// Empty on purpose. The worker never reads the contents, and writing
	// anything here would open a channel from an unprivileged process into a
	// root one — the thing this design exists to avoid.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		writeAPIError(w, r, http.StatusInternalServerError, "veilharden-request-failed",
			"Could not create the hardening request", err.Error())
		return
	}
	dbpkg.AuditLog("vayuveil.harden.request", dbpkg.AuditActor(r), "veil.harden", "requested")
	writeJSON(w, r, http.StatusOK, map[string]any{
		"status": "requested", "rearmed": rearmed,
		"note": "The worker writes the drop-in and restarts the service, so this page will " +
			"disconnect briefly. Reload it afterwards — the verdict is read back from the kernel, " +
			"not from the request.",
	})
}

// veilHardenChip is the accordion's collapsed-state summary.
func veilHardenChip(v vayuveil.HardenVerdict) string {
	switch v {
	case vayuveil.HardenInForce:
		return `<span class="mon-chip mon-chip--on">in force</span>`
	case vayuveil.HardenPending:
		return `<span class="mon-chip mon-chip--off">requested</span>`
	case vayuveil.HardenAwaitingRestart:
		return `<span class="mon-chip mon-chip--off">awaiting restart</span>`
	case vayuveil.HardenDidNotTake:
		return `<span class="mon-chip mon-chip--off">did not take</span>`
	case vayuveil.HardenSkipped:
		return `<span class="mon-chip mon-chip--off">partly skipped</span>`
	case vayuveil.HardenReverted:
		return `<span class="mon-chip mon-chip--off">reverted</span>`
	case vayuveil.HardenFailed:
		return `<span class="mon-chip mon-chip--off">failed</span>`
	case vayuveil.HardenNotRequested:
		return `<span class="mon-chip mon-chip--off">not requested</span>`
	default:
		return `<span class="mon-chip mon-chip--off">unverified</span>`
	}
}

// veilHardenInstallCommand installs the root-side worker and its watcher. It is
// the ceiling the standing rule allows: something that genuinely cannot be done
// without root is shown as an exact, copyable command WITH its reason, on the
// page, rather than as an instruction in a reply.
const veilHardenInstallCommand = "curl -sSL https://raw.githubusercontent.com/johalputt/VayuPress/main/scripts/install-provisioning.sh | sudo bash"

// veilHardenCard renders the request-and-verify surface.
//
// Pure: it takes the state rather than reading it, so the page can be rendered
// and asserted on for every verdict — including the ones a test host cannot
// produce, which is most of them.
func veilHardenCard(st vayuveil.HardenState, sb vayuveil.SandboxState, processStart time.Time) string {
	esc := html.EscapeString
	v := vayuveil.ReconcileHardening(st, sb, processStart)
	missing := vayuveil.UnverifiedHardening(sb)

	var b strings.Builder
	b.WriteString(`<div class="card"><div class="settings-block-title">What this asks for</div>`)
	b.WriteString(`<p class="text-sm muted">` + esc(vayuveil.DescribeHardenVerdict(v, missing)) + `</p>`)

	// The baseline, every row saying where its verification comes from. A row
	// that claims a control should be able to say how it knows.
	b.WriteString(`<div class="table-wrap"><table class="table"><thead><tr><th>Directive</th>` +
		`<th>What it denies</th><th>Read back from</th><th>Now</th></tr></thead><tbody>`)
	for _, d := range vayuveil.HardenBaseline() {
		on, known := d.InForce(sb)
		state := `<span class="mon-chip mon-chip--off">unverified</span>`
		switch {
		case known && on:
			state = `<span class="mon-chip mon-chip--on">in force</span>`
		case known:
			state = `<span class="mon-chip mon-chip--off">not in force</span>`
		}
		b.WriteString(`<tr><td class="text-xs mono">` + esc(d.Directive) + `</td><td class="text-xs muted">` +
			esc(d.Denies) + `</td><td class="text-xs mono muted">` + esc(d.ReadBack) + `</td><td>` +
			state + `</td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)

	// What the last run actually did. Skips are rendered with their reasons,
	// because a worker that quietly left a directive out and reported success is
	// the same defect as a probe that skips what it cannot do.
	if st.HaveResult {
		b.WriteString(`<div class="settings-block-title">The last run</div>`)
		if len(st.Wrote) > 0 {
			b.WriteString(`<p class="text-sm muted">Written into the drop-in: <span class="mono">` +
				esc(strings.Join(st.Wrote, ", ")) + `</span></p>`)
		} else {
			b.WriteString(`<p class="text-sm muted">The run wrote nothing into the drop-in.</p>`)
		}
		if len(st.Skipped) > 0 {
			b.WriteString(`<p class="text-sm muted">Left out, with the reason:</p><ul class="text-sm muted">`)
			for _, s := range st.Skipped {
				b.WriteString(`<li>` + esc(s) + `</li>`)
			}
			b.WriteString(`</ul>`)
		}
		if st.Detail != "" {
			b.WriteString(`<p class="text-xs muted mono">` + esc(st.Detail) + `</p>`)
		}
	}

	// What is deliberately NOT written, and why. This is the half of the page
	// that makes the other half worth believing.
	b.WriteString(`<div class="settings-block-title">What this will not write, and why</div>` +
		`<p class="text-sm muted">Systemd offers dozens more hardening directives and several of them ` +
		`would read well in a release note. Each one below is excluded for a reason that survives being ` +
		`said out loud — either this process cannot read it back, so it could be reported as applied ` +
		`and never as verified, or writing it blind can take a live install down at its next restart.</p>` +
		`<ul class="text-sm muted">`)
	for _, ref := range vayuveil.HardenRefusals() {
		b.WriteString(`<li><span class="mono">` + esc(ref.Directive) + `</span> — ` + esc(ref.Reason) + `</li>`)
	}
	b.WriteString(`</ul>`)

	// The action, or the one command that genuinely needs root.
	switch {
	case !st.Installed:
		// The daily sweep is named FIRST, and deliberately. It is how this
		// arrives on an install that is already running: the provisioning worker
		// upgrades its own helpers from the signed release bundle every day and
		// now writes this watcher too. Leading with the command would send an
		// operator to a terminal for something already on its way — the exact
		// failure this project has a standing rule about, in its politest form.
		b.WriteString(`<p class="text-sm muted">The root-side worker is not installed here <b>yet</b>. ` +
			`If subdomain provisioning is set up on this server, the daily sweep installs it on its ` +
			`own from the signed release bundle, with no terminal use at all — this page will then ` +
			`show the button instead of this paragraph.</p>` +
			`<p class="text-sm muted"><b>Allow up to two daily sweeps</b>, and the reason is worth ` +
			`knowing rather than rounding off: the sweep upgrades its own driver, and the upgraded ` +
			`driver only takes effect on the following run. So the first sweep delivers the worker ` +
			`and the second one installs its watcher.</p>` +
			`<p class="text-sm muted">Installing a <code>systemd</code> unit needs root and this ` +
			`service deliberately cannot become root, which is itself one of the controls above. So if ` +
			`provisioning is not set up, or you would rather not wait for the sweep, this one command ` +
			`does it now and touches neither the binary nor the database:</p>` +
			`<div class="vm-row"><code class="mono text-xs vm-pgp__wkd" data-veilharden-cmd>` +
			esc(veilHardenInstallCommand) + `</code>` +
			`<button type="button" class="btn btn--sm" data-veilharden-copy>Copy</button></div>`)
	case len(missing) == 0:
		b.WriteString(`<p class="text-sm muted">Every directive in the baseline is already in force for ` +
			`this process, so there is nothing to request. The button is not shown rather than shown ` +
			`and inert.</p>`)
	default:
		b.WriteString(`<p class="text-sm muted"><b>The service restarts to apply this.</b> Systemd ` +
			`applies unit directives at exec, so a drop-in written under a running process does nothing ` +
			`until it starts again — which means a button that wrote the file and stopped there would ` +
			`report a control that does not exist. The restart is brief, and if the service does not ` +
			`come back the worker <b>removes the drop-in and restarts it without one</b>: a hardening ` +
			`button that can lock you out of your own panel is worse than the exposure it closes.</p>` +
			`<p class="text-sm muted"><span class="mono">MemorySwapMax=0</span> is worth understanding ` +
			`before you click. It forbids the kernel from paging this service out at all, which is what ` +
			`keeps decrypted mail and the keystore key off the disk — and it means that under real ` +
			`memory pressure the service is killed rather than swapped. That is the trade, stated ` +
			`rather than buried.</p>` +
			`<div class="vm-row"><button type="button" class="btn btn--primary btn--sm" ` +
			`data-veilharden-run>Request hardening and restart</button>` +
			`<span class="text-sm muted" data-veilharden-status></span></div>`)
	}

	b.WriteString(`<p class="text-xs muted">Requesting a run creates an empty flag file that a ` +
		`root-side service watches. No argument is passed and its contents are never read, so this ` +
		`console can ask for hardening and cannot influence which directives root writes.</p></div>`)

	return monAcc("🧱", "Unit hardening", "Ask root for the directives this process can verify afterwards",
		veilHardenChip(v), v != vayuveil.HardenInForce, b.String())
}
