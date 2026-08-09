// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

// The referrer self-host exclusion is a hook, and a hook nothing sets is a fix
// that ships inert. internal/analytics can only prove the exclusion is correct
// once it HAS the host list; that it ever gets one is a fact about this package.
//
// This is the same shape as the flusher that counts into a buffer nobody drains,
// pinned a few lines above the call it guards.
func TestTheAnalyticsStoreIsToldEveryHostThisInstallServes(t *testing.T) {
	src := readSourceFile(t, "main.go")
	if !strings.Contains(src, "a.analytics.UseSelfHosts(a.registeredHosts)") {
		t.Fatal("nothing supplies the host list to the analytics store, so the referrer " +
			"exclusion is back to the primary alone and every hosted domain reappears in the " +
			"operator's list as a referrer to itself")
	}

	body := goFuncBody(readSourceFile(t, "middleware_domain.go"), "registeredHosts")
	if body == "" {
		t.Fatal("registeredHosts is gone")
	}
	// Every registered host, not the mail-enabled or active subset: a domain
	// disabled today still produced the rows recorded while it was live, and
	// filtering by status would leave exactly those in the list.
	for _, wrong := range []string{"MailEnabled", "StatusActive", "IsPrimary"} {
		if strings.Contains(body, wrong) {
			t.Errorf("registeredHosts filters on %s. The question the exclusion asks is "+
				"'is this referrer us?', which does not depend on what the domain serves "+
				"today — the rows were recorded when it did", wrong)
		}
	}
}
