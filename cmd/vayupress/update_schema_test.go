// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installed lays out what an apply leaves: the new binary at bin, the
// previous one beside it as bin.bak.
func installed(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "vayupress")
	if err := os.WriteFile(bin, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin+".bak", []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin
}

// steps stands in for the child and the re-exec, recording what ran in order.
func steps(t *testing.T, childErr error) *[]string {
	t.Helper()
	var ran []string
	oldApply, oldRestart := schemaApply, restartInto
	t.Cleanup(func() { schemaApply, restartInto = oldApply, oldRestart })
	schemaApply = func(context.Context, string) error { ran = append(ran, "schema"); return childErr }
	restartInto = func(string, time.Duration, func()) { ran = append(ran, "restart") }
	return &ran
}

func onDisk(t *testing.T, bin string) string {
	t.Helper()
	b, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestTheSwitchWaitsForTheDatabase — the new binary's migrations run before
// the re-exec, not after it: after it, they ran with the port closed (133 took
// johal.in down for 5 min 43 s).
func TestTheSwitchWaitsForTheDatabase(t *testing.T) {
	ran := steps(t, nil)
	bin := installed(t)
	(&App{}).activateUpdate(bin, "v3.18.33", 0)
	if got := strings.Join(*ran, ","); got != "schema,restart" {
		t.Errorf("an update ran %q, want the schema applied and then the restart", got)
	}
	if onDisk(t, bin) != "new" {
		t.Error("a prepared update did not keep the new binary")
	}
}

// TestADatabaseThatCannotBePreparedKeepsThisVersion — a failed child puts the
// previous binary back and does not restart: the new one would meet the same
// migration at its next start, with the port closed.
func TestADatabaseThatCannotBePreparedKeepsThisVersion(t *testing.T) {
	ran := steps(t, errors.New("exit status 1: migration 135 exec: disk full"))
	bin := installed(t)
	(&App{}).activateUpdate(bin, "v3.18.33", 0)
	if got := strings.Join(*ran, ","); got != "schema" {
		t.Errorf("a failed preparation went on to %q, want no restart", got)
	}
	if onDisk(t, bin) != "old" {
		t.Error("a failed preparation left the new binary in place")
	}
}

// TestAnOlderBinaryIsNotAskedForItsSchema — a release before schema apply
// does not know the word; it is switched to as before.
func TestAnOlderBinaryIsNotAskedForItsSchema(t *testing.T) {
	ran := steps(t, errors.New("unknown subcommand"))
	bin := installed(t)
	(&App{}).activateUpdate(bin, "v3.18.32", 0)
	if got := strings.Join(*ran, ","); got != "restart" {
		t.Errorf("an update to 3.18.32 ran %q, want only the restart", got)
	}
}

// TestTheChildsLastWordsAreKept — what the child printed on failure reaches the
// history row the Update page shows, so the operator reads the migration that
// failed, not just an exit status.
func TestTheChildsLastWordsAreKept(t *testing.T) {
	child := filepath.Join(t.TempDir(), "child")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"schema apply\" ] || exit 3\necho 'schema: migrations: migration 135 exec: disk full' >&2\nexit 1\n"
	if err := os.WriteFile(child, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	err := runSchemaApply(context.Background(), child)
	if err == nil || !strings.Contains(err.Error(), "migration 135 exec: disk full") || !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("the child's failure reads %v, want its exit status and what it printed", err)
	}
}
