// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// THE REAL-IP BUTTON COULD NOT BE PRESSED SUCCESSFULLY ON A HOST THAT ALREADY
// SET real_ip_header.
//
// From the operator's panel, verbatim:
//
//	Could not apply — nginx rejected the real-IP config; the previous state was
//	restored. nginx: [emerg] "real_ip_header" directive is duplicate in
//	/etc/nginx/conf.d/vayushield-realip.conf:25
//
// conf.d is included into http, and nginx treats a second real_ip_header in one
// block as fatal. So the helper wrote a file that could never load, rolled it
// back, and reported nginx's text — every press, forever. Meanwhile the posture
// row that sent them there stayed red and their geo rules stayed inert, because
// nothing was resolving the reader.
//
// They had not gone off-script. deploy/nginx-vayupress.conf carried a
// copy-paste recipe that wrote exactly such a file. The product shipped two
// remedies for one problem and the manual one disabled the automatic one.

// TestTheHelperDefersToAnExistingRealIPHeader runs the SHIPPED emit block
// against a config dump that already sets the directive.
//
// Extracted and executed rather than pattern-matched: the defect was in what the
// file contained, and only running the generator can tell you that. A test
// asserting the source mentions "conflict" would have passed against the broken
// version the moment somebody added the word.
func TestTheHelperDefersToAnExistingRealIPHeader(t *testing.T) {
	agent := readDeployFile(t, "vayushield-agent.sh")
	probe := shellFuncBody(agent, "realip_existing_header")
	if probe == "" {
		t.Fatal("realip_existing_header is not defined; nothing detects the duplicate")
	}

	dir := t.TempDir()
	// A dump shaped like the operator's host: their hand-written file sets the
	// header at http level, and the vhost below is ordinary config.
	dump := `# configuration file /etc/nginx/nginx.conf:
http {
    include /etc/nginx/conf.d/*.conf;
}
# configuration file /etc/nginx/conf.d/00-cdn-realip.conf:
set_real_ip_from 173.245.48.0/20;
real_ip_header CF-Connecting-IP;
`
	got := runRealIPProbe(t, dir, probe, dump, "/etc/nginx/conf.d/vayushield-realip.conf")
	if !strings.Contains(got, "00-cdn-realip.conf") {
		t.Fatalf("the probe did not find the existing header.\n\nWithout it the helper writes a "+
			"second one and nginx rejects the whole file.\n\ngot: %q", got)
	}
	if !strings.Contains(got, "CF-Connecting-IP") {
		t.Errorf("the probe found the directive but not its VALUE. The value decides whether these "+
			"ranges resolve anything: a CF-Connecting-IP allowlist under an X-Forwarded-For header "+
			"resolves whatever the chain claims.\n\ngot: %q", got)
	}
}

// The negative, and the one that keeps the feature working: a real_ip_header
// this helper's OWN file set must not be mistaken for somebody else's, or the
// second run after a successful first would stop emitting it and quietly undo
// the fix.
func TestTheHelperDoesNotTreatItsOwnHeaderAsAConflict(t *testing.T) {
	probe := shellFuncBody(readDeployFile(t, "vayushield-agent.sh"), "realip_existing_header")
	mine := "/etc/nginx/conf.d/vayushield-realip.conf"
	dump := `# configuration file /etc/nginx/nginx.conf:
http {
    include /etc/nginx/conf.d/*.conf;
}
# configuration file ` + mine + `:
set_real_ip_from 173.245.48.0/20;
real_ip_header CF-Connecting-IP;
`
	if got := runRealIPProbe(t, t.TempDir(), probe, dump, mine); strings.TrimSpace(got) != "" {
		t.Errorf("the helper reported its own header as a conflict.\n\nOn the next run it would "+
			"stop emitting the directive, and resolution would silently stop working on a host "+
			"that was fine.\n\ngot: %q", got)
	}
}

// A real_ip_header inside a server block is legal alongside one in http —
// nginx scopes the duplicate check per block. Reporting it would make the
// helper skip its own directive for no reason, leaving a host with ranges and
// no header at http level.
func TestARealIPHeaderInsideAServerBlockIsNotAConflict(t *testing.T) {
	probe := shellFuncBody(readDeployFile(t, "vayushield-agent.sh"), "realip_existing_header")
	dump := `# configuration file /etc/nginx/nginx.conf:
http {
    server {
        listen 443 ssl;
        real_ip_header X-Forwarded-For;
        location / {
            proxy_pass http://127.0.0.1:8080;
        }
    }
}
`
	if got := runRealIPProbe(t, t.TempDir(), probe, dump, "/etc/nginx/conf.d/vayushield-realip.conf"); strings.TrimSpace(got) != "" {
		t.Errorf("a server-scoped real_ip_header was reported as an http-level conflict.\n\n"+
			"The helper would then omit its own directive and resolve nothing at http level.\n\n"+
			"got: %q", got)
	}
}

