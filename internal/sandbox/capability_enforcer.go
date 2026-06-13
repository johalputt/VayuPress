package sandbox

import (
	"fmt"
	"strings"
)

// EnforceCapabilities validates that a request does not exceed the plugin's
// declared capabilities. The host calls this before writing to the subprocess.
// This is a defence-in-depth check — the subprocess also self-enforces.
func EnforceCapabilities(m Manifest, hook string, payload map[string]interface{}) error {
	// Check network access if payload contains a "url", "endpoint", or "host" key.
	if !m.AllowNetwork {
		for k := range payload {
			lk := strings.ToLower(k)
			if lk == "url" || lk == "endpoint" || lk == "host" {
				return fmt.Errorf("%w: hook %q payload key %q implies network access", ErrCapabilityDenied, hook, k)
			}
		}
	}
	// Check path access if payload contains a "path" key.
	if path, ok := payload["path"].(string); ok {
		if !m.AllowsReadPath(path) && !m.AllowsWritePath(path) {
			return fmt.Errorf("%w: hook %q path %q not in allowed paths", ErrCapabilityDenied, hook, path)
		}
		// Symlink traversal: verify the resolved target is also within allowed paths.
		combined := append(append([]string{}, m.AllowedReadPaths...), m.AllowedWritePaths...)
		if err := ResolveAndCheckPath(path, combined); err != nil {
			return err
		}
	}
	return nil
}
