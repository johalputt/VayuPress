// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"io"
	"log"
	"os"
	"testing"
)

// TestMain drops info-level log lines for the whole package. Each test database
// logs its ninety-five migrations at info, so a full run printed tens of
// thousands of lines, and CI's log view — the last five thousand — no longer
// reached the line naming the failing test. Warnings and errors still print:
// they are what explains a failure.
func TestMain(m *testing.M) {
	log.SetOutput(dropInfo{os.Stderr})
	os.Exit(m.Run())
}

// dropInfo passes through every log write except a structured info line.
type dropInfo struct{ w io.Writer }

func (d dropInfo) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`{"level":"info"`)) {
		return len(p), nil
	}
	return d.w.Write(p)
}

func TestTestLogsKeepWarningsAndErrors(t *testing.T) {
	var buf bytes.Buffer
	d := dropInfo{&buf}
	for _, line := range []string{
		`2026/09/24 {"level":"info","component":"migrations","msg":"applied: 001"}`,
		`2026/09/24 {"level":"warn","component":"x","msg":"kept"}`,
		`2026/09/24 {"level":"error","component":"x","msg":"kept too"}`,
		`plain log line, kept`,
	} {
		if _, err := d.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	out := buf.String()
	if bytes.Contains(buf.Bytes(), []byte("applied: 001")) {
		t.Error("an info line reached the test output")
	}
	for _, want := range []string{`"level":"warn"`, `"level":"error"`, "plain log line"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("%s was dropped; only info lines may be", want)
		}
	}
}
