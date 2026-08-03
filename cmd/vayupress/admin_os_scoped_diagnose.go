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
	"crypto/rand"
	"encoding/hex"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/johalputt/vayupress/internal/config"
	"github.com/johalputt/vayupress/internal/domain"
)

// provisionLogLines is how much of the worker's log the console keeps.
//
// It was 25, which is the tail of whatever ran LAST — and a run covers five
// helpers across every hosted domain. On a real install those 25 lines were
// entirely one domain's certbot failure, while the console page they appeared on
// was about a different domain whose own output had scrolled away hours earlier.
// The log is kept long enough to segment by host and the segment is what gets
// shown; the raw tail is only the fallback.
const provisionLogLines = 400

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
		line := strings.TrimRight(stripANSI(sc.Text()), " \t\r")
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

// stripANSI removes terminal colour escapes from a log line.
//
// The helpers colour their output for a human at a terminal, and the console was
// rendering those bytes literally — an operator read `[1;33m  certbot could not
// issue a cert for …  [0m` on the page. Escaped, so harmless, and still noise in
// the middle of the one artifact this page exists to show plainly.
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Skip ESC, an optional '[', then parameter bytes, to the final letter.
		i++
		if i < len(s) && s[i] == '[' {
			i++
		}
		for i < len(s) && (s[i] == ';' || (s[i] >= '0' && s[i] <= '9')) {
			i++
		}
		if i < len(s) {
			i++ // the final byte of the sequence
		}
	}
	return b.String()
}

// hostLogSegment returns the lines the worker wrote about ONE host during its
// most recent attempt at it.
//
// This is the difference between a log and an answer. The console showed the
// tail of a multi-domain run, so the page for a site whose certificate was
// failing displayed, verbatim and in full, a DIFFERENT site's certbot error —
// with nothing saying it was a different site. An operator reading that is being
// actively misled by the artifact included to stop them being misled.
func hostLogSegment(lines []string, host string) []string {
	marker := "Provisioning " + host
	start := -1
	for i, l := range lines {
		if strings.Contains(l, marker) {
			start = i // last attempt wins
		}
	}
	if start < 0 {
		return nil
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		// The next host's section, or the next helper's. A host whose name is a
		// SUFFIX of another cannot be confused for it: the marker requires
		// "Provisioning " immediately before the name.
		if strings.Contains(lines[i], "Provisioning ") && !strings.Contains(lines[i], marker) {
			end = i
			break
		}
	}
	return lines[start:end]
}

