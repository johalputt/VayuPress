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
