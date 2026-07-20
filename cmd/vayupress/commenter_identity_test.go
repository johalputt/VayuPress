package main

import (
	"testing"

	"github.com/johalputt/vayupress/internal/members"
)

// TestCommenterIdentityCan pins the unified access rule that keeps the public
// member UI and the member-gated APIs in agreement: an operator has full access,
// a paid reader clears the paid tier, a signed-in reader clears members-only, and
// an anonymous visitor clears only public content.
func TestCommenterIdentityCan(t *testing.T) {
	operator := &commenterIdentity{Name: "Owner", Email: "o@x.com", Paid: true, Operator: true}
	paid := &commenterIdentity{Name: "Paid", Email: "p@x.com", Paid: true}
	free := &commenterIdentity{Name: "Free", Email: "f@x.com"}
	var anon *commenterIdentity // nil == not signed in

	cases := []struct {
		name  string
		who   *commenterIdentity
		level string
		want  bool
	}{
		{"operator/paid", operator, members.AccessPaid, true},
		{"operator/members", operator, members.AccessMembers, true},
		{"operator/public", operator, members.AccessPublic, true},
		{"paid/paid", paid, members.AccessPaid, true},
		{"paid/members", paid, members.AccessMembers, true},
		{"free/members", free, members.AccessMembers, true},
		{"free/paid", free, members.AccessPaid, false},
		{"anon/members", anon, members.AccessMembers, false},
		{"anon/paid", anon, members.AccessPaid, false},
		{"anon/public", anon, members.AccessPublic, true},
	}
	for _, c := range cases {
		if got := c.who.Can(c.level); got != c.want {
			t.Errorf("%s: Can(%q) = %v, want %v", c.name, c.level, got, c.want)
		}
	}
}
