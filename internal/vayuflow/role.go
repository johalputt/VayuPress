// SPDX-License-Identifier: Apache-2.0

package vayuflow

import "github.com/johalputt/vayupress/internal/users"

// The roles a flow owner can hold, re-exported so callers of this package do
// not have to reach into users just to name an authority floor.
const (
	RoleAdmin  = users.RoleAdmin
	RoleEditor = users.RoleEditor
	RoleAuthor = users.RoleAuthor
)

// roleRank orders the authority ladder. It is written out rather than derived,
// because the ladder in internal/users is NOT total: `client` is a real,
// valid account role that is deliberately not a rung on it (ADR-0152 — a client
// reaches a fixed enumerated surface scoped to one domain, which the linear
// ladder cannot express).
//
// A rank function that quietly gave `client` a number would be the same defect
// class as a contract whose zero value is a valid answer: the type answering a
// question nobody asked it. So `client` gets no rank, ownerAtLeast refuses it,
// and a client account can never own a flow — not because it ranks low, but
// because "how much authority does a client have on the content ladder" is not
// a question with an answer.
var roleRank = map[string]int{
	users.RoleAuthor: 1,
	users.RoleEditor: 2,
	users.RoleAdmin:  3,
}

// validOwnerRole reports whether a role can appear as a flow owner's role or as
// a capability's MinRole.
func validOwnerRole(role string) bool {
	_, ok := roleRank[role]
	return ok
}

// ownerAtLeast reports whether an owner holding `have` satisfies a floor of
// `need`. Both must be on the ladder; anything else — an empty string, a
// deleted account's blank role, `client`, or a role invented later — is refused
// rather than compared.
//
// This is called at RUN time, not at save time. A flow armed by an admin who is
// later demoted stops working, which is the whole reason the check lives here
// rather than in the editor.
func ownerAtLeast(have, need string) bool {
	h, okH := roleRank[have]
	n, okN := roleRank[need]
	if !okH || !okN {
		return false
	}
	return h >= n
}
