// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_recheck.go — running the certificate checks again, on demand.
//
// # The gap this closes
//
// The diagnostic told an operator to fix their DNS and then said "run again" —
// and offered nothing to press. There was no re-check control anywhere on the
// page, so the only ways to re-evaluate were to reload the browser or to press
// "Provision now", which requests a whole root-side provisioning run to answer
// a question a DNS lookup answers in milliseconds.
//
// An operator hit exactly that: they pointed the domain at the server, the
// console kept reporting the old answer, and with no way to make it look again
// they deleted the domain and re-added it. That worked, and it should never
// have been the step. Deleting a domain to refresh a status line is the panel
// admitting it has no refresh.
//
// # Why the answer can still be "does not resolve" straight after a fix
//
// boundedLookup does no caching of its own — it is a fresh resolver call on
// every render. The staleness is the MACHINE's: a negative DNS answer is cached
// by the system resolver for the zone's SOA minimum, commonly several minutes,
// and nothing in this process can invalidate somebody else's cache.
//
// So this control does the honest thing rather than the impressive one. It
// re-runs every check immediately, stamps when it ran, and — when DNS is still
// the blocker — says that a change made in the last few minutes may not be
// visible here yet and why. A button that silently returned the same cached
// answer would teach an operator that the button does not work.

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/render"
)

// scopedDiagnosticPanel wraps the checks in the element the re-check swaps.
//
// The wrapper carries the id and the HTMX attributes so the fragment returned
// by the endpoint is byte-identical to the one rendered inline — a fragment
// that differs from its first render is a swap that visibly changes the page
// for no reason the operator asked for.
func scopedDiagnosticPanel(domainID string, checks []diagCheck, logLines []string, host string, at time.Time) string {
	esc := html.EscapeString
	stamp := at.UTC().Format("15:04:05") + " UTC"

	// Whether DNS is the blocker decides one sentence, and it is the sentence
	// the operator in the report needed.
	dnsBlocking := false
	for _, c := range checks {
		if !c.OK && c.Fatal && isDNSCheck(c.Label) {
			dnsBlocking = true
			break
		}
	}
	note := ""
	if dnsBlocking {
		note = `<p class="text-xs muted">This machine's resolver caches a “does not exist” answer for a
    few minutes, so a DNS record you have just pointed here may not show up on the next re-check.
    Nothing in this console can clear that cache — if the record is correct, wait a moment and press
    re-check again rather than changing anything else.</p>`
	}

	return `<div id="` + scopedDiagnosticPanelID + `" class="vm-recheck">
  <div class="vm-row">
    <button type="button" class="btn btn--ghost btn--sm"
      hx-get="/os/d/` + esc(domainID) + `/diagnose/live"
      hx-target="#` + scopedDiagnosticPanelID + `" hx-swap="outerHTML">Re-check now</button>
    <span class="text-xs muted">Checked at ` + esc(stamp) + `</span>
  </div>
  ` + note + scopedDiagnosticBody(checks, logLines, host) + `
</div>`
}

// isDNSCheck identifies the resolver row without matching a neighbour.
//
// Keyed on the label that row actually renders ("DNS answers for <host>"), not
// on the presence of "DNS" anywhere in a label. A mutation proved the looser
// version indistinguishable under the first version of the test: any row whose
// wording mentions DNS — advice, a provider note, a future check — would have
// been mistaken for the resolver result and shown the resolver-cache caveat on
// a page where DNS was fine.
//
// An assertion that cannot say WHICH row it matched is not an assertion.
func isDNSCheck(label string) bool {
	return strings.HasPrefix(label, "DNS answers")
}

// handleOSScopedDiagnoseLive re-runs the checks and returns the panel fragment.
//
// GET, because it is a read: it resolves a name, reads the provisioning log and
// probes this server's own challenge path. Nothing here changes state, so it
// carries no CSRF token and can be pressed as often as an operator likes.
func (a *App) handleOSScopedDiagnoseLive(w http.ResponseWriter, r *http.Request) {
	d, ok := osScopedDomain(r)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var checks []diagCheck
	var logLines []string
	if scopedNeedsCertificate(d) {
		logLines = provisionLogTail(provisionLogLines)
		checks = a.diagnoseCertificate(r.Context(), d, logLines)
	}
	_ = render.CSPNonce(r) // the fragment carries no script; keep the nonce path warm
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(scopedDiagnosticPanel(d.ID, checks, logLines, d.Host, time.Now())))
}

// scopedDiagnosticPanelID is the swap target, shared by the inline render and
// the endpoint so the two cannot drift apart.
const scopedDiagnosticPanelID = "scoped-diagnostic"