// certbotHostVerdict reads what the last run actually did for ONE host.
//
// FINDING, visible on a live install: the panel said "5 helper(s) did work, 0
// reported a problem" beside a log that plainly read "certbot could not issue a
// cert for …". Both came from the same run. The summary counts HELPERS, and
// setup-vayudomain.sh loops over hosts with `continue` on failure and exits 0 —
// so a helper that failed to issue a certificate is indistinguishable, in the
// result file, from one that issued every certificate it was asked for.
//
// The driver could be taught to count host failures, and it has been. That fix
// reaches nobody who already has the bug: the in-app updater swaps the BINARY
// and cannot write to /usr/local/lib/vayupress — which this very console reports
// as out of date on the install this was found on. So the binary reads the log
// and reports the per-host truth itself, which is the half that arrives through
// Update & Backup.
func certbotHostVerdict(seg []string, host string) (diagCheck, bool) {
	var out diagCheck
	found := false
	var reasons []string
	for _, l := range seg {
		t := strings.TrimSpace(l)
		switch {
		case strings.Contains(l, "VayuDomain live: https://"+host):
			out, found = diagCheck{
				Label: "The last run issued this site's certificate", OK: true,
				Detail: "the worker recorded it as live",
			}, true
		case strings.Contains(l, "certbot could not issue a cert for "+host):
			out, found = diagCheck{
				Label: "The certificate authority refused " + host, OK: false, Fatal: true,
			}, true
		case strings.Contains(l, "nginx test failed for "+host):
			out, found = diagCheck{
				Label: "nginx rejected this site's vhost", OK: false, Fatal: true,
				Detail: "the worker wrote the vhost, nginx refused to load it, and the site was " +
					"skipped without a certificate being attempted",
			}, true
		}
		// The authority's own words, which say more than any summary of them.
		if strings.HasPrefix(t, "Detail:") || strings.HasPrefix(t, "Type:") {
			reasons = append(reasons, t)
		}
	}
	if !found {
		return diagCheck{}, false
	}
	if out.Fatal && out.Detail == "" {
		out.Detail = "the run reported a refusal for this host"
		if len(reasons) > 0 {
			out.Detail = strings.Join(reasons, " ")
		}
		out.Detail += ". The summary below counts HELPERS, not hosts, and a helper that fails one " +
			"host still exits cleanly — so this is read from the log rather than from that summary." +
			certbotErrorKind(seg)
	}
	return out, true
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
func (a *App) diagnoseCertificate(ctx context.Context, d domain.Domain, logLines []string) []diagCheck {
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

	// 2. WHAT THE AUTHORITY ITSELF SAID, and whether this server can answer its
	//    own challenge.
	//
	// These lead because they are DIRECT EVIDENCE and everything below them is
	// inference. That ordering was got wrong, at a cost: the console reported a
	// DNS mismatch it had deduced, in a row above the authority's own error —
	// which named a different address and a different failure mode — and the
	// deduction was read as the cause for three releases while the actual
	// message sat further down the same page.
	//
	// A page that has the answer and ranks a guess above it is not a diagnostic.
	seg := hostLogSegment(logLines, d.Host)
	if v, ok := certbotHostVerdict(seg, d.Host); ok {
		out = append(out, v)
	}
	if strings.Contains(certbotErrorKind(seg), "CONNECTION") || len(seg) == 0 {
		// The probe answers exactly the question a connection error raises, and
		// nothing else can. It writes a token and reads it back, so it is only run
		// when there is a reason to.
		vh := vhostCheck(d.Host)
		pr := challengeProbe(ctx, d.Host)
		out = append(out, vh, pr)
		if c, ok := staleConfigCheck(vh, pr); ok {
			out = append(out, c)
		}
	}

	// 3. Would the helper's own registry read see this domain? This is the check
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

	// 4. Where does the name actually point?
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

	// 5. Are the root-side helpers as new as this binary?
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

	// 6. Did the worker RUN, and did it record what it did?
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

	// 7. Was the last run AFTER the most recent request?
	//
	// The first version of this check called a run "ok" on Failed==0 && Ran>0
	// without looking at WHEN it happened — so a healthy run from yesterday was
	// reported as a pass while the request made thirty seconds ago sat
	// unconsumed. A stale success displayed as a current one is worse than no
	// check: it sends the operator to look somewhere else.
	res, have := readProvisionResult()
	requestStuck := false
	if reqAt, pending := provisionRequestAt(); pending {
		consumed := have && runFinishedAfter(res, reqAt)
		requestStuck = !consumed
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

	// 8. What the last run actually did. Reported rather than summarised: the
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

	if c, ok := rootSideStalledCheck(requestStuck, fresh); ok {
		out = append(out, c)
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
		// Held — but not necessarily on every family it publishes. See
		// mismatchedFamily: an AAAA that answers as somebody else fails validation
		// while the A record beside it is perfect, and that failure names neither.
		if stray := mismatchedFamily(addrs, local, apex); stray != "" {
			return diagCheck{
				Label: "DNS points here on one family and elsewhere on the other", OK: false, Fatal: true,
				Detail: host + " resolves to " + strings.Join(addrs, ", ") + ". This machine holds some " +
					"of those but NOT " + stray + ", and the certificate authority validates over IPv6 " +
					"first whenever an AAAA record exists. A connection it cannot open at all is retried " +
					"over IPv4; one that opens and answers as somebody else is not — the failure stands, " +
					"with a perfectly correct A record beside it. Remove or repoint the " + stray +
					" record, or make this server answer on it.",
			}
		}
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
				"with a 404, and a certificate can never be issued from here. It does not block the " +
				"other sites — each gets its own certificate in its own run — but every run reports a " +
				"failure for it forever, which buries a NEW fault among the standing one. Put it on " +
				"hold under Lifecycle, or repoint it.",
		}
	}
}

// mismatchedFamily returns the address family this host publishes that this
// machine does NOT answer on, or "" when there is no such split.
//
// FINDING, and a correction to something this page said one release ago. It
// claimed a domain that cannot validate "spends the budget every other site on
// this install needs", on the strength of Let's Encrypt's per-account rate
// limits. The install's own provisioning script disproves it: each host is
// issued in its own certbot run under its own --cert-name, inside a loop that
// continues past a failure, and the Failed Validation limit is scoped per
// account PER HOSTNAME. A stuck domain is loud, not contagious. Stating a
// mechanism that does not exist is the same defect as a panel row overstating
// what is enforcing — committed, again, by the check written to find it.
//
// The mechanism that IS real is this one. Validation is attempted over IPv6
// first whenever an AAAA exists. An address that refuses the connection outright
// is retried over IPv4; one that ACCEPTS and answers as a different site is not,
// so a stale or wrong AAAA fails the whole issuance while the A record beside it
// is exactly right — and nothing in the resulting error says "IPv6".
func mismatchedFamily(addrs []string, local, apex map[string]bool) string {
	var v4Bad, v6Bad []string
	v4OK, v6OK := false, false
	for _, a := range addrs {
		six := strings.Contains(a, ":")
		switch {
		case local[a] || apex[a]:
			if six {
				v6OK = true
			} else {
				v4OK = true
			}
		case six:
			v6Bad = append(v6Bad, a)
		default:
			v4Bad = append(v4Bad, a)
		}
	}
	// Only a family that is ENTIRELY unaccounted for is reported. One good
	// address in a family is enough for that family to reach here, and naming it
	// anyway would be the cry-wolf failure this check exists to avoid.
	if len(v6Bad) > 0 && !v6OK {
		return "the IPv6 address (" + strings.Join(v6Bad, ", ") + ")"
	}
	if len(v4Bad) > 0 && !v4OK {
		return "the IPv4 address (" + strings.Join(v4Bad, ", ") + ")"
	}
	return ""
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
func scopedDiagnosticBody(checks []diagCheck, logLines []string, host string) string {
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

	if seg := hostLogSegment(logLines, host); len(seg) > 0 {
		b.WriteString(`<div class="settings-block-title">What the worker wrote about ` + esc(host) + `</div>`)
		b.WriteString(`<p class="text-xs muted">` + strconv.Itoa(len(seg)) + ` lines, verbatim, from this ` +
			`site's own section of the last run — not the tail of whatever ran last, which on a ` +
			`multi-domain install is somebody else's failure printed under your domain's heading.</p>`)
		b.WriteString(`<pre class="mono text-xs vm-logbox">` + esc(strings.Join(seg, "\n")) + `</pre>`)
	} else if len(logLines) > 0 {
		tail := logLines
		if len(tail) > 25 {
			tail = tail[len(tail)-25:]
		}
		b.WriteString(`<div class="settings-block-title">The provisioning log, as written</div>`)
		b.WriteString(`<p class="text-xs muted">The last ` + strconv.Itoa(len(tail)) +
			` lines the root-side helper wrote. <b>This run has no section for ` + esc(host) + `</b>, so ` +
			`these lines are about the install as a whole or about other sites — said plainly, because ` +
			`reading another domain's error as your own is exactly the mistake this box caused.</p>`)
		b.WriteString(`<pre class="mono text-xs vm-logbox">` + esc(strings.Join(tail, "\n")) + `</pre>`)
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

// acmeWebroot is where certbot's --webroot writes, and therefore where nginx
// must serve /.well-known/acme-challenge/ from.
func acmeWebroot() string {
	if d := strings.TrimSpace(os.Getenv("CACHE_DIR")); d != "" {
		return d
	}
	return "/var/cache/vayupress"
}

// challengeProbe serves a real token and fetches it back over LOOPBACK with this
// site's Host header.
//
// This is the check that splits the problem in two, and it is the one that was
// missing while three releases went by arguing about DNS. The authority reported
// "Type: connection" for a host whose A record is correct, and from the outside
// that has two completely different causes with the same symptom:
//
//   - nginx is not serving the challenge path for this host, or
//   - nginx is serving it perfectly and nothing from the internet can reach
//     port 80 on this machine.
//
// No amount of reasoning about DNS separates those. Asking the server to answer
// its own challenge does, decisively, without leaving VayuOS and without asking
// anybody to run a command.
func challengeProbe(ctx context.Context, host string) diagCheck {
	dir := filepath.Join(acmeWebroot(), ".well-known", "acme-challenge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return diagCheck{
			Label: "This server can answer its own challenge", OK: true,
			Detail: "the challenge directory (" + dir + ") could not be created from this " +
				"process, so this check was skipped — nothing is claimed either way",
		}
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return diagCheck{Label: "This server can answer its own challenge", OK: true,
			Detail: "the probe token could not be generated, so nothing is claimed either way"}
	}
	// Prefixed so anything that ever sees this file knows what it is. Random, so
	// it cannot collide with a live challenge, and removed immediately below.
	token := "vayupress-selftest-" + hex.EncodeToString(raw[:])
	path := filepath.Join(dir, token)
	if err := os.WriteFile(path, []byte(token), 0o644); err != nil { //nolint:gosec // must be world-readable: nginx serves it
		return diagCheck{
			Label: "This server can answer its own challenge", OK: true,
			Detail: "could not write into " + dir + " (" + err.Error() + "), so this check was " +
				"skipped — note that certbot writes there too, and if it cannot either, that is " +
				"the fault",
		}
	}
	defer func() { _ = os.Remove(path) }()

	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://127.0.0.1/.well-known/acme-challenge/"+token, nil)
	if err != nil {
		return diagCheck{Label: "This server can answer its own challenge", OK: true,
			Detail: "the probe request could not be built, so nothing is claimed either way"}
	}
	// The Host header is the whole point: it selects this site's vhost rather
	// than the default one, which is exactly what the authority's request does.
	req.Host = host
	client := &http.Client{
		Timeout: 4 * time.Second,
		// A redirect means the challenge location did NOT match and the catch-all
		// `return 301 https://…` did. Following it would hide that.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return diagCheck{
			Label: "This server can answer its own challenge", OK: false, Fatal: true,
			Detail: "nothing answered on port 80 over loopback for " + host + " (" + err.Error() +
				"). " + port80Kind() + ". The authority cannot reach a challenge this machine " +
				"will not serve to itself — see the server-block check above for which it is.",
		}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	if resp.StatusCode == http.StatusOK && strings.TrimSpace(string(body)) == token {
		return diagCheck{
			Label: "This server can answer its own challenge", OK: true,
			Detail: "a token written to " + dir + " was served back over loopback for " + host +
				". nginx and the webroot are correct, so a failure the authority reports as a " +
				"CONNECTION problem is between the internet and this machine — port 80 filtered " +
				"upstream, or the address the name resolves to is not this server — and not " +
				"something to fix in nginx.",
		}
	}
	return diagCheck{
		Label: "This server can answer its own challenge", OK: false, Fatal: true,
		Detail: "the loopback request for " + host + " returned HTTP " + strconv.Itoa(resp.StatusCode) +
			" instead of the token. nginx is up but this site's vhost does not serve " +
			"/.well-known/acme-challenge/ from " + acmeWebroot() + " — a redirect here means the " +
			"catch-all matched instead of the challenge location, which no certificate can survive.",
	}
}

// certbotErrorKind explains the authority's own error TYPE.
//
// "Type: connection" and "Type: unauthorized" are entirely different faults with
// the same visible outcome, and the console was printing them raw. Connection
// means nobody answered; unauthorized means something answered with the wrong
// thing. One is a network path, the other is a webroot — and telling them apart
// is most of the work.
func certbotErrorKind(seg []string) string {
	for _, l := range seg {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "Type:") {
			continue
		}
		switch kind := strings.TrimSpace(strings.TrimPrefix(t, "Type:")); kind {
		case "connection":
			return " A CONNECTION error means the authority could not open port 80 to that " +
				"address at all — nothing answered. That is a network path, not a webroot: the " +
				"content it would have found is irrelevant because it never got that far."
		case "unauthorized":
			return " An UNAUTHORIZED error means something DID answer and served the wrong " +
				"thing — so the network path works and the fault is which server, or which " +
				"vhost, is answering for this name."
		case "dns":
			return " A DNS error means the authority could not resolve the name at the moment " +
				"it looked, which is upstream of this server entirely."
		}
	}
	return ""
}

// nginxSitesEnabled is the directory whose contents decide which Host header
// reaches which server block. World-readable, so this unprivileged process can
// read it — which is the whole reason these checks can exist at all.
var nginxSitesEnabled = "/etc/nginx/sites-enabled"

// serverBlocks splits an nginx config into its top-level `server { … }` bodies.
//
// Block-level, not file-level, because one file routinely holds two: an HTTP
// block that carries the ACME challenge and an HTTPS block that does not. Asking
// "does this FILE mention port 80" answers yes for a file whose only port-80
// block belongs to a different hostname — a check meant to end the guessing
// starting a fresh round of it.
func serverBlocks(cfg string) []string {
	var out []string
	for i := 0; i < len(cfg); {
		j := strings.Index(cfg[i:], "server")
		if j < 0 {
			break
		}
		j += i
		k := strings.IndexByte(cfg[j:], '{')
		if k < 0 {
			break
		}
		k += j
		// Only a bare `server` directive opens a block; `server_name` and
		// `server_tokens` must not be mistaken for one.
		if strings.TrimSpace(cfg[j+len("server"):k]) != "" {
			i = j + len("server")
			continue
		}
		depth, end := 0, -1
		for q := k; q < len(cfg) && end < 0; q++ {
			switch cfg[q] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = q
				}
			}
		}
		if end < 0 {
			break
		}
		out = append(out, cfg[k+1:end])
		i = end + 1
	}
	return out
}

