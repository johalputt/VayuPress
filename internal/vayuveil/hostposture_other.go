// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package vayuveil

// hostposture_other.go — every non-Linux build.
//
// There is no sysfs to read. Supported=false renders every row unverified, which
// is the honest answer; returning a zero value that merely LOOKS like "Secure
// Boot off" would report a finding on a platform where the question was never
// asked.

// ReadHostPosture returns the unknown state.
func ReadHostPosture() HostPosture { return HostPosture{Supported: false} }
