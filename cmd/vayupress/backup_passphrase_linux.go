// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// hideInput turns off terminal echo so a typed passphrase does not land in the
// scrollback, a tmux capture, or over someone's shoulder. It returns a restore
// func and whether hiding actually took effect — the caller warns the operator
// when it did not, rather than silently echoing a secret.
func hideInput() (hidden bool, restore func(), ok bool) {
	fd := int(os.Stdin.Fd())
	before, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return false, nil, false // not a terminal (piped input) — nothing to hide
	}
	after := *before
	after.Lflag &^= unix.ECHO
	after.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &after); err != nil {
		return false, nil, false
	}
	return true, func() { _ = unix.IoctlSetTermios(fd, unix.TCSETS, before) }, true
}
