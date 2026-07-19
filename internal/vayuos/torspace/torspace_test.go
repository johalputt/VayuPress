package torspace

import (
	"strings"
	"testing"
)

func envMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

// TestChildEnvIsolation: every writable path the child uses must nest under its
// own root — no path may fall back to a clearnet-shared default (the content /
// media / temp bleed the sibling script had by omitting MEDIA_DIR).
func TestChildEnvIsolation(t *testing.T) {
	root := "/var/lib/vayupress/tor-space"
	m := envMap(BuildChildEnv(root, "abc.onion", 8181, "child-key"))

	for _, k := range []string{"DB_PATH", "CACHE_DIR", "MEDIA_DIR", "STATIC_DIR", "LOG_DIR", "TMP_DIR"} {
		v, ok := m[k]
		if !ok {
			t.Errorf("child env missing %s", k)
			continue
		}
		if !strings.HasPrefix(v, root+"/") {
			t.Errorf("%s=%q must nest under the isolated root %q", k, v, root)
		}
	}
	if m["VAYUOS_MODE"] != "tor" {
		t.Errorf("child must run in Tor mode, VAYUOS_MODE=%q", m["VAYUOS_MODE"])
	}
	if m["VAYUOS_TOR"] != "off" {
		t.Errorf("child must not run its own onion engine, VAYUOS_TOR=%q", m["VAYUOS_TOR"])
	}
	if m[EnvSpaceChild] != "1" {
		t.Errorf("child must carry the recursion-guard sentinel %s=1", EnvSpaceChild)
	}
	if m["PORT"] != "8181" {
		t.Errorf("child PORT=%q, want the distinct loopback port 8181", m["PORT"])
	}
	if m["DOMAIN"] != "abc.onion" {
		t.Errorf("child DOMAIN=%q, want its own onion", m["DOMAIN"])
	}
}

// TestChildEnvNoSecretLeak: the child must NOT inherit the parent's API key or
// any other parent secret — sharing them would leak a secret AND correlate the
// two worlds. Only PATH is whitelisted through.
func TestChildEnvNoSecretLeak(t *testing.T) {
	t.Setenv("API_KEY", "PARENT-SECRET-KEY")
	t.Setenv("DOMAIN", "clearnet.example.com")
	t.Setenv("VAYU_RELEASE_PUBKEY", "parent-only-secret")

	m := envMap(BuildChildEnv("/root", "abc.onion", 8181, "DISTINCT-CHILD-KEY"))

	if m["API_KEY"] != "DISTINCT-CHILD-KEY" {
		t.Errorf("child API_KEY=%q, want its own distinct key (never the parent's)", m["API_KEY"])
	}
	if m["API_KEY"] == "PARENT-SECRET-KEY" {
		t.Error("child inherited the parent's API_KEY — secret leak + world correlation")
	}
	if m["DOMAIN"] == "clearnet.example.com" {
		t.Error("child inherited the parent's clearnet DOMAIN")
	}
	if _, present := m["VAYU_RELEASE_PUBKEY"]; present {
		t.Error("child inherited a non-whitelisted parent env var (secret bleed)")
	}
}

// TestChildEnvOnionOptional: DOMAIN is only set once the onion is known.
func TestChildEnvOnionOptional(t *testing.T) {
	m := envMap(BuildChildEnv("/root", "", 8181, "k"))
	if _, present := m["DOMAIN"]; present {
		t.Error("DOMAIN must be unset until the onion is minted")
	}
}

func TestChildSpaceRoot(t *testing.T) {
	if got := ChildSpaceRoot("/var/lib/vayupress/vayupress.db"); got != "/var/lib/vayupress/tor-space" {
		t.Errorf("ChildSpaceRoot = %q, want /var/lib/vayupress/tor-space", got)
	}
}

func TestRecursionGuard(t *testing.T) {
	t.Setenv(EnvSpaceChild, "1")
	if !IsSpaceChild() {
		t.Error("IsSpaceChild must be true when the sentinel is set")
	}
	t.Setenv(EnvSpaceChild, "")
	if IsSpaceChild() {
		t.Error("IsSpaceChild must be false without the sentinel")
	}
}

func TestNewAPIKeyDistinct(t *testing.T) {
	a, b := NewAPIKey(), NewAPIKey()
	if a == "" || b == "" {
		t.Fatal("NewAPIKey returned empty")
	}
	if a == b {
		t.Error("NewAPIKey must return a fresh key each call")
	}
	if len(a) != 64 { // 32 bytes hex
		t.Errorf("NewAPIKey length = %d, want 64 hex chars", len(a))
	}
}
