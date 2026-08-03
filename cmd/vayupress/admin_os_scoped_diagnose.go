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

	"github.com/johalputt/vayupress/internal/config"
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

	// 3. Where does the name actually point?
	//
	// The first version asked only whether it RESOLVED, and reported "so the
	// challenge can reach this server" — which does not follow. A domain pointed
	// at somebody else's server resolves perfectly, and its HTTP-01 challenge is
	// answered by whatever is actually there, with a 404. That is not
	// hypothetical: an install had a domain registered here pointing at a
	// static-hosting provider, and every run asked Let's Encrypt for a
	// certificate for a site this server does not serve.
	//
	// The primary's addresses come along because "not one of ours" is not the
	// same statement as "not us" — see dnsPointsHereCheck for why that distinction
	// is the whole check.
	addrs, looked := boundedLookup(ctx, d.Host)
	apexAddrs, _ := boundedLookup(ctx, strings.TrimSpace(config.Cfg.Domain))
	out = append(out, dnsPointsHereCheck(d.Host, addrs, looked, localAddrSet(), apexAddrs))

	// 4. Are the root-side helpers as new as this binary?
	//
	// The in-app updater swaps the BINARY ONLY — it runs unprivileged and cannot
	// write to /usr/local/lib/vayupress. So an install can be fully up to date
	// and still be running month-old shell helpers, which is invisible from
	// every version number on every page. That is exactly what happened here:
	// the binary carried the fix for the failure, the helper that trips over it
	// did not, and nothing said the two could differ.
	// NOT Fatal. Stale helpers degrade REPORTING — they cannot tell a
	// broken-nginx abort from a clean run — but they do not stop a certificate
	// being issued. Marking this "blocking" sent an operator to fix the thing
	// that was not stopping them, which is the same defect as a panel row
	// overstating what is enforcing, committed by the check written to find
	// exactly that. Shown, and shown as secondary.
	fresh, why := provisionHelpersCurrent()
	out = append(out, diagCheck{
		Label: "Root-side helpers up to date", OK: fresh, Fatal: false,
		Detail: why + map[bool]string{
			true:  "",
			false: " This does NOT stop a certificate being issued — it makes the run's own report unreliable.",
		}[fresh],
	})

	// 5. Did the worker RUN, and did it record what it did?
	//
	// The console has both files and was comparing neither. The worker appends to
	// provision.log throughout and writes provision.result only at the very end,
	// so their timestamps answer a question nothing else can: a log newer than
	// the result means a run STARTED and did not finish recording — the process
	// died, was killed by its start timeout, or exited early — and every number
	// below it is from an older run.
	//
	// Without this the page shows "Last run <days ago>" beside a worker that has
	// been executing all along, and an operator pressing the button watches
	// nothing change with no way to tell the difference between "it never ran"
	// and "it ran and told you nothing".
	logAt, haveLog := provisionFileTime("provision.log")
	resAt, haveRes := provisionFileTime(provisionResultFile)
	out = append(out, workerTraceCheck(logAt, haveLog, resAt, haveRes))

	// 6. Was the last run AFTER the most recent request?
	//
	// The first version of this check called a run "ok" on Failed==0 && Ran>0
	// without looking at WHEN it happened — so a healthy run from yesterday was
	// reported as a pass while the request made thirty seconds ago sat
	// unconsumed. A stale success displayed as a current one is worse than no
	// check: it sends the operator to look somewhere else.
	res, have := readProvisionResult()
	if reqAt, pending := provisionRequestAt(); pending {
		consumed := have && runFinishedAfter(res, reqAt)
		out = append(out, diagCheck{
			Label: "Your provisioning request was picked up", OK: consumed, Fatal: !consumed,
			Detail: map[bool]string{
				true: "a run started after you asked for one",
				false: "a request is waiting and no run has started since. The root-side watcher " +
					"is not consuming it — most often because the helpers above are stale or the " +
					"service failed its last start",
			}[consumed],
		})
	}

	// 7. What the last run actually did. Reported rather than summarised: the
	//    per-helper detail string is the thing that names which one skipped.
	if have {
		ok := res.Failed == 0 && res.Ran > 0
		label := "Last run " + res.FinishedAt
		age := ""
		if t, err := time.Parse(time.RFC3339, res.FinishedAt); err == nil {
			// The age, always — not only when it is old. A reader in any timezone
			// can act on "16 hours ago"; a bare Z timestamp they have to convert
			// is where this went wrong.
			label = "Last run " + stamp(t)
			if time.Since(t) > 24*time.Hour {
				age = " This is not a report on anything you just did."
				ok = false
			}
		}
		out = append(out, diagCheck{
			Label: label, OK: ok,
			Detail: strconv.Itoa(res.Ran) + " helper(s) did work, " + strconv.Itoa(res.Skipped) +
				" had nothing to do, " + strconv.Itoa(res.Failed) + " reported a problem — " +
				res.Details + age,
		})
	} else {
		out = append(out, diagCheck{
			Label: "Last run", OK: false,
			Detail: "no run has ever completed on this install",
		})
	}
	return out
}

