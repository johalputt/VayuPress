// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package vayuveil

// hardening_other.go — every non-Linux build.
//
// The honest answer on a platform whose dumpability this binary cannot set or
// read is "unknown", never "fine". dumpableState returns known=false, and the
// report renders that as unverified rather than quietly crediting the platform
// with a protection nobody checked.

const hardeningSupported = false

func setUndumpable() error { return errUnsupported }

func dumpableState() (undumpable bool, known bool) { return false, false }

func setCoreLimitZero() error { return errUnsupported }

func coreLimitState() (zero bool, known bool) { return false, false }

var errUnsupported = unsupportedError{}

type unsupportedError struct{}

func (unsupportedError) Error() string {
	return "refusing to be dumped is not supported on this platform"
}

// nonblockFlag — see the Linux file for why the capture suite needs it. Zero on
// platforms without it; the suite still runs, and a blocking device there would
// be a platform-specific hazard this binary cannot avoid.
const nonblockFlag = 0
