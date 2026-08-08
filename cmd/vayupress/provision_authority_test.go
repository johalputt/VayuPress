// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/johalputt/vayupress/internal/apikeys"
	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/users"
)

// SECTION 6 AUDIT — two doors to the same root action, at two different heights.
//
// `POST /os/api/provision/run` and the MCP tool `provision_certificates` do the
// identical thing: write the flag that starts a root systemd unit. They did not
// agree on who may do it.
//
//	HTTP  cmd/vayupress/admin_os_ui.go:432 → isAdminRequest + keyMayCall, and
//	      /os/api/provision appeared in no rule, so the fail-closed default
//	      applied: a SUPERUSER key only.
//	MCP   cmd/vayupress/mcp_sites.go:551 → Visible: mcpVisible(SectionDomains,
//	      ActionWrite) — a scoped `domains:write` key.
//
// A key holding domains:write was refused at the front door and admitted at the
// side one. That is not a hole so much as a rule nobody wrote down: the HTTP
// side's strictness came from an omission from the table, not from a decision,
// and an unstated rule is the kind that changes silently when someone adds the
// obvious mapping later.
//
// Resolved by stating it: the route is mapped to domains:write, matching the
// tool. This grants no capability that did not already exist — the same key
// could already do exactly this through MCP — and it replaces an accident with
// a decision. Raising the MCP tool to superuser instead was rejected: it would
// take away a working path from operators driving provisioning through a scoped
// key, to close a gap that is not open.
func TestBothDoorsToRootProvisioningRequireTheSameCapability(t *testing.T) {
	const route = "/os/api/provision/run"

	sec, act, mapped := capabilityFor("POST", route)
	if !mapped {
		t.Fatalf("%s is in no capability rule, so it falls through to the superuser\n"+
			"default while the MCP tool that does the identical thing accepts\n"+
			"domains:write. The two doors to a root action must state the same rule.", route)
	}
	if sec != apikeys.SectionDomains || act != apikeys.ActionWrite {
		t.Errorf("%s requires %s:%s; the MCP tool provision_certificates requires %s:%s.\n\n"+
			"Both write the same flag and start the same root unit.",
			route, sec, act, apikeys.SectionDomains, apikeys.ActionWrite)
	}

	// The bar must still be a real one — a mapping to a capability every key
	// holds would "agree" with the tool and admit anybody.
	perms := apikeys.NewPermissions()
	perms.Grant(apikeys.SectionPosts, apikeys.ActionWrite)
	if keyMayCall(apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: perms}, "POST", route) {
		t.Error("a key holding only posts:write can start the root provisioning unit")
	}

	// And the bar the tool sets must actually open this door, or the two are
	// still disagreeing — in the other direction.
	dom := apikeys.NewPermissions()
	dom.Grant(apikeys.SectionDomains, apikeys.ActionWrite)
	if !keyMayCall(apikeys.KeyInfo{Scope: apikeys.ScopeExternal, Perms: dom}, "POST", route) {
		t.Error("a key holding domains:write — which provision_certificates accepts — is still " +
			"refused at the HTTP route")
	}
}

// A privileged action with no record of who asked for it.
//
// The sibling request handler already does this — admin_os_veil_harden.go:164
// records `vayuveil.harden.request` — so the mechanism exists and this path
// simply never called it. Root ran, nginx was rewritten, certificates were
// requested, and the only trace was a log line from the worker saying what
// happened, never who asked.
// Driven, not read. A test that greps the handler for "AuditLog" passes a
// refactor that leaves the call in a comment and fails an honest rename, so this
// calls the handler and reads the table.
func TestStartingTheRootProvisioningUnitIsRecorded(t *testing.T) {
	app := resetSessionApp(t)
	data := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", data)
	installProvisioningFixture(t, filepath.Join(data, provisionRequestFile))

	req := httptest.NewRequest(http.MethodPost, "/os/api/provision/run", nil)
	rec := httptest.NewRecorder()
	app.handleOSProvisionRequest(rec, withUser(req, &users.User{ID: "admin1", Role: users.RoleAdmin}))

	var n int
	if err := dbpkg.DB.QueryRow(
		`SELECT COUNT(*) FROM audit_log WHERE action = ?`, provisionAuditAction,
	).Scan(&n); err != nil {
		t.Skipf("audit_log not queryable in this harness: %v", err)
	}
	if n == 0 {
		t.Errorf("starting the root provisioning unit wrote no audit record (status %d).\n\n"+
			"It runs a unit as root, rewrites nginx and talks to a certificate authority.\n"+
			"An operator asking later who triggered it has nothing to read — while the\n"+
			"sibling request handler for VayuVeil hardening has recorded exactly this\n"+
			"since it was written.", rec.Code)
	}
}

