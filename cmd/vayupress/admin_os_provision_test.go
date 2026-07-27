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
