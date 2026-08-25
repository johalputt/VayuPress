// SPDX-License-Identifier: Apache-2.0

package siemsink

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmitWritesCEFLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shield.cef")
	s, err := New(path, "3.17.58")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	s.Emit("BLOCK", "198.51.100.7", "score=0.90 type=BadBot")
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	line := string(b)
	for _, want := range []string{"CEF:0|VayuPress|VayuShield|3.17.58|BLOCK|", "|9|", "src=198.51.100.7"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %q in %q", want, line)
		}
	}
}

func TestEmitEscapesPipes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.cef")
	s, _ := New(path, "1")
	// A detail forged from a hostile UA must not be able to inject extension
	// fields or extra header segments into the CEF line.
	s.Emit("CHALLENGE_POW", "203.0.113.5", "ua=evil|src=9.9.9.9|sh=0")
	_ = s.Close()
	b, _ := os.ReadFile(path)
	line := string(b)
	// Escaped pipes (\|) legitimately contain the '|' byte; only UNESCAPED
	// separators count. A compliant CEF:0 line has exactly 7: six header
	// separators (version|vendor|product|version|signature|name|severity)
	// plus one extension separator.
	unescaped := strings.Count(line, "|") - strings.Count(line, `\|`)
	if unescaped != 7 {
		t.Fatalf("pipe injection: %d unescaped pipes in %q", unescaped, line)
	}
	if !strings.Contains(line, `evil\|src\=9.9.9.9`) {
		t.Fatalf("detail not escaped: %q", line)
	}
}

func TestRotationKeepsOneGeneration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.cef")
	s, _ := New(path, "1")
	big := strings.Repeat("x", 512)
	for i := 0; i < (maxBytes/512)+10; i++ {
		s.Emit("BLOCK", "1.2.3.4", big)
	}
	_ = s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active file missing after rotation: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated generation missing: %v", err)
	}
}

func TestNilSinkIsSafe(t *testing.T) {
	var s *Sink
	s.Emit("BLOCK", "1.1.1.1", "no-op") // must not panic
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
