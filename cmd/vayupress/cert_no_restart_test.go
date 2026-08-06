// SPDX-License-Identifier: Apache-2.0

package main

// cert_no_restart_test.go — ADR-0155. A certificate must never cost an outage.
//
// nginx terminates TLS and proxies to :8080 with no queue in front of it, so
// while the app is restarting every request is a 502. The outage is exactly the
// app's startup time — and three separate helpers were spending it to publish a
// certificate, one of them unattended on every quarterly renewal.
//
// Two of those restarts were never needed. The domain registry re-reads SQLite
// on a TTL whose own comment says it exists to bound staleness from an
// out-of-band DB edit, which is precisely what the provisioning CLI does; and the
// mail TLS keypair is served by a loader that stats the files and hot-reloads
// them. Both mechanisms shipped long before the restarts were added.
//
// This file is the guard that keeps them gone. It reads the shell, because that
// is where the defect lives and no Go test would otherwise look.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// certPathScripts are the helpers that run on a certificate path — the ones an
// operator triggers by adding a domain, and the ones certbot triggers on
// renewal. Every one of them may reload nginx; none of them may restart the app.
var certPathScripts = []string{
	"scripts/setup-vayudomain.sh",
	"scripts/setup-talk-subdomain.sh",
	"scripts/setup-mcp-subdomain.sh",
	"scripts/setup-api-subdomain.sh",
	"scripts/setup-openpgpkey-subdomain.sh",
	"scripts/provision-subdomains.sh",
}

// restartsTheApp reports the executable lines that restart VayuPress.
//
// Comments are skipped, and that matters here rather than being hygiene: the
// removals left behind comments that NAME the command they deleted, explaining
// why it went. A guard that matched those would fail on the very change it
// exists to protect — the "a gate can match itself" defect this repository has
// a standing note about.
func restartsTheApp(src string) []string {
	var hits []string
	for _, line := range strings.Split(src, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if !strings.Contains(t, "vayupress") {
			continue
		}
		for _, verb := range []string{"try-restart", "restart vayupress", "systemctl restart"} {
			if strings.Contains(t, verb) {
				hits = append(hits, t)
				break
			}
		}
	}
	return hits
}

// THE test. A certificate is not worth an outage.
func TestNoCertificatePathRestartsTheApp(t *testing.T) {
	for _, rel := range certPathScripts {
		b, err := os.ReadFile(filepath.Clean(filepath.Join("../..", rel)))
		if err != nil {
			t.Skipf("%s not readable from here: %v", rel, err)
			return
		}
		for _, line := range restartsTheApp(string(b)) {
			t.Errorf("%s restarts the app on a certificate path: %s\n"+
				"\tnginx has no queue in front of :8080, so this is a 502 for every visitor "+
				"for the whole of startup. Reload nginx instead; the registry TTL and the "+
				"reloading mail certificate already carry the change into the running process.",
				rel, line)
		}
	}
}

// The certbot renewal hook is the worst case and gets its own assertion: it fires
// unattended, quarterly, with nobody watching, on an install nobody is looking at.
func TestTheCertbotRenewalHookDoesNotRestartTheApp(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/deploy-vayupress.sh"))
	if err != nil {
		t.Skipf("deploy script not readable from here: %v", err)
		return
	}
	src := string(b)
	start := strings.Index(src, "renewal-hooks/deploy/vayupress-mailcert.sh")
	if start < 0 {
		t.Skip("the mail certificate renewal hook is no longer written here")
		return
	}
	end := strings.Index(src[start:], "\nHOOK")
	if end < 0 {
		t.Fatal("the renewal hook heredoc is unterminated; this guard cannot see its body")
	}
	for _, line := range restartsTheApp(src[start : start+end]) {
		t.Errorf("the mail certificate renewal hook restarts the app: %s\n"+
			"\tThis runs unattended on renewal. The mail TLS keypair is already served by a "+
			"loader that hot-reloads it from disk, so the restart takes a live site down to "+
			"do what the binary does by itself.", line)
	}
}

// Reloading nginx is not merely allowed, it is the mechanism — so a helper that
// stopped doing it would have published a vhost the running server never read.
// Asserting the positive keeps a future "simplification" from deleting both.
func TestTheCertificateHelpersStillReloadNginx(t *testing.T) {
	for _, rel := range []string{
		"scripts/setup-vayudomain.sh",
		"scripts/setup-mcp-subdomain.sh",
		"scripts/setup-api-subdomain.sh",
	} {
		b, err := os.ReadFile(filepath.Clean(filepath.Join("../..", rel)))
		if err != nil {
			t.Skipf("%s not readable from here: %v", rel, err)
			return
		}
		if !strings.Contains(string(b), "reload nginx") {
			t.Errorf("%s never reloads nginx, so its vhost is a file the running server has not read", rel)
		}
	}
}
