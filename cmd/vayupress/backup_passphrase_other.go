// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package main

// hideInput is a no-op away from Linux, where the termios ioctl names differ.
// It reports ok=false so the prompt warns that input will be visible instead of
// pretending otherwise — the deployment target is Linux, and an operator on
// another platform is told plainly to use the env var or a passphrase file.
func hideInput() (hidden bool, restore func(), ok bool) { return false, nil, false }
