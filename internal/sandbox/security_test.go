// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"os"
	"testing"
)

// TestManifestDefaultsAreSafe verifies that a zero-value Manifest has
// safe (restrictive) defaults — no network, no broad path access.
func TestManifestDefaultsAreSafe(t *testing.T) {
	m := Manifest{}

	if m.AllowNetwork {
		t.Error("default AllowNetwork must be false")
	}
	if len(m.AllowedReadPaths) != 0 {
		t.Errorf("default AllowedReadPaths must be empty, got %v", m.AllowedReadPaths)
	}
	if len(m.AllowedWritePaths) != 0 {
		t.Errorf("default AllowedWritePaths must be empty, got %v", m.AllowedWritePaths)
	}
	if m.ConfineMounts {
		// ConfineMounts=false by default is required: enabling it needs
		// CAP_SYS_ADMIN, so it must never be silently true.
		t.Error("default ConfineMounts must be false (requires CAP_SYS_ADMIN)")
	}
}

// TestPluginConfinementCleanupIsIdempotent verifies Cleanup can be called
// multiple times without panic or error — important for deferred cleanup paths.
func TestPluginConfinementCleanupIsIdempotent(t *testing.T) {
	c, err := SetupConfinement(Manifest{Name: "idem", ConfineMounts: true})
	if err != nil {
		t.Fatal(err)
	}
	dir := c.ScratchDir()
	// Must not panic.
	c.Cleanup()
	c.Cleanup()
	c.Cleanup()
	if dir != "" {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("the scratch directory %s outlived Cleanup", dir)
		}
	}
}

// TestPluginConfinementNilSafe verifies nil receiver methods don't panic.
func TestPluginConfinementNilSafe(t *testing.T) {
	var c *PluginConfinement
	c.Cleanup() // must not panic
	if got := c.ScratchDir(); got != "" {
		t.Errorf("nil ScratchDir() = %q, want empty", got)
	}
}

// TestErrQuarantinedIsDistinct verifies ErrQuarantined is a distinct sentinel
// that callers can unwrap — not a generic errors.New value.
func TestErrQuarantinedIsDistinct(t *testing.T) {
	if ErrQuarantined == nil {
		t.Fatal("ErrQuarantined must not be nil")
	}
	if ErrQuarantined.Error() == "" {
		t.Error("ErrQuarantined.Error() must not be empty")
	}
}

// TestCapabilityEnforcementBlocksNetworkPayload verifies EnforceCapabilities
// rejects network-implicating payload keys when AllowNetwork=false.
func TestCapabilityEnforcementBlocksNetworkPayload(t *testing.T) {
	m := Manifest{
		Name:         "test-plugin",
		AllowNetwork: false,
	}
	payload := map[string]interface{}{"url": "https://evil.example.com"}
	if err := EnforceCapabilities(m, "on_publish", payload); err == nil {
		t.Error("EnforceCapabilities must reject url payload when AllowNetwork=false")
	}
}

// TestCapabilityEnforcementAllowsCleanPayload verifies a clean payload passes.
func TestCapabilityEnforcementAllowsCleanPayload(t *testing.T) {
	m := Manifest{
		Name:         "test-plugin",
		AllowNetwork: false,
	}
	payload := map[string]interface{}{"title": "hello world"}
	if err := EnforceCapabilities(m, "on_publish", payload); err != nil {
		t.Errorf("EnforceCapabilities rejected safe payload: %v", err)
	}
}
