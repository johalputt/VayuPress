// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package vayuveil

// sandbox_other.go — every non-Linux build.
//
// There is no procfs to read and no systemd unit to have applied anything, so
// every field stays unknown and the report renders unverified. Returning a
// zero-valued SandboxState with Supported=false is the honest answer; returning
// one that merely LOOKS unhardened would report a missing control on a platform
// where the question does not apply.

// ReadSandbox returns the unknown state.
func ReadSandbox() SandboxState { return SandboxState{Supported: false} }
