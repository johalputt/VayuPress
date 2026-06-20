package render

import (
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// TestSafeCacheJoin verifies the cache-path guard rejects traversal that would
// escape the cache directory (defence in depth for CodeQL "uncontrolled data in
// path expression"), while accepting legitimate relative paths.
func TestSafeCacheJoin(t *testing.T) {
	config.Cfg.CacheDir = "/var/cache/vayupress"

	ok := []string{
		"posts/hello.html",
		"tags/go.html",
		"home/index.html",
		"posts/a-b-c.html",
	}
	for _, p := range ok {
		if _, valid := safeCacheJoin(p); !valid {
			t.Errorf("expected %q to be accepted", p)
		}
	}

	bad := []string{
		"../etc/passwd",
		"posts/../../etc/passwd",
		"../../../../etc/shadow",
		"posts/../../../tmp/x",
	}
	for _, p := range bad {
		if full, valid := safeCacheJoin(p); valid {
			t.Errorf("expected %q to be rejected, got %q", p, full)
		}
	}
}