// listensOnPort80 reports whether a server block is reachable over plain HTTP.
//
// The port is PARSED, not substring-matched: "listen 8080" contains "80", and
// "listen 443 ssl" must never count. The IPv6 form ("listen [::]:80") carries
// the port after the last colon.
func listensOnPort80(block string) bool {
	for _, line := range strings.Split(block, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "listen") {
			continue
		}
		rest := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(t, "listen")), ";")
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		spec := strings.TrimSuffix(fields[0], ";")
		if i := strings.LastIndex(spec, ":"); i >= 0 {
			spec = spec[i+1:]
		}
		if spec == "80" {
			return true
		}
	}
	return false
}

// vhostState is what the enabled configs actually provide for one host.
type vhostState struct {
	File            string
	Found           bool
	OnPort80        bool
	ServesChallenge bool
}

// enabledVhostFor finds the enabled server blocks claiming this host and reports
// what they can actually do for an HTTP-01 challenge.
func enabledVhostFor(host string) (st vhostState, readable bool) {
	entries, err := os.ReadDir(nginxSitesEnabled)
	if err != nil {
		return st, false
	}
	want := strings.ToLower(strings.TrimSpace(host))
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Clean(filepath.Join(nginxSitesEnabled, e.Name()))) //nolint:gosec // a fixed, world-readable config dir
		if err != nil {
			continue
		}
		for _, blk := range serverBlocks(string(b)) {
			if !serverNameClaims(blk, want) {
				continue
			}
			// Any block naming this host counts as found; the port-80 and
			// challenge facts are ORed across them, because two blocks in one
			// file legitimately split the work between :80 and :443.
			st.Found = true
			if st.File == "" {
				st.File = e.Name()
			}
			if listensOnPort80(blk) {
				st.OnPort80 = true
				if strings.Contains(blk, "acme-challenge") {
					st.ServesChallenge = true
				}
			}
		}
	}
	return st, true
}