// installProvisioningFixture points the two root-half paths at a temp tree and
// writes a .path unit watching watchPath, so the real provisionUnitsInstalled
// runs against something a test may create.
func installProvisioningFixture(t *testing.T, watchPath string) {
	t.Helper()
	// Byte-for-byte the shape scripts/install-provisioning.sh writes.
	installProvisioningFixtureRaw(t, "[Unit]\nDescription=x\n\n[Path]\nPathExists="+watchPath+"\nUnit=vayupress-provision.service\n")
}

func installProvisioningFixtureRaw(t *testing.T, unitBody string) {
	t.Helper()
	dir := t.TempDir()
	worker := filepath.Join(dir, "provision-subdomains.sh")
	unit := filepath.Join(dir, "vayupress-provision.path")
	if err := os.WriteFile(worker, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte(unitBody), 0o644); err != nil {
		t.Fatal(err)
	}
	oldW, oldU := provisionWorkerPath, provisionUnitPath
	provisionWorkerPath, provisionUnitPath = worker, unit
	t.Cleanup(func() { provisionWorkerPath, provisionUnitPath = oldW, oldU })
}

// SECTION 6 AUDIT — a button that is enabled, reports success, and can never work.
//
// provisionStateDir() honours VAYU_DATA_DIR. The systemd .path unit does not:
// scripts/install-provisioning.sh:119 writes
// `PathExists=/var/lib/vayupress/provision.request` as a literal. Set the
// variable — a supported configuration, honoured by the Go side on purpose — and
// the panel writes its request to one path while systemd watches another.
//
// Both halves are installed, so the old check said yes. The operator gets an
// enabled button, a success response, "provisioning is running", and then
// silence for ever, because nothing is watching the file that was written. It is
// precisely the failure provisionUnitsInstalled was written to prevent, reached
// through configuration instead of a partial install — and the check could not
// see it, because it asked whether the units EXIST and never whether they and
// this process agree about where the request goes.
func TestProvisioningIsNotOfferedWhenTheUnitWatchesADifferentFile(t *testing.T) {
	data := t.TempDir()
	t.Setenv("VAYU_DATA_DIR", data)

	installProvisioningFixture(t, filepath.Join(data, provisionRequestFile))
	if !provisionUnitsInstalled() {
		t.Fatal("provisioning reported unavailable while the unit watches exactly the path " +
			"this process writes — the check refuses a working install")
	}

	// The worker script itself. Pre-existing behaviour, but mutation showed
	// nothing covered it: deleting the check left every test green.
	installProvisioningFixture(t, filepath.Join(data, provisionRequestFile))
	if err := os.Remove(provisionWorkerPath); err != nil {
		t.Fatal(err)
	}
	if provisionUnitsInstalled() {
		t.Error("provisioning is offered with no worker script installed — the unit would " +
			"fire and run nothing")
	}
	installProvisioningFixture(t, filepath.Join(data, provisionRequestFile))

	// A unit carrying no PathExists= at all watches nothing, so it is no more
	// installed than a missing one. Found by mutation: making the missing-directive
	// branch return true left every test green.
	installProvisioningFixtureRaw(t, "[Unit]\nDescription=x\n\n[Path]\nUnit=vayupress-provision.service\n")
	if provisionUnitsInstalled() {
		t.Error("a .path unit with no PathExists= directive was accepted as installed — " +
			"systemd would watch nothing at all")
	}

	// The mismatch: the unit still watches the compiled-in default.
	installProvisioningFixture(t, "/var/lib/vayupress/"+provisionRequestFile)
	if provisionUnitsInstalled() {
		t.Errorf("provisioning is offered while systemd watches /var/lib/vayupress/%s and this\n"+
			"process writes to %s.\n\n"+
			"The button renders enabled, answers success, and reports the request as running\n"+
			"until it times out. Nothing consumes it, and nothing anywhere says why.",
			provisionRequestFile, filepath.Join(data, provisionRequestFile))
	}
}
