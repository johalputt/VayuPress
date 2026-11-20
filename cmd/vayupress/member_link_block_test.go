// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/johalputt/vayupress/internal/signin"
)

// Each request for a member's sign-in link is recorded on Sign-in attempts,
// and a source blocked there, with no end, is refused the next link as
// silently as the budgets refuse: the same answer, and no link made. Another
// source is still served.
func TestABlockedSourceIsMailedNoSignInLink(t *testing.T) {
	a := credentialFloodApp(t)
	st := withSignins(t, a)
	const abuser, reader = "198.51.100.7", "203.0.113.9"

	first := postMemberLogin(t, a, abuser, "someone@elsewhere.test")
	if err := st.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Attempts(context.Background(), "", "", 10)
	if err != nil || len(rows) != 1 || rows[0].Door != signin.DoorLink || rows[0].Stage != signin.StageLink || rows[0].Masked != "198.51.x.x" {
		t.Fatalf("the request was recorded as %+v (%v)", rows, err)
	}
	if _, err := st.BlockSource(context.Background(), rows[0].FP, false, false, "", 0, "admin", reader); err != nil {
		t.Fatal(err)
	}

	again := postMemberLogin(t, a, abuser, "another@elsewhere.test")
	if n := countLoginTokens(t, a, "another@elsewhere.test"); n != 0 {
		t.Errorf("a blocked source was made %d link(s)", n)
	}
	if again.Code != first.Code || again.Body.String() != first.Body.String() {
		t.Errorf("a blocked source was answered differently: %d %q, then %d %q", first.Code, first.Body, again.Code, again.Body)
	}
	postMemberLogin(t, a, reader, "reader@elsewhere.test")
	if n := countLoginTokens(t, a, "reader@elsewhere.test"); n != 1 {
		t.Errorf("another source was made %d links, want 1", n)
	}
}
