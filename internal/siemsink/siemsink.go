// SPDX-License-Identifier: Apache-2.0

// Package siemsink writes VayuShield decisions to a local file in ArcSight
// Common Event Format (CEF), so an operator's existing log shipper — filebeat,
// fluentd, rsyslog imfile, anything that tails a file — can feed their SIEM
// without this binary growing network egress, a queue, or a vendor SDK
// (2025 plan Wave 4). Disabled entirely unless the operator sets a path.
package siemsink

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	// maxBytes rotates the active file at 10 MiB. One rotated generation
	// (path → path.1) is kept: enough for a slow shipper to catch up, small
	// enough that an unmonitored install cannot fill a disk.
	maxBytes = 10 << 20

	vendor  = "VayuPress"
	product = "VayuShield"
)

// Sink appends CEF lines to a rotating file. Safe for concurrent use; every
// write is one locked WriteString + Sync-free append (the OS flushes; SIEM
// consumers tolerate at-least-once far better than they tolerate latency).
type Sink struct {
	mu      sync.Mutex
	f       *os.File
	path    string
	version string
	written int64
}

// New opens (or creates) the sink file for appending.
func New(path, version string) (*Sink, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Sink{f: f, path: path, version: version, written: info.Size()}, nil
}

// Emit records one decision. event names the CEF signature (e.g. "BLOCK"),
// src is the source address as the shield keyed it, detail is free text.
// A write error is swallowed by design: telemetry must never take down the
// request path it is reporting on.
func (s *Sink) Emit(event, src, detail string) {
	if s == nil {
		return
	}
	sev := severity(event)
	line := fmt.Sprintf("CEF:0|%s|%s|%s|%s|%s|%d|src=%s duser=%s msg=%s\n",
		vendor, product, cefEscape(s.version), cefEscape(event), cefEscape(detail), sev,
		cefEscape(src), "-", cefEscape(event))

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return
	}
	if s.written > maxBytes {
		s.rotateLocked()
	}
	n, _ := s.f.WriteString(line)
	s.written += int64(n)
}

// Close flushes and closes the underlying file.
func (s *Sink) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Close()
	s.f = nil
	return err
}

// rotateLocked renames the active file to path.1 and reopens. Callers hold mu.
func (s *Sink) rotateLocked() {
	_ = s.f.Close()
	_ = os.Rename(s.path, s.path+".1")
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		s.f = nil // sink dead until process restart; requests unaffected
		s.written = 0
		return
	}
	s.f = f
	s.written = 0
}

// severity maps events to the CEF 0–10 scale.
func severity(event string) int {
	switch {
	case strings.HasPrefix(event, "BLOCK"), event == "JAIL":
		return 9
	case event == "TARPIT":
		return 8
	case strings.HasPrefix(event, "CHALLENGE"):
		return 5
	case event == "REFUSED":
		return 7
	default:
		return 3
	}
}

// cefEscape strips the pipe and backslash characters CEF reserves, so a
// crafted UA or path cannot forge extra extension fields inside one line.
func cefEscape(s string) string {
	r := strings.NewReplacer("|", "\\|", "\\", "\\\\", "\n", " ", "\r", " ", "=", "\\=")
	return r.Replace(s)
}
