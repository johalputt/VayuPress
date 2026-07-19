// Package torspace supports the VayuOS "Anonymous Tor Space" (ADR-0141): a
// second, isolated VayuPress instance the running clearnet process supervises so
// an operator can stand up a separate .onion world with ONE click — no terminal,
// no root, no systemd. The child is a full VayuPress in Tor mode with its OWN
// database, media, accounts and .onion identity, sharing no state with the
// clearnet world.
//
// HONESTY (do not overstate): this is content/identity COMPARTMENTALISATION, not
// anonymity against an adversary who can access the server or correlate the
// network. Both worlds run on the same host, same UID and the same Tor daemon;
// a server seizure, disk image, or coincident uptime links them — and the
// clearnet install is already an identified real-world entity. Anonymity that
// survives that requires a physically separate machine (the sibling-service
// install, scripts/setup-tor-space.sh). The admin UI states this plainly.
//
// This file is the isolation contract: it builds the child's CURATED environment
// (no inherited secrets, a distinct API key, every writable path pinned under an
// isolated root) and the recursion-guard sentinel. It spawns nothing.
package torspace

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
)

// EnvSpaceChild is the recursion-guard sentinel. The child instance runs with it
// set to "1" so it never supervises a grandchild Tor Space or starts the
// parent's per-domain onion engine.
const EnvSpaceChild = "VAYUOS_SPACE_CHILD"

// EnvParentConsole tells the child it is managed from the parent's clearnet
// console (proxied), so its Tor-only sidebar renders a working "Clearnet" switch
// back (a link the parent intercepts) instead of a static indicator. It is NOT
// set when the .onion is opened directly in Tor Browser, so that path stays a
// pure Tor world with no clearnet affordance.
const EnvParentConsole = "VAYUOS_PARENT_CONSOLE"

// FromParentConsole reports whether this child is being managed from the parent
// clearnet console (proxied), enabling the in-console "back to Clearnet" switch.
func FromParentConsole() bool { return os.Getenv(EnvParentConsole) == "1" }

// IsSpaceChild reports whether THIS process is a Tor-Space child instance.
func IsSpaceChild() bool { return os.Getenv(EnvSpaceChild) == "1" }

// ChildSpaceRoot returns the child's isolated data root, ALWAYS under the
// parent's data dir (the directory of its DB) so the child inherits the parent
// unit's systemd ReadWritePaths with no new sandbox grant. parentDBPath is the
// parent's DB_PATH.
func ChildSpaceRoot(parentDBPath string) string {
	return filepath.Join(filepath.Dir(parentDBPath), "tor-space")
}

// NewAPIKey returns a fresh random API key (32 bytes hex) for the child, so the
// two worlds never share the parent's key — which would both leak a secret and
// correlate the worlds. Returns "" only if the system CSPRNG fails.
func NewAPIKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// BuildChildEnv builds the CURATED environment for the Tor-Space child. It does
// NOT inherit the parent's environment (which carries API_KEY and other secrets
// that would correlate the worlds); it whitelists only PATH and constructs every
// writable path under root, pinning the child to Tor mode on its own loopback
// port with its own distinct API key.
//
//	root   — the child data root (see ChildSpaceRoot)
//	onion  — the child's .onion (its DOMAIN); "" until the onion is minted
//	port   — the child's loopback HTTP port (unprivileged)
//	apiKey — a DISTINCT key (see NewAPIKey); never the parent's
//
// Every path that could otherwise default to a clearnet-shared location
// (DB_PATH, CACHE_DIR, MEDIA_DIR, STATIC_DIR, LOG_DIR, TMP_DIR) is pinned under
// root so no content, media or temp file can bleed between the two worlds.
func BuildChildEnv(root, onion string, port int, apiKey string) []string {
	env := []string{
		"VAYUOS_MODE=tor",
		EnvSpaceChild + "=1",
		"VAYUOS_TOR=off", // the parent mints this world's onion; the child runs no onion engine
		"PORT=" + strconv.Itoa(port),
		"DB_PATH=" + filepath.Join(root, "vayupress.db"),
		"CACHE_DIR=" + filepath.Join(root, "cache"),
		"MEDIA_DIR=" + filepath.Join(root, "media"),
		"STATIC_DIR=" + filepath.Join(root, "static"),
		"LOG_DIR=" + filepath.Join(root, "logs"),
		"TMP_DIR=" + filepath.Join(root, "tmp"),
		"API_KEY=" + apiKey,
		// The child is reachable from the parent's clearnet console (proxied), so
		// its Tor-only sidebar offers a working switch back to Clearnet.
		EnvParentConsole + "=1",
	}
	if onion != "" {
		env = append(env, "DOMAIN="+onion)
	}
	// Whitelist ONLY PATH from the parent so the child can resolve its runtime;
	// nothing secret rides along.
	if p := os.Getenv("PATH"); p != "" {
		env = append(env, "PATH="+p)
	}
	return env
}
