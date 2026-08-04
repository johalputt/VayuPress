// SPDX-License-Identifier: Apache-2.0

package vayuflow

import (
	"testing"

	"github.com/johalputt/vayupress/internal/users"
)

// A client account is a VALID account role that is deliberately not a rung on
// the authority ladder (ADR-0152). The danger is a rank function that quietly
// gives it a number: `client` would then compare against a floor and either
// pass one it should not, or fail in a way that reads as a permissions bug.
//
// It must be refused as an answer, not ranked low.
func TestAClientCanNeverSatisfyAnAuthorityFloor(t *testing.T) {
	if validOwnerRole(users.RoleClient) {
		t.Error("client is on the flow-owner ladder; it must not be")
	}
	for _, floor := range []string{RoleAuthor, RoleEditor, RoleAdmin} {
		if ownerAtLeast(users.RoleClient, floor) {
			t.Errorf("a client satisfied the %s floor", floor)
		}
	}
	// And nothing satisfies a floor OF client, because that is not a question
	// with an answer.
	if ownerAtLeast(RoleAdmin, users.RoleClient) {
		t.Error("an admin satisfied a 'client' floor; that floor is not meaningful")
	}
}

func TestTheAuthorityLadderOrdersAsWritten(t *testing.T) {
	for _, tc := range []struct {
		have, need string
		want       bool
	}{
		{RoleAdmin, RoleAdmin, true},
		{RoleAdmin, RoleEditor, true},
		{RoleAdmin, RoleAuthor, true},
		{RoleEditor, RoleAdmin, false},
		{RoleEditor, RoleEditor, true},
		{RoleEditor, RoleAuthor, true},
		{RoleAuthor, RoleEditor, false},
		{RoleAuthor, RoleAuthor, true},
	} {
		if got := ownerAtLeast(tc.have, tc.need); got != tc.want {
			t.Errorf("ownerAtLeast(%q, %q) = %v, want %v", tc.have, tc.need, got, tc.want)
		}
	}
}

// An owner whose account was deleted, or whose role was set to something this
// build does not know, must fail closed. The alternative — treating an
// unrecognised role as "probably fine" — is how a flow keeps running with
// authority nobody holds.
func TestAnUnknownOrAbsentRoleFailsClosed(t *testing.T) {
	for _, have := range []string{"", " ", "superuser", "ADMIN", "owner"} {
		if ownerAtLeast(have, RoleAuthor) {
			t.Errorf("role %q satisfied the lowest floor; unknown roles must fail closed", have)
		}
	}
}

// MinOwnerRole reports the strongest floor across a flow's steps — the role its
// owner must still hold for the WHOLE flow to run. A flow is not partially
// authorised.
func TestMinOwnerRoleTakesTheStrongestFloor(t *testing.T) {
	f := goodFlow()
	f.Steps = []Step{
		{Action: "content.draft.create"},
		{Action: "content.draft.update"},
	}
	if got := f.MinOwnerRole(); got != RoleEditor {
		t.Errorf("MinOwnerRole = %q, want %q", got, RoleEditor)
	}
}