// serverNameClaims reports whether a config's server_name directives list this
// host as a WHOLE name.
//
// Whole-name matching, not substring: "johal.in" appears inside
// "test.johal.in", so a substring test would report the apex's vhost as
// covering every subdomain of it and call a missing vhost present — turning the
// one decisive check into a confident wrong answer.
func serverNameClaims(cfg, host string) bool {
	for _, line := range strings.Split(cfg, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "server_name") {
			continue
		}
		t = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(t, "server_name")), ";")
		for _, name := range strings.Fields(t) {
			if strings.EqualFold(strings.TrimSuffix(name, "."), host) {
				return true
			}
		}
	}
	return false
}

// vhostCheck reports whether nginx will route this host's CHALLENGE anywhere.
//
// FINDING, and the console produced it against itself: the first version checked
// only whether some enabled config NAMED the host, and printed "an enabled
// config names this host, so a request carrying it reaches this site" in a row
// directly above a probe reporting that nothing answered. Both were on screen at
// once and they cannot both be true.
//
// The gap was the PORT. A config can name a host and listen only on 443 — which
// is exactly what this helper writes once a certificate exists, and what it
// leaves behind if one is later removed. HTTP-01 arrives on port 80, finds no
// block for that name there, and falls through to the default server, which
// closes the connection. The name check said "present"; the thing that mattered
// was absent.
func vhostCheck(host string) diagCheck {
	st, readable := enabledVhostFor(host)
	switch {
	case !readable:
		return diagCheck{
			Label: "nginx routes this host on port 80", OK: true,
			Detail: nginxSitesEnabled + " could not be read from this process, so nothing is " +
				"claimed either way about which server block answers for this host",
		}
	case !st.Found:
		return diagCheck{
			Label: "nginx has NO server block for " + host, OK: false, Fatal: true,
			Detail: "no enabled config under " + nginxSitesEnabled + " names this host, so every " +
				"request for it falls through to the DEFAULT server — and this install's default " +
				"server answers an unrecognised Host by closing the connection with no response. " +
				"That is precisely what the authority reports as a connection error and what the " +
				"loopback probe sees as an EOF. Press Provision now: the root-side helper writes " +
				"this site's vhost before it asks for a certificate.",
		}
	case !st.OnPort80:
		return diagCheck{
			Label: "This host's server block does NOT listen on port 80", OK: false, Fatal: true,
			Detail: st.File + " names " + host + " but no block for it listens on port 80 — only " +
				"443. The challenge arrives over plain HTTP on port 80, finds no block for this " +
				"name there, and falls through to the default server, which closes the connection " +
				"without a response. That is the EOF below and the authority's connection error, " +
				"from a config that looks present because the NAME is there and the PORT is not.",
		}
	case !st.ServesChallenge:
		return diagCheck{
			Label: "This host's port-80 block does not serve the challenge", OK: false, Fatal: true,
			Detail: st.File + " listens on port 80 for " + host + " but has no " +
				"/.well-known/acme-challenge/ location, so the catch-all redirect to https answers " +
				"the challenge instead. A redirect is not a token, and validation fails with the " +
				"site looking entirely healthy in a browser.",
		}
	default:
		return diagCheck{
			Label: "nginx routes this host's challenge on port 80", OK: true,
			Detail: "an enabled config (" + st.File + ") names this host, listens on port 80 and " +
				"serves /.well-known/acme-challenge/ from the webroot",
		}
	}
}

