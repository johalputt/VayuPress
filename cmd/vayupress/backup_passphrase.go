// SPDX-License-Identifier: Apache-2.0

package main

// backup_passphrase.go — resolving the backup passphrase without leaving it
// somewhere it can be read later.
//
// The passphrase is the only key to an encrypted backup, so where it comes from
// matters as much as how it is used. Three sources, in order of preference:
//
//  1. VAYU_BACKUP_PASSPHRASE — for unattended runs.
//  2. -passphrase-file — for automation that keeps secrets in files with real
//     permissions rather than in an environment inherited by every child.
//  3. An interactive prompt, with terminal echo disabled where the platform
//     allows it.
//
// It is never accepted as a command-line flag: argv is world-readable through
// /proc on Linux and lands in shell history.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// resolvePassphrase returns the backup passphrase from the first source that
// supplies one. fileFlag may be empty.
func resolvePassphrase(out io.Writer, fileFlag string) (string, error) {
	if p := os.Getenv("VAYU_BACKUP_PASSPHRASE"); strings.TrimSpace(p) != "" {
		return p, nil
	}
	if f := strings.TrimSpace(fileFlag); f != "" {
		b, err := os.ReadFile(f) //nolint:gosec // operator-supplied path, by design
		if err != nil {
			return "", fmt.Errorf("could not read the passphrase file: %w", err)
		}
		// Trim only the trailing newline an editor adds; interior spaces are
		// legitimate passphrase characters and must survive.
		p := strings.TrimRight(string(b), "\r\n")
		if p == "" {
			return "", errors.New("the passphrase file is empty")
		}
		return p, nil
	}
	return promptPassphrase(out)
}

// promptPassphrase reads a passphrase from the terminal, hiding it when the
// platform supports it. hideInput is provided per-platform.
func promptPassphrase(out io.Writer) (string, error) {
	hidden, restore, ok := hideInput()
	if ok {
		fmt.Fprint(out, "Backup passphrase: ")
	} else {
		fmt.Fprint(out, "Backup passphrase (WILL BE VISIBLE on this platform — prefer VAYU_BACKUP_PASSPHRASE or -passphrase-file): ")
	}
	defer func() {
		if restore != nil {
			restore()
		}
		if ok {
			fmt.Fprintln(out)
		}
	}()
	_ = hidden
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return "", errors.New("no passphrase provided")
	}
	p := strings.TrimRight(sc.Text(), "\r\n")
	if strings.TrimSpace(p) == "" {
		return "", errors.New("empty passphrase")
	}
	return p, nil
}
