// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package vayuveil

import "testing"

func raiseCoreLimitForTest(*testing.T) bool { return false }