// port80Kind classifies what is actually on port 80, so the console stops
// asserting "nginx is not listening" about a socket that accepted the
// connection.
//
// FINDING, and it was in a message shipped one release ago: the probe reported
// "nginx is not listening on port 80, or is not running" for an EOF. An EOF
// means the opposite — the connection was ACCEPTED and then closed. Something is
// listening; it declined to answer. Sending an operator to start a running
// service is the same defect as every other overstatement on this page.
func port80Kind() string {
	c, err := net.DialTimeout("tcp", "127.0.0.1:80", 2*time.Second)
	if err != nil {
		return "nothing is listening on port 80 at all (" + err.Error() + "), so nginx is down " +
			"or not bound there"
	}
	_ = c.Close()
	return "something IS listening on port 80 and accepted the connection before closing it " +
		"without a response. That is not a stopped service — it is a server block declining to " +
		"answer, and the server-block check above says which one"
}

// staleConfigCheck names the one conclusion that follows from two checks
// disagreeing, and from nothing else.
//
// If the config on disk NAMES this host, LISTENS on port 80 and SERVES the
// challenge location — and a loopback request carrying that Host still gets
// nothing — then the file is right and the process is not using it. nginx reads
// its configuration at start and at reload; a vhost written after the last
// reload is a file on disk that the running server has never seen.
//
// The console had both halves of this and printed them as unrelated rows, which
// left the reader to notice that "the config is correct" and "nothing answered"
// can only both be true for one reason. Making a person derive the conclusion
// from two facts already on the page is the same failure as not showing them.
func staleConfigCheck(vhost, probe diagCheck) (diagCheck, bool) {
	if !vhost.OK || probe.OK {
		return diagCheck{}, false
	}
	return diagCheck{
		Label: "nginx is running configuration older than this site's vhost", OK: false, Fatal: true,
		Detail: "the two checks above can only disagree for one reason. The config on disk names " +
			"this host, listens on port 80 and serves the challenge location — and a request " +
			"carrying that Host still gets nothing. nginx reads its configuration at start and at " +
			"reload, so a vhost written after the last reload is a file the running server has " +
			"never seen. Nothing about the file needs changing and no DNS record is involved. " +
			"Press Provision now: the root-side helper reloads nginx as part of its run, which is " +
			"the step this service is deliberately unable to perform for itself.",
	}, true
}

