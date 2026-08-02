// SPDX-License-Identifier: Apache-2.0

package main

// handlers_client.go — the agency CLIENT confinement (ADR-0152).
//
// A client is a paying customer of a studio that hosts their site: they get a
// login to this console and must reach their own mail, their own website and
// their own traffic, for the single domain they are bound to, and nothing else.
// No blog, no editor, no media, no server pages, no API keys, no mailbox
// creation, and no other client's anything.
//
// WHY THIS IS NOT A NEW RUNG ON THE EXISTING LADDER
//
// osPathMinLevel ranks paths on a linear scale (author < editor < admin) and
// resolves an UNENUMERATED non-API /os page to the PERMISSIVE author default.
// That default is right for staff — those pages are navigational and the
// sensitive ones are admin-gated by name — and it is lethal for a principal the
// operator does not employ. A page added next year would be reachable by every
// client on the install, silently, with no diff that looks like a security
// change.
//
// So confinement inverts the default: a client reaches ONLY what is explicitly
// declared for them, and anything undeclared is refused. Forgetting is safe.

import (
	"net/http"
	"strings"
)

// osAudience declares who an /os prefix is for.
//
// The ZERO VALUE IS NOT A VALID ANSWER: audienceUnset means nobody declared
// this, and every consumer treats it as a refusal rather than as a default. A
// table entry that omits its audience does not quietly inherit anything — it is
// caught at process start by mustValidateClientSurface.
type osAudience uint8

const (
	audienceUnset    osAudience = iota // zero value — never valid
	audienceOperator                   // operator and staff only
	audienceClient                     // also reachable by a bound client session
)

func (a osAudience) valid() bool { return a == audienceOperator || a == audienceClient }

// confinement classifies a resolved session. Zero value invalid for the same
// reason: an unclassified session is refused, not served.
type confinement uint8

const (
	confineUnset    confinement = iota
	confineNone                 // operator / staff — the historic path
	confineMailOnly             // a mailbox-only principal (mailOnlyPathAllowed)
	confineClient               // an agency client, bound to one domain
)

// clientSurfaceEntry is one declared prefix. Matching is exact-or-child:
// "/os/mysite" matches "/os/mysite" and "/os/mysite/anything", never
// "/os/mysite-secret" — a prefix test without that separator rule is how an
// allowlist accidentally admits a sibling route.
type clientSurfaceEntry struct {
	Prefix   string
	Audience osAudience
}

// clientSurface is the COMPLETE set of /os prefixes a bound client may reach.
//
// Keep it small and keep it explicit. Every addition is a decision about what a
// customer of the studio can see, and the safe answer for anything not on this
// list is already "no".
var clientSurface = []clientSurfaceEntry{
	// Their own site: brand, and the facts about what is live.
	{Prefix: "/os/mysite", Audience: audienceClient},
	{Prefix: "/os/api/mysite", Audience: audienceClient},
	// Their own mailbox. NOT /os/vayumail, which carries accounts/create,
	// accounts/delete, accounts/update, the DNS panel and the security page —
	// widening this entry by those eight characters re-admits all of it.
	{Prefix: "/os/vayumail/inbox", Audience: audienceClient},
	{Prefix: "/os/vayumail/message", Audience: audienceClient},
	{Prefix: "/os/vayumail/compose", Audience: audienceClient},
	{Prefix: "/os/vayumail/sent", Audience: audienceClient},
	{Prefix: "/os/vayumail/connect", Audience: audienceClient},
	{Prefix: "/os/api/vayuos/mail/", Audience: audienceClient},
	// Their own account and the way out.
	{Prefix: "/os/profile", Audience: audienceClient},
	{Prefix: "/os/change-password", Audience: audienceClient},
	{Prefix: "/os/logout", Audience: audienceClient},
	// Assets the pages above need in order to render.
	{Prefix: "/os/static", Audience: audienceClient},
}

// mustValidateClientSurface panics at process start if any entry is malformed or
// left its audience unset.
//
// A panic here is deliberate. The alternative is a table entry that silently
// means nothing, which for an allowlist is indistinguishable from a working one
// until a client reaches something they should not. Failing to boot is loud,
// immediate, and impossible to deploy past.
func mustValidateClientSurface() {
	seen := make(map[string]bool, len(clientSurface))
	for _, e := range clientSurface {
		if !e.Audience.valid() {
			panic("clientSurface: entry " + e.Prefix + " declares no audience; the zero value is not a valid answer")
		}
		if e.Prefix == "" || !strings.HasPrefix(e.Prefix, "/os") {
			panic("clientSurface: entry " + e.Prefix + " is not an /os prefix")
		}
		if strings.HasSuffix(e.Prefix, "/") && e.Prefix != "/os/api/vayuos/mail/" {
			panic("clientSurface: entry " + e.Prefix + " has a trailing slash; matching is exact-or-child")
		}
		if seen[e.Prefix] {
			panic("clientSurface: duplicate entry " + e.Prefix)
		}
		seen[e.Prefix] = true
	}
}

func init() { mustValidateClientSurface() }

// clientPathAllowed reports whether a bound client may reach path.
//
// Deny by default: a path matching no declared prefix is refused. This is the
// whole inversion — nothing needs to be added here for a new page to be safe.
func clientPathAllowed(path string) bool {
	for _, e := range clientSurface {
		if e.Audience != audienceClient {
			continue
		}
		if strings.HasSuffix(e.Prefix, "/") {
			if strings.HasPrefix(path, e.Prefix) {
				return true
			}
			continue
		}
		if path == e.Prefix || strings.HasPrefix(path, e.Prefix+"/") {
			return true
		}
	}
	return false
}

// clientScopeFor returns the domain a client session is bound to.
//
// ok is false for an identity that must not be served at all: a client with no
// binding. That combination is invalid rather than defaulted, because the empty string is the
// PRIMARY domain's sentinel everywhere else in this codebase — defaulting a
// client to it would hand a customer the agency's own install.
func clientScopeFor(role, clientDomainID string) (scope string, ok bool) {
	if role != roleClientName {
		return "", false
	}
	id := strings.TrimSpace(clientDomainID)
	if id == "" || !isHexScope(id) {
		return "", false
	}
	return id, true
}

// roleClientName is the client role's stored value. Declared here rather than
// imported at each use so the confinement logic reads without a package prefix.
const roleClientName = "client"

// denyClient refuses a confined client. Browser navigation lands on the page
// they do have; anything scripted gets a 403 so a stale tab cannot keep trying.
func (a *App) denyClient(w http.ResponseWriter, r *http.Request) {
	a.denyAccess(w, r, "/os/mysite")
}
