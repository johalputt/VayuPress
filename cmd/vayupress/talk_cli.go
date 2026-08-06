// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	dbpkg "github.com/johalputt/vayupress/internal/db"
	"github.com/johalputt/vayupress/internal/settings"
)

// talk_cli.go — `vayupress talk …`, the narrow surface the privileged VayuTalk
// subdomain helper records its result through (ADR-0155 P2).
//
// # Why this exists at all
//
// scripts/setup-talk-subdomain.sh used to publish the talk hostname by writing
// VAYUOS_TALK_HOST into /etc/vayupress/env and then restarting the whole install,
// because a process's environment cannot change without an exec. nginx has no
// queue in front of :8080, so every second of that restart was a 502 for every
// visitor — a full outage to advertise a hostname.
//
// Writing a SETTING instead needs no restart: the running server re-reads the
// settings table on a thirty-second TTL, so the new host is advertised on the
// next request with nothing interrupted.
//
// # Why a narrow command and not `settings set <key> <value>`
//
// A general settings writer would be a single root-runnable command that can
// change any value in the install — the theme, a threshold, a gate. This one can
// set one key to one kind of value and is refused anything else, which is the
// same shape `domains set-tls` already has and for the same reason: the helper
// needs to record exactly one fact, so that is exactly what it is given.
//
//	vayupress talk set-host <host>   # advertise <host> for the VayuTalk relay
//	vayupress talk set-host ""       # advertise none (falls back to the mail domain)
//	vayupress talk host              # print what is currently advertised
func runTalkCLI(args []string, out io.Writer) error {
	store := settings.New(dbpkg.DB)
	ctx := context.Background()
	sub := "host"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "host", "":
		fmt.Fprintln(out, store.Get(ctx, settings.ForPrimary(), settings.KeyTalkHost))
		return nil

	case "set-host":
		if len(args) < 2 {
			return fmt.Errorf(`usage: vayupress talk set-host <host>   (empty string advertises none)`)
		}
		host := strings.ToLower(strings.TrimSpace(args[1]))
		if host != "" {
			if err := validTalkHost(host); err != nil {
				return err
			}
		}
		if err := store.SetMany(ctx, settings.ForPrimary(),
			map[string]string{settings.KeyTalkHost: host}); err != nil {
			return err
		}
		if host == "" {
			fmt.Fprintln(out, "talk host cleared; the app will advertise the mail domain")
			return nil
		}
		// Says the delay out loud. "Advertised" and "advertised in a moment" are
		// different claims, and an operator watching a client pick the host up
		// needs to know which one they were given.
		fmt.Fprintf(out, "talk host set to %s; advertised within %s, no restart\n",
			host, settings.CacheTTL)
		return nil
	}
	return fmt.Errorf("unknown talk subcommand %q (want host|set-host)", sub)
}

// validTalkHost refuses anything that is not a plain hostname.
//
// The value is published in autoconfig and a client connects to it, so a
// malformed or hostile value is not a cosmetic problem — it is where a client
// gets pointed. This runs as root from a helper, and a helper that passes
// through whatever it was handed is how a shell quoting slip becomes a redirect.
func validTalkHost(h string) error {
	if len(h) > 253 {
		return fmt.Errorf("talk host is too long to be a hostname")
	}
	// No scheme, no path, no port, no credentials — a bare host and nothing else.
	for _, bad := range []string{"/", ":", "@", " ", "\t", "?", "#", "\\"} {
		if strings.Contains(h, bad) {
			return fmt.Errorf("talk host %q must be a bare hostname: no scheme, port, path or credentials", h)
		}
	}
	if !strings.Contains(h, ".") || strings.HasPrefix(h, ".") || strings.HasSuffix(h, ".") ||
		strings.Contains(h, "..") {
		return fmt.Errorf("talk host %q is not a valid hostname", h)
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return fmt.Errorf("talk host %q contains %q, which is not valid in a hostname", h, r)
		}
	}
	return nil
}