// rootSideStalledCheck joins the two root-side facts into the one action that
// resolves both.
//
// The console was reporting them as separate rows — "a request is waiting and no
// run has started since" and "the shell helpers are OLDER than this binary" —
// several rows apart, each true, neither saying what to do. Together they are
// not two problems: they are one. The watcher that consumes the request and the
// helpers that do the work are installed by the same step, and while that step
// is stale nothing else on this page can proceed, because every repair the
// console can offer runs through a worker that is not starting.
//
// This is the one place the product genuinely cannot fix itself, and the rule
// for that case is explicit: show the exact command, copyable, with the reason,
// on the page rather than in a conversation. The service runs unprivileged and
// deliberately cannot install a systemd unit — which is the same property that
// stops a bug in it taking over the machine.
func rootSideStalledCheck(requestStuck, helpersFresh bool) (diagCheck, bool) {
	if !requestStuck || helpersFresh {
		return diagCheck{}, false
	}
	return diagCheck{
		Label: "The root-side half needs reinstalling once", OK: false, Fatal: true,
		Detail: "two rows above are one problem. A provisioning request is sitting unconsumed AND " +
			"the shell helpers predate this binary — and the same step installs both the systemd " +
			"watcher that consumes the request and the helpers that do the work. While that step " +
			"is stale, every repair this console offers goes through a worker that never starts: " +
			"the vhost is never rewritten, nginx is never reloaded, and no certificate can be " +
			"issued no matter how correct the DNS is. Updating from this console swaps the BINARY " +
			"only — it runs unprivileged and cannot write to /usr/local/lib/vayupress or install a " +
			"systemd unit, which is the same property that stops a bug in it taking over the " +
			"machine. Domains & DNS shows the single command that reinstalls the helper, re-arms " +
			"the watcher and runs a pass immediately; it touches neither the binary nor the " +
			"database.",
	}, true
}
