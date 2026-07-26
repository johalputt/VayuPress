// SPDX-License-Identifier: Apache-2.0

package main

import "testing"

func TestParseCgroupLimit(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantOK  bool
		comment string
	}{
		{"536870912\n", 536870912, true, "cgroup v2 finite limit"},
		{"  1073741824  ", 1073741824, true, "whitespace trimmed"},
		{"max\n", 0, false, "cgroup v2 unlimited sentinel"},
		{"", 0, false, "empty file"},
		{"not-a-number", 0, false, "garbage"},
		{"0", 0, false, "zero is not a usable limit"},
		{"-5", 0, false, "negative"},
		{"9223372036854771712", 0, false, "cgroup v1 near-max unlimited sentinel"},
	}
	for _, c := range cases {
		got, ok := parseCgroupLimit(c.in)
		if got != c.want || ok != c.wantOK {
			t.Errorf("parseCgroupLimit(%q) = (%d,%v), want (%d,%v) — %s",
				c.in, got, ok, c.want, c.wantOK, c.comment)
		}
	}
}
