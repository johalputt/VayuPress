// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

func TestVsBarWidthClass(t *testing.T) {
	cases := []struct {
		val, max int64
		want     string
	}{
		{0, 100, "w-0"},     // zero value → empty
		{50, 0, "w-0"},      // zero max → empty (no divide-by-zero)
		{100, 100, "w-100"}, // the max row → full
		{50, 100, "w-50"},
		{1, 100, "w-5"},     // tiny but real → floor at a visible sliver
		{99, 100, "w-95"},   // buckets down to the 5% step
		{200, 100, "w-100"}, // clamp over-100 (shouldn't happen, but safe)
		{-5, 100, "w-0"},    // negative → empty
	}
	for _, c := range cases {
		if got := vsBarWidthClass(c.val, c.max); got != c.want {
			t.Errorf("vsBarWidthClass(%d,%d) = %q, want %q", c.val, c.max, got, c.want)
		}
	}
}

func TestVsBarCellStructure(t *testing.T) {
	out := vsBarCell(42, 100)
	for _, want := range []string{`class="vs-barcell"`, `>42<`, `class="vs-bar__fill w-40"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("vsBarCell missing %q in:\n%s", want, out)
		}
	}
}

func TestMaxInt64(t *testing.T) {
	rows := []int64{3, 9, 1, 7}
	if got := maxInt64(len(rows), func(i int) int64 { return rows[i] }); got != 9 {
		t.Fatalf("maxInt64 = %d, want 9", got)
	}
	if got := maxInt64(0, func(i int) int64 { return 0 }); got != 0 {
		t.Fatalf("maxInt64(empty) = %d, want 0", got)
	}
}