// runRealIPProbe executes the extracted probe with a stub `nginx -T`.
//
// The stub is what makes this a test of the shipped code rather than of a
// paraphrase: the function is pasted in unmodified and only its one external
// dependency is replaced.
func runRealIPProbe(t *testing.T, dir, probe, dump, mine string) string {
	t.Helper()
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("awk unavailable")
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	dumpPath := filepath.Join(dir, "dump.txt")
	if err := os.WriteFile(dumpPath, []byte(dump), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\nexec cat " + dumpPath + "\n"
	if err := os.WriteFile(filepath.Join(bin, "nginx"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(dir, "probe.sh")
	body := "#!/bin/sh\n" + probe + "\n}\nrealip_existing_header \"" + mine + "\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/sh", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe failed: %v\n%s", err, out)
	}
	return string(out)
}

// A CONTROL THAT CANNOT DO WHAT IT WAS PRESSED FOR HAS NOT BEEN APPLIED.
//
// The first version of the deferral wrote state "active" whatever header it
// found, and put the problem in the reason prose. The operator upgraded, read
// the green "Applied" pill, and came back still receiving the traffic they had
// refused — because the header their config named was one their CDN does not
// send, so nginx had nothing to read and every visitor still arrived as the
// edge.
//
// Green has to mean the thing you pressed the button for is now true. This is
// the same "a claim is not a control" defect as the geo rule itself, one layer
// further in, introduced while fixing that one.
// RUN, don't read. The first version of this test asserted on the function's
// source and passed against the exact bug it was written for: it looked for
// `write_state realip active` immediately after the `if`, which a later line
// broke up, and for the presence of `write_state realip error` anywhere — which
// the rejected-config branch supplies whatever the deferral logic does. Two
// assertions, both green, neither touching the behaviour.
func TestDeferringToAHeaderTheCDNDoesNotSendIsReportedAsAFailure(t *testing.T) {
	for _, tc := range []struct {
		name, header, wantState string
	}{
		// What their host is doing: nginx's own default name, which looks right
		// and which Cloudflare never sends.
		{"a header the CDN does not send", "X-Real-IP", "error"},
		{"the header this helper would have written", "CF-Connecting-IP", "active"},
		{"the enterprise equivalent", "True-Client-IP", "active"},
		{"the weaker but working one", "X-Forwarded-For", "active"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := runReconcileRealIP(t, tc.header)
			if got != tc.wantState {
				t.Errorf("state = %q, want %q\n\nreason: %s\n\n"+
					"A green pill means the thing the operator pressed the button for is now "+
					"true. It was reported for a config that parses and resolves nobody, and "+
					"the operator upgraded, saw green, and came back still receiving the "+
					"traffic they had refused.", got, tc.wantState, reason)
			}
			if tc.wantState == "error" && !strings.Contains(reason, "does not send") {
				t.Errorf("the failure never says the CDN does not send that header, which is the "+
					"one fact explaining why a config nginx ACCEPTED still resolves nobody.\n\n"+
					"reason: %s", reason)
			}
			if !strings.Contains(reason, tc.header) {
				t.Errorf("the reason does not name the header in effect (%s), so the operator "+
					"cannot tell which line to change.\n\nreason: %s", tc.header, reason)
			}
		})
	}
}

// runReconcileRealIP executes the SHIPPED reconcile_realip with its external
// dependencies stubbed, and returns the state and reason it wrote.
//
// Only the seams are replaced — nginx, the reload, the digest, the state
// writers. The decision being tested is the function's own.
func runReconcileRealIP(t *testing.T, existingHeader string) (state, reason string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh unavailable")
	}
	agent := readDeployFile(t, "vayushield-agent.sh")
	probe := shellFuncBody(agent, "realip_existing_header")
	fn := shellFuncBody(agent, "reconcile_realip")
	if probe == "" || fn == "" {
		t.Fatal("could not extract the shipped functions")
	}

	dir := t.TempDir()
	ctl := filepath.Join(dir, "control")
	bin := filepath.Join(dir, "bin")
	for _, d := range []string{ctl, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The panel's request, and a proxy range list with one valid range.
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(ctl, "realip.want"), "")
	allow := filepath.Join(dir, "cdn-allow.conf")
	write(allow, "173.245.48.0/20\n2400:cb00::/32\n")

	// nginx -T reports a host that already sets the directive at http level.
	dump := "# configuration file /etc/nginx/conf.d/00-cdn-realip.conf:\n" +
		"real_ip_header " + existingHeader + ";\n"
	dumpPath := filepath.Join(dir, "dump.txt")
	write(dumpPath, dump)
	write(filepath.Join(bin, "nginx"), "#!/bin/sh\nexec cat "+dumpPath+"\n")
	if err := os.Chmod(filepath.Join(bin, "nginx"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "vayushield-realip.conf")
	script := filepath.Join(dir, "run.sh")
	write(script, `#!/bin/sh
CONTROL_DIR=`+ctl+`
CDN_ALLOW_FILE=`+allow+`
REALIP_CONF_FILE=`+out+`
write_state() { printf '%s' "$2" >"${CONTROL_DIR}/$1.state"; }
clear_reason() { rm -f "${CONTROL_DIR}/$1.reason"; }
write_digest() { :; }
shield_backup() { :; }
# nginx accepts the file — the whole point is that a config which PARSES can
# still resolve nobody.
nginx_try_reload() { return 0; }
`+probe+`
}
`+fn+`
}
reconcile_realip
`)
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("/bin/sh", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reconcile_realip failed: %v\n%s", err, o)
	}
	s, err := os.ReadFile(filepath.Join(ctl, "realip.state"))
	if err != nil {
		t.Fatalf("no state written: %v", err)
	}
	r, _ := os.ReadFile(filepath.Join(ctl, "realip.reason"))
	return string(s), string(r)
}

// The manual recipe that CREATED the collision must not come back. It wrote
// /etc/nginx/conf.d/00-cdn-realip.conf with its own real_ip_header, and any host
// that ran it could never apply the panel control again.
func TestTheNginxTemplateNoLongerHandsOutTheCollidingRecipe(t *testing.T) {
	conf := readDeployFile(t, "nginx-vayupress.conf")
	if strings.Contains(conf, "tee /etc/nginx/conf.d/00-cdn-realip.conf") {
		t.Error("the template still tells operators to hand-write a second real_ip_header file, " +
			"which makes the Hardening control permanently unappliable on that host")
	}
	// And it must point at what does work, or removing the recipe just leaves an
	// operator behind a CDN with no instruction at all.
	if !strings.Contains(conf, "Resolve the real visitor address") {
		t.Error("the template no longer names the panel control that does this, so the section " +
			"describes a problem with no remedy")
	}
}

// THE TWO SIDES OF THE DIGEST HAVE TO AGREE ON THE KEY NAMES.
//
// The agent writes the file and the app reads it, in different languages, in
// different repositories of knowledge, with nothing between them but a string.
// A key renamed on one side is not a compile error — it is a field that silently
// stays zero, which for this field means the posture row goes back to reporting
// a symptom with no cause. That is exactly the state this whole track started in.
func TestTheRealIPDigestKeysRoundTripFromTheAgentToTheRow(t *testing.T) {
	agent := readDeployFile(t, "vayushield-agent.sh")

	// The agent must emit them...
	for _, k := range []string{"realip_header", "realip_ranges"} {
		if !strings.Contains(agent, "printf '"+k+"=%s\\n'") {
			t.Errorf("the agent never emits %q, so the app reads a key nothing writes and the "+
				"posture row loses the cause it exists to report", k)
		}
	}

	// ...and the app must actually parse them. Behavioural: a real digest file
	// through the real reader.
	dir := t.TempDir()
	t.Setenv("VAYUSHIELD_CONTROL_DIR", dir)
	digest := "schema=1\ngenerated=1\nrealip_header=X-Real-IP\nrealip_ranges=22\n"
	if err := os.WriteFile(filepath.Join(dir, shieldDigestName), []byte(digest), 0o644); err != nil {
		t.Fatal(err)
	}

	d := readShieldDigest()
	if !d.Present {
		t.Fatal("the digest did not parse at all")
	}
	if d.RealIPHeader != "X-Real-IP" {
		t.Errorf("RealIPHeader = %q, want X-Real-IP", d.RealIPHeader)
	}
	if d.RealIPRanges != 22 {
		t.Errorf("RealIPRanges = %d, want 22", d.RealIPRanges)
	}
}