// humanAge renders how long ago something happened, beside the timestamp.
//
// This page printed bare UTC with a Z, and on a server in IST that reads as
// YESTERDAY for a run that happened after midnight TODAY — a 5½ hour offset that
// crosses the date line. It cost a real misdiagnosis: a run eight milliseconds
// from its own result, on a healthy worker, was read as days-stale by everyone
// who looked at it. A timestamp nobody can convert in their head is a number the
// reader has to trust, and this page exists to stop asking for trust.
//
// The absolute time stays — it is what matches the server's own files — with the
// age beside it, because an age cannot be misread across a timezone.
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "seconds ago"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " minutes ago"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " hours ago"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " days ago"
	}
}

// stamp renders a moment as the server writes it, plus how long ago that was.
func stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339) + " (" + humanAge(t) + ")"
}

// dnsPointsHereCheck is check #3, separated from the lookup so the verdict can
// be tested against every combination of answer without a resolver.
//
// It is a pure function on purpose: the branch that matters most — a name
// pointed at a DIFFERENT HOST — is the one a live DNS query in a test can never
// be relied on to produce.
//
// FINDING, and the reason this takes the primary's addresses as well. The first
// version asked only "is this address one of ours" and called everything else
// blocking. That contradicted the DNS page of the same product, which had
// already reasoned the case through and says, in as many words, that a name
// resolving somewhere this machine cannot prove it holds is "normal behind NAT,
// and not a fault". It is also wrong on any CDN-fronted install, where every
// hosted name resolves to the front rather than the origin and HTTP-01 still
// validates because the front passes the challenge path through. Two pages of
// one product returning opposite verdicts on the same fact is worse than either
// verdict alone.
//
// So the comparison the DNS page uses to RECOGNISE A PROXY is used here for the
// same purpose: an address shared with the primary is this install's own front,
// whether that front is a CDN or a NAT router. An address the primary never
// uses is a different host altogether — which is the case worth being loud
// about, because failed validations are rate-limited PER ACCOUNT and one domain
// that can never validate spends the budget every other site here needs.
func dnsPointsHereCheck(host string, addrs []string, looked bool, local map[string]bool, apexAddrs []string) diagCheck {
	holds := func(set map[string]bool) bool {
		for _, a := range addrs {
			if set[a] {
				return true
			}
		}
		return false
	}
	apex := map[string]bool{}
	for _, a := range apexAddrs {
		apex[a] = true
	}
	switch {
	case !looked || len(addrs) == 0:
		return diagCheck{
			Label: "DNS answers for " + host, OK: false, Fatal: true,
			Detail: "the name does not resolve yet — point it at this server on Domains & DNS, then run again",
		}
	case holds(local):
		return diagCheck{
			Label: "DNS points at this server", OK: true,
			Detail: "it resolves to an address this machine holds, so the challenge reaches here",
		}
	case len(local) == 0 || len(apex) == 0:
		// Nothing is claimed, in either direction. A process that cannot enumerate
		// its own interfaces — or could not resolve its own primary domain to
		// compare against — has no basis for calling the record wrong, and saying
		// it anyway is the failure this whole page is a correction for.
		return diagCheck{
			Label: "DNS answers for " + host, OK: true,
			Detail: "it resolves to " + strings.Join(addrs, ", ") + "; this process has nothing " +
				"trustworthy to compare that against, so nothing is claimed either way",
		}
	case holds(apex):
		return diagCheck{
			Label: "DNS points at the same front as this install", OK: true,
			Detail: "it resolves to " + strings.Join(addrs, ", ") + ", the same address the primary " +
				"domain uses — a proxy or a NAT router in front of this server, not a different host. " +
				"The challenge reaches here provided that front passes /.well-known/acme-challenge/ " +
				"through rather than answering it or presenting a bot check.",
		}
	default:
		return diagCheck{
			Label: "DNS points at a different host", OK: false, Fatal: true,
			Detail: host + " resolves to " + strings.Join(addrs, ", ") + " — an address this machine " +
				"does not hold AND one the primary domain does not use either, so it is not this " +
				"install behind a proxy. Whatever is actually at that address answers the challenge, " +
				"with a 404, and a certificate can never be issued from here. Failed validations are " +
				"rate-limited PER ACCOUNT, so a domain that can never validate spends the budget every " +
				"other site on this install needs — put it on hold under Lifecycle, or repoint it.",
		}
	}
}

// provisionRunActiveFor is how recently the worker must have written to its log
// for a run to count as still underway rather than dead.
//
// The unit allows 900s before systemd kills it, and certbot across several new
// domains genuinely takes minutes, so a short window here would call a working
// run a failed one — which is the mistake this constant exists to stop repeating.
const provisionRunActiveFor = 10 * time.Minute

