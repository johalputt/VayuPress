// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProvisionRequestCarriesNoArguments is the guard on the property the whole
// privilege boundary rests on.
//
// The unprivileged service (www-data, NoNewPrivileges) creates a flag file that
// a ROOT systemd unit reacts to. That is only safe while the flag is a pure
// signal: the moment anything writes content here and the root worker reads it,
// an unprivileged process is passing arguments to a root process — the classic
// local privilege escalation, and it would arrive looking like a small feature
// ("let the console choose which subdomain to provision").
//
// So this test asserts the request file is EMPTY, and the source below asserts
// the worker never reads it.
func TestProvisionRequestCarriesNoArguments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", dir)

	path := filepath.Join(dir, provisionRequestFile)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(b) != 0 {
		t.Fatalf("request file carries %d bytes; it must be a pure signal", len(b))
	}
	if !provisionPending() {
		t.Error("a fresh request was not reported as pending")
	}
}

// TestProvisionWorkerNeverReadsTheRequest inspects the root-side script. A grep
// is crude, but the property is worth an imperfect guard: if someone teaches the
// worker to read the flag, this fails and says why, which is more than review
// would reliably catch on a shell script nobody looks at twice.
func TestProvisionWorkerNeverReadsTheRequest(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("../../scripts/provision-subdomains.sh"))
	if err != nil {
		t.Skipf("worker script not readable from here: %v", err)
	}
	src := string(b)

	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.Contains(trimmed, "REQUEST") {
			continue
		}
		// Removing the flag is the only legitimate use.
		if strings.HasPrefix(trimmed, "rm ") || strings.Contains(trimmed, `rm -f "$REQUEST"`) ||
			strings.HasPrefix(trimmed, "REQUEST=") {
			continue
		}
		t.Errorf("the root worker touches the request file beyond deleting it:\n  %s\n"+
			"Reading it would let an unprivileged process pass arguments to root.", trimmed)
	}
}

// TestProvisionStatusRequiresAdmin — the endpoint reveals server paths and run
// history, and the run endpoint makes the box execute certbot on demand. Neither
// belongs to a non-admin session.
func TestProvisionStatusRequiresAdmin(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("admin_os_provision.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	src := string(b)
	for _, fn := range []string{"handleOSProvisionRequest", "handleOSProvisionStatus"} {
		i := strings.Index(src, "func (a *App) "+fn)
		if i < 0 {
			t.Fatalf("%s not found", fn)
		}
		end := strings.Index(src[i:], "\nfunc ")
		if end < 0 {
			end = len(src) - i
		}
		if !strings.Contains(src[i:i+end], "isAdminRequest") {
			t.Errorf("%s is not admin-gated", fn)
		}
	}
}

// TestProvisionRunIsCSRFProtected — a POST that makes the server run certbot
// must not be triggerable by an off-origin page. Let's Encrypt rate limits are
// finite, so this is a real denial-of-service lever, not a theoretical one.
func TestProvisionRunIsCSRFProtected(t *testing.T) {
	b, err := os.ReadFile(filepath.Clean("admin_os_ui.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.Contains(line, "/os/api/provision/run") {
			continue
		}
		if !strings.Contains(line, "CSRFTokenMiddleware") {
			t.Errorf("provision run route is not CSRF-protected:\n  %s", strings.TrimSpace(line))
		}
		return
	}
	t.Error("provision run route not registered")
}

// TestDNSRecordsCoverEveryProvisionedSubdomain keeps the page honest against the
// provisioning worker. If a helper gains a new subdomain and this list does not,
// the page shows a green install that is quietly missing a record — which is the
// exact failure mode the page was built to end.
func TestDNSRecordsCoverEveryProvisionedSubdomain(t *testing.T) {
	got := map[string]bool{}
	for _, r := range subdomainRecords("example.com") {
		got[r.Host] = true
	}
	for _, want := range []string{
		"example.com", "www.example.com", "mail.example.com",
		"openpgpkey.example.com", "talk.example.com",
		"mcp.example.com", "api.example.com",
	} {
		if !got[want] {
			t.Errorf("Domains & DNS does not list %s", want)
		}
	}
}

// TestProxyOffMarkedOnEveryMachineToMachineHost — the apex and www may sit behind
// a proxy; every other host is fetched by software with no JavaScript engine, so
// a bot challenge breaks it. Marking one of them "either" would send an operator
// to a silent failure with the page's blessing.
func TestProxyOffMarkedOnEveryMachineToMachineHost(t *testing.T) {
	for _, r := range subdomainRecords("example.com") {
		machineToMachine := strings.HasPrefix(r.Host, "mail.") ||
			strings.HasPrefix(r.Host, "openpgpkey.") ||
			strings.HasPrefix(r.Host, "talk.") ||
			strings.HasPrefix(r.Host, "mcp.") ||
			strings.HasPrefix(r.Host, "api.")
		if machineToMachine && !r.ProxyOff {
			t.Errorf("%s is machine-to-machine but not marked proxy-off", r.Host)
		}
		if !machineToMachine && r.ProxyOff {
			t.Errorf("%s is browser traffic; forcing proxy-off needlessly gives up CDN protection", r.Host)
		}
	}
}

// TestDNSPageIsAdminGated — the page lists the install's own hostnames and
// resolved addresses and carries the privileged provisioning control.
func TestDNSPageIsAdminGated(t *testing.T) {
	if osPathMinLevel("/os/dns") < osPathMinLevel("/os/update") {
		t.Error("/os/dns is gated below /os/update; it exposes infrastructure detail and a privileged control")
	}
}
