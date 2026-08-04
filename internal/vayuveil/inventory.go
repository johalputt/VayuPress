// SPDX-License-Identifier: Apache-2.0

package vayuveil

// inventory.go — what is actually present on the running machine.
//
// The registry says what policy WOULD be. This says what is there. The two are
// diffed, and an interface present in the system but absent from the registry is
// a failing test rather than a surprise — ADR-0150 §3.1's exhaustiveness
// property.
//
// Every probe reads the machine through an injected Host rather than touching
// the filesystem directly, so the inventory is a pure function of an observation.
// A probe that reads the real machine inside a test asserts whatever the CI
// runner happens to look like, which is not an assertion.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Presence is what a probe found. The zero value is Unknown, deliberately: a
// probe that could not determine an answer must not report "absent", because
// absent reads as safe and unknown does not.
type Presence uint8

const (
	// PresenceUnknown — could not be determined from inside this binary.
	PresenceUnknown Presence = iota
	// PresenceAbsent — the interface is not on this machine.
	PresenceAbsent
	// PresentUnreachable — the interface exists but this process cannot open it.
	// Worth distinguishing: it is the difference between "no camera in the room"
	// and "a camera this particular process happens not to hold".
	PresentUnreachable
	// PresentReachable — the interface exists and this process can use it. On a
	// channel whose Disposition is Deny or Absent, this is the finding.
	PresentReachable
)

func (p Presence) String() string {
	switch p {
	case PresenceAbsent:
		return "absent"
	case PresentUnreachable:
		return "present, not reachable by this process"
	case PresentReachable:
		return "present and reachable by this process"
	default:
		return "unknown"
	}
}

// Observation is one probe's result.
type Observation struct {
	Presence Presence
	// Detail is what was actually looked at, in the operator's terms. A verdict
	// with no evidence behind it cannot be checked, and a posture report nobody
	// can check is decoration.
	Detail string
}

// Host is the view of the running machine that probes read.
type Host struct {
	// Exists reports whether a path is there at all.
	Exists func(path string) bool
	// Readable reports whether THIS process can open the path for reading. This is
	// the question that matters: a device node nobody can open is a different
	// posture from one this process holds.
	Readable func(path string) bool
	// Glob expands a shell pattern, returning nothing on error.
	Glob func(pattern string) []string
	// Env reads an environment variable.
	Env func(key string) string
	// ReadFile returns a small file's contents, or "" if unreadable.
	ReadFile func(path string) string
}

// LiveHost reads the actual machine.
func LiveHost() Host {
	return Host{
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		Readable: func(p string) bool {
			f, err := os.Open(p) // #nosec G304 -- fixed probe paths from the registry
			if err != nil {
				return false
			}
			_ = f.Close()
			return true
		},
		Glob: func(pat string) []string {
			m, err := filepath.Glob(pat)
			if err != nil {
				return nil
			}
			return m
		},
		Env: os.Getenv,
		ReadFile: func(p string) string {
			b, err := os.ReadFile(p) // #nosec G304 -- fixed probe paths from the registry
			if err != nil {
				return ""
			}
			return string(b)
		},
	}
}

// probeGlob reports on a family of device nodes: absent if none match, reachable
// if this process can open any of them, present-but-unreachable otherwise.
func probeGlob(h Host, pattern, what string) Observation {
	matches := h.Glob(pattern)
	if len(matches) == 0 {
		return Observation{PresenceAbsent, "no " + what + " matching " + pattern}
	}
	var open []string
	for _, m := range matches {
		if h.Readable(m) {
			open = append(open, m)
		}
	}
	if len(open) > 0 {
		return Observation{PresentReachable,
			strings.Join(open, ", ") + " can be opened for reading by this process"}
	}
	return Observation{PresentUnreachable,
		strconv.Itoa(len(matches)) + " node(s) match " + pattern + "; none openable by this process"}
}

// probeSocket reports on a display-server or bus socket named by an env var, a
// path, or both.
func probeSocket(h Host, envKey, path, what string) Observation {
	// The variable is reported as SET, never printed. A session-bus or AT-SPI
	// address carries the runtime directory and the numeric UID, and this string
	// is rendered straight onto a console page that gets screenshotted into
	// support threads. The operator needs to know a session is addressable; the
	// socket path tells them nothing more and tells a reader over their shoulder
	// rather more.
	if envKey != "" {
		if strings.TrimSpace(h.Env(envKey)) != "" {
			return Observation{PresentReachable, envKey + " is set — a " + what + " is addressable from this process"}
		}
	}
	if path != "" && h.Exists(path) {
		if h.Readable(path) {
			return Observation{PresentReachable, path + " exists and is openable"}
		}
		return Observation{PresentUnreachable, path + " exists but is not openable by this process"}
	}
	return Observation{PresenceAbsent, "no " + what + " (" + envKey + " unset, " + path + " absent)"}
}

// Inventory runs every registered channel's probe against a host view.
//
// A channel with no probe is recorded as Unknown rather than omitted. Omitting
// it would let a caller iterate the map and see a clean sweep, which is the
// difference between "we looked and found nothing" and "we did not look".
func Inventory(h Host) map[ChannelID]Observation {
	out := make(map[ChannelID]Observation, len(channels))
	for _, c := range channels {
		if c.Probe == nil {
			out[c.ID] = Observation{PresenceUnknown,
				"no probe exists for this channel; presence cannot be determined from inside this binary"}
			continue
		}
		out[c.ID] = c.Probe(h)
	}
	return out
}