// workerTraceCheck compares the worker's log against its recorded result.
//
// A log newer than the result means a run STARTED and did not finish recording:
// the process died, was killed by its start timeout, or exited early. Every
// number reported below it is then from an older run, and an operator pressing
// the button watches nothing change with no way to tell "it never ran" from "it
// ran and told you nothing".
//
// The two-minute margin is the ordinary gap between a worker's last log line and
// the result it writes moments later; anything beyond that is a run that did not
// come back.
func workerTraceCheck(logAt time.Time, haveLog bool, resAt time.Time, haveRes bool) diagCheck {
	switch {
	case !haveLog:
		return diagCheck{
			Label: "The worker has left a trace", OK: false, Fatal: true,
			Detail: "there is no provisioning log at all, so the root-side worker has never run here",
		}
	// A log newer than the result has TWO explanations, and the first version of
	// this check asserted the alarming one. A worker still executing writes its
	// log throughout and records nothing until it finishes — which is exactly
	// what a run in progress looks like, and certbot across several new domains
	// takes minutes. Calling that "running and dying" tells an operator their
	// working install is broken, on the strength of a timestamp that says the
	// opposite.
	//
	// So the two are separated by whether the log is STILL MOVING.
	case haveRes && logAt.Sub(resAt) > 2*time.Minute && time.Since(logAt) < provisionRunActiveFor:
		return diagCheck{
			Label: "A run is in progress", OK: true, Fatal: false,
			Detail: "the worker wrote to its log " + humanAge(logAt) + " and has not recorded a " +
				"result yet, which is what a run underway looks like — it records only when it " +
				"finishes, and certbot across several new domains takes minutes. The numbers " +
				"below are still from the previous run until this one lands. Reload shortly.",
		}
	case haveRes && logAt.Sub(resAt) > 2*time.Minute:
		return diagCheck{
			Label: "The last run recorded what it did", OK: false, Fatal: true,
			Detail: "the worker WROTE TO ITS LOG at " + stamp(logAt) +
				" but last recorded a result at " + stamp(resAt) +
				", and has been quiet since. It started and stopped before recording anything — " +
				"so every number below is from an older run. The log above is the only account " +
				"of what that run did.",
		}
	default:
		return diagCheck{
			Label: "The worker last wrote its log " + stamp(logAt), OK: true,
			Detail: "the log and the recorded result agree, so the report below is that run's own",
		}
	}
}

// provisionFileTime returns a provisioning state file's modification time.
func provisionFileTime(name string) (time.Time, bool) {
	fi, err := os.Stat(filepath.Clean(filepath.Join(provisionStateDir(), name)))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// provisionRequestAt returns when the outstanding request was made.
func provisionRequestAt() (time.Time, bool) {
	fi, err := os.Stat(filepath.Clean(filepath.Join(provisionStateDir(), provisionRequestFile)))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// runFinishedAfter reports whether a recorded run finished after a given moment.
// An unparseable timestamp is treated as NOT after: claiming a request was
// consumed on the strength of a date we cannot read is the kind of guess this
// page exists to stop making.
func runFinishedAfter(res provisionResult, t time.Time) bool {
	fin, err := time.Parse(time.RFC3339, res.FinishedAt)
	if err != nil {
		return false
	}
	return fin.After(t)
}

// provisionHelpersCurrent reports whether the root-side shell helpers carry the
// fixes this binary expects.
//
// It checks for a marker string rather than a version, because the helpers carry
// no version and adding one would not help an install that already has the old
// copy. The marker is the classification the driver gained when it stopped
// reporting a run that did nothing as a clean run — the single most useful
// behaviour to know the presence of, since without it every number on this page
// comes from a report that cannot distinguish success from silence.
func provisionHelpersCurrent() (bool, string) {
	b, err := os.ReadFile(filepath.Clean(provisionDriverPath))
	if err != nil {
		// UNKNOWN, reported as not-current. Reporting "up to date" for a file we
		// could not open would be the same unearned reassurance the driver's own
		// "nothing to do" was — a verdict asserted on the absence of evidence.
		return false, "the root-side driver could not be read, so whether it carries the " +
			"current fixes is unknown — this page will not call that up to date"
	}
	return driverCarriesReportingFixes(string(b))
}

// provisionDriverPath is a var rather than a const so a test can reach the
// unreadable branch, which is the one that must never report "current".
var provisionDriverPath = "/usr/local/lib/vayupress/provision-subdomains.sh"

// driverCarriesReportingFixes is the check itself, over the driver's text.
func driverCarriesReportingFixes(src string) (bool, string) {
	missing := []string{}
	if !strings.Contains(src, "ALREADY invalid") {
		missing = append(missing, "it cannot tell a broken-nginx abort from a clean run")
	}
	if !strings.Contains(src, "nothing to provision") {
		missing = append(missing, "it counts a helper that did nothing as one that did work")
	}
	if len(missing) == 0 {
		return true, "the driver carries the current reporting fixes"
	}
	return false, "the shell helpers are OLDER than this binary — " + strings.Join(missing, ", ") +
		". Updating from this console swaps the binary only; it runs unprivileged and cannot " +
		"write to /usr/local/lib/vayupress. Re-run the provisioning installer once to refresh them."
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
	// No heading of its own: this body is the inside of an accordion whose summary
	// already says what it is, and the summary stays visible when it is collapsed.
	b.WriteString(`<div class="card">`)
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
