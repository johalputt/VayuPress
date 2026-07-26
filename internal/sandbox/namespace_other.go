// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package sandbox

func namespaceCloneflags(m Manifest) uintptr { return 0 }
