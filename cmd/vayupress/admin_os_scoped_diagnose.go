// SPDX-License-Identifier: Apache-2.0

package main

// admin_os_scoped_diagnose.go — why this site has no certificate, answered on
// the page (ADR-0154 D11).
//
// This exists because of a rule the operator had to state twice: a fix or a
// diagnosis an operator cannot reach from VayuOS has not shipped. Diagnosing
// their stuck certificate took four
// rounds of "run this and paste the output" — `nginx -t`, `provision.log`,
// `systemctl status`, `vayupress domains list`. Every one of those facts is
// available to this process. Asking a person to fetch something the console can
// read is the same failure as a control that does nothing: the page knows and
// does not say.
//
// So the console runs the checks itself and prints the verdict. The one thing it
// cannot do is BE root — but it can tell you, precisely, whether the root-side
// half is installed, when it last ran, what it said, and whether the registry
// read that broke on this very install would succeed today.

import (
	"bufio"
	"context"
	"html"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/domain"
)

// provisionLogTail returns the last n lines of the root worker's log.
//
// Read-only, best effort, and bounded: the file is capped at 2000 lines by the
// worker, and a console page must never be the thing that reads an unbounded
// file into memory.
func provisionLogTail(n int) []string {
	f, err := os.Open(filepath.Clean(filepath.Join(provisionStateDir(), "provision.log")))
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
	ring := make([]string, 0, n)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" {
			continue
		}
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, line)
	}
	return ring
}

// diagCheck is one thing the console can determine for itself.
type diagCheck struct {
	Label string
	OK    bool
	// Fatal marks a check whose failure explains everything below it, so the
	// page can lead with the one that matters instead of a list of six.
	Fatal  bool
	Detail string
}

// diagnoseCertificate answers "why has this site no certificate" from inside the
// process, in the order the root helper would hit each condition.
//
// It reports what it CHECKED, not just a verdict. A diagnostic that says "looks
// fine" without naming what it looked at is the kind an operator stops trusting
// the first time it is wrong.
func (a *App) diagnoseCertificate(ctx context.Context, d domain.Domain) []diagCheck {
	var out []diagCheck

	// 1. The root-side half. Nothing below matters if this is missing, and it is
	//    the one thing this process genuinely cannot install for itself.
	installed := provisionUnitsInstalled()
	out = append(out, diagCheck{
		Label: "Root-side helper installed", OK: installed, Fatal: !installed,
		Detail: map[bool]string{
			true:  "the worker script and its systemd units are present",
			false: "the worker script or its systemd units are missing, so a provisioning request is never consumed",
		}[installed],
	})

	// 2. Would the helper's own registry read see this domain? This is the check
	//    that would have found the real fault in one glance: the helper runs
	//    `vayupress domains hosts`, and on at least one install that command
	//    exited fatal on a missing API_KEY and reported an empty list. Here the
	//    same filter runs in-process, where it cannot fail for that reason.
	approved := !d.IsPrimary && d.IsSyncApproved() && d.Status == domain.StatusActive
	reason := "this site is approved and active, so the helper's host list includes it"
	switch {
	case d.IsPrimary:
		reason = "the primary's certificate is managed outside the registry"
	case d.Status != domain.StatusActive:
		reason = "this site is disabled, and the helper skips disabled sites — enable it under Lifecycle"
	case !d.IsSyncApproved():
		reason = "this site is on manual hold, so the helper skips it by design — approve it under Lifecycle"
	}
	out = append(out, diagCheck{Label: "Listed for provisioning", OK: approved, Fatal: !approved, Detail: reason})

	// 3. Does the name resolve? certbot's HTTP-01 challenge cannot validate a
	//    host that does not answer, and the helper skips it rather than burning
	//    a rate limit.
	resolves := hostResolves(ctx, d.Host)
	out = append(out, diagCheck{
		Label: "DNS answers for " + d.Host, OK: resolves, Fatal: !resolves,
		Detail: map[bool]string{
			true:  "the name resolves, so the challenge can reach this server",
			false: "the name does not resolve yet — point it at this server on Domains & DNS, then run again",
		}[resolves],
	})

	// 4. What the last run actually did. Reported rather than summarised: the
	//    per-helper detail string is the thing that names which one skipped.
	if res, have := readProvisionResult(); have {
		ok := res.Failed == 0 && res.Ran > 0
		out = append(out, diagCheck{
			Label: "Last run " + res.FinishedAt, OK: ok,
			Detail: strconv.Itoa(res.Ran) + " helper(s) did work, " + strconv.Itoa(res.Skipped) +
				" had nothing to do, " + strconv.Itoa(res.Failed) + " reported a problem — " + res.Details,
		})
	} else {
		out = append(out, diagCheck{
			Label: "Last run", OK: false,
			Detail: "no run has ever completed on this install",
		})
	}
	return out
}

// hostResolves is a bounded lookup used only for the diagnostic.
func hostResolves(ctx context.Context, host string) bool {
	h := strings.TrimSpace(strings.ToLower(host))
	if h == "" || isPendingTorSite(h) {
		return false
	}
	addrs, ok := boundedLookup(ctx, h)
	return ok && len(addrs) > 0
}

// scopedDiagnosticBody renders the checks, and the worker's own log beneath
// them.
//
// The log is included because it is the artifact that actually answered this on
// a real install, and because a diagnostic an operator cannot verify is one they
// have to take on trust. It is shown verbatim and escaped.
func scopedDiagnosticBody(checks []diagCheck, logLines []string) string {
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString(`<div class="card">`)
	b.WriteString(`<div class="settings-block-title">What this console checked</div>`)
	b.WriteString(`<p class="text-sm muted">Run here, now, against this install — not a description of what ` +
		`to go and look at.</p>`)
	b.WriteString(`<div class="table-wrap"><table class="table"><tbody>`)
	for _, c := range checks {
		badge := `<span class="badge badge--ok">ok</span>`
		if !c.OK {
			badge = `<span class="badge badge--muted">no</span>`
			if c.Fatal {
				badge = `<span class="badge badge--warn">blocking</span>`
			}
		}
		b.WriteString(`<tr><td>` + badge + `</td><td><b>` + esc(c.Label) + `</b>` +
			`<div class="text-xs muted">` + esc(c.Detail) + `</div></td></tr>`)
	}
	b.WriteString(`</tbody></table></div>`)

	if len(logLines) > 0 {
		b.WriteString(`<div class="settings-block-title">The provisioning log, as written</div>`)
		b.WriteString(`<p class="text-xs muted">The last ` + strconv.Itoa(len(logLines)) +
			` lines the root-side helper wrote. Shown verbatim so nothing is being summarised at you.</p>`)
		b.WriteString(`<pre class="mono text-xs vm-logbox">` + esc(strings.Join(logLines, "\n")) + `</pre>`)
	} else {
		b.WriteString(`<p class="text-sm muted">The provisioning log is empty or unreadable from this ` +
			`process, which usually means the helper has never run.</p>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// boundedLookup resolves a host under a short deadline. A console page must
// never wait on a resolver: a timeout here reports "cannot tell", never "wrong".
func boundedLookup(ctx context.Context, host string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var r net.Resolver
	addrs, err := r.LookupHost(ctx, host)
	if err != nil {
		return nil, false
	}
	return addrs, true
}
