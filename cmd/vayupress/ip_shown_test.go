// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/johalputt/vayupress/internal/db"
)

// An address in shown text is masked as Sign-in attempts masks one; what
// only looks like one (a time, a version, a count) is left as it is.
func TestAnAddressShownIsMasked(t *testing.T) {
	for in, want := range map[string]string{
		"223.184.220.17":                   "223.184.x.x",
		"self-service from 223.184.220.17": "self-service from 223.184.x.x",
		"2001:db8:85a3::8a2e:370:7334":     "2001:db8:x:x::",
		"08:38":                            "08:38",
		"v3.18.44":                         "v3.18.44",
		"reason=forgot sessions_revoked=2": "reason=forgot sessions_revoked=2",
	} {
		if got := ipsMasked(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// The reset trail shows who asked by a masked address; the audit row it
// reads keeps the whole one.
func TestTheResetTrailShowsNoWholeAddress(t *testing.T) {
	openMigratedDB(t)
	if _, err := dbpkg.DB.Exec(`INSERT INTO audit_log(ts,action,actor,target,detail) VALUES(?,?,?,?,?)`,
		time.Now().UTC(), "vayumail.password.reset", "223.184.220.17", "hello@example.com", "reason=forgot"); err != nil {
		t.Fatal(err)
	}
	out := resetTrailHTML(context.Background())
	if !strings.Contains(out, "223.184.x.x") || strings.Contains(out, "220.17") {
		t.Errorf("the trail shows %q", out)
	}
	var actor string
	if err := dbpkg.DB.QueryRow(`SELECT actor FROM audit_log WHERE action='vayumail.password.reset'`).Scan(&actor); err != nil || actor != "223.184.220.17" {
		t.Errorf("the record keeps %q (%v)", actor, err)
	}
}
