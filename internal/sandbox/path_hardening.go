package sandbox

import (
	"fmt"
	"os"
	"strings"
)

// ResolveAndCheckPath resolves symlinks in path and verifies the resolved path
// is still under one of the allowed prefixes. This prevents symlink traversal
// attacks where a plugin payload points to a symlink that escapes the allowed tree.
func ResolveAndCheckPath(path string, allowedPrefixes []string) error {
	resolved, err := os.Readlink(path)
	if err != nil {
		// Not a symlink — use the path as-is.
		resolved = path
	}
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(resolved, prefix) {
			return nil
		}
	}
	return fmt.Errorf("%w: resolved path %q (from %q) not in allowed prefixes", ErrCapabilityDenied, resolved, path)
}
