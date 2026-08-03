// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johalputt/vayupress/internal/config"
)

// The provisioning helper drives `vayupress domains hosts`, and that command
// required API_KEY — a value it never uses. The systemd unit carried no
// EnvironmentFile, so the command exited
//
//	{"level":"fatal","component":"config","msg":"required env not set","key":"API_KEY"}
//
// before reading a single row, and an install provisioned no certificates for a
// week while every panel said the domain was approved.
//
// A configuration check strict enough to break a command that does not use the
// value it is checking protects nobody. Fixing it in the BINARY matters
// separately from fixing the shell: the binary is what the in-app updater can
// deliver, so an operator repairs this from VayuOS rather than from a terminal.

func TestALocalSubcommandDoesNotRequireAnAPIKey(t *testing.T) {
	t.Setenv("API_KEY", "")
	// Must not call log.Fatal. If it does, the test binary exits and the failure
	// is unmissable.
	config.LoadLocalCLI()
	if config.Cfg.APIKey == "" {
		t.Fatal("a local subcommand left APIKey EMPTY. Empty compares equal to an absent " +
			"header, so any constant-time compare reached with it would authenticate a " +
			"request carrying no key at all")
	}
}

// And the value it does use must be one nothing can ever present.
func TestTheUnsetAPIKeyCanNeverMatchARequest(t *testing.T) {
	t.Setenv("API_KEY", "")
	config.LoadLocalCLI()
	k := config.Cfg.APIKey
	if !strings.Contains(k, "\x00") {
		t.Errorf("the placeholder key %q contains no NUL, so it is a value a caller could "+
			"conceivably send in a header", k)
	}
	// Nothing a client can put in an HTTP header equals it.
	for _, attempt := range []string{"", " ", "cli-no-api-key", "\\x00cli-no-api-key\\x00"} {
		if attempt == k {
			t.Errorf("a header value %q matches the placeholder key", attempt)
		}
	}
}

// The SERVING path must still refuse to start without a key. Relaxing that
// would serve an unauthenticated admin API, which is a far worse bug than the
// one being fixed.
func TestTheServerStillRequiresAnAPIKey(t *testing.T) {
	src, err := os.ReadFile(filepath.Clean("../../internal/config/config.go"))
	if err != nil {
		t.Skipf("config not readable: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, `func Load() { load(true) }`) {
		t.Error("the serving Load no longer demands an API key, so the server would start " +
			"without one and serve an unauthenticated admin API")
	}
	if !strings.Contains(s, `Cfg.APIKey = MustEnv("API_KEY")`) {
		t.Error("MustEnv is gone from the serving path entirely")
	}
}

// The subcommand the privileged helper actually drives must use the relaxed
// loader, or none of the above reaches the bug.
func TestTheDomainsSubcommandUsesTheLocalLoader(t *testing.T) {
	src := readSourceFile(t, "main.go")
	i := strings.Index(src, `os.Args[1] == "domains"`)
	if i < 0 {
		t.Fatal("the domains subcommand is gone")
	}
	seg := src[i : i+600]
	if !strings.Contains(seg, "config.LoadLocalCLI()") {
		t.Fatal("the domains subcommand still calls config.Load(), so it dies on a missing " +
			"API_KEY and the provisioning helper still reports nothing to do")
	}
}
