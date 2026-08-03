// SPDX-License-Identifier: Apache-2.0

package settings

// scope.go — whose settings these are (ADR-0153 D1).
//
// Every setting in this store used to be install-wide. `Get(ctx, key)` took no
// scope, so it could not be wrong at a call site, so nothing ever forced a
// feature to consider which hosted domain it was serving. Around sixty call
// sites inherited the primary domain's configuration by default, silently, and
// each new feature inherited it too. The visible result was that a hosted
// domain was the operator's own site wearing a different name and three
// colours — every other key, from the theme to the SEO defaults to the
// newsletter, was the operator's.
//
// That is not a missing feature. It is a missing ARGUMENT. So the scope becomes
// one, and the zero value is not a valid answer: a caller that reaches this
// store without saying whose settings it wants does not get the primary's, and
// a caller added next year cannot silently inherit them, because there is
// nothing to inherit from.
//
// The direction of the fallback is the product decision, and it is deliberate
// (ADR-0153 D2): an unset key falls back to the compiled-in DEFAULT, never to
// the primary's stored value. Falling back to Defaults is falling back to the
// product. Falling back to the primary is falling back to another tenant's
// data, and it is exactly what made hosted domains feel linked.

import "strings"

// scopeKind identifies whose settings a Scope selects. The zero value is not a
// valid answer — see the file comment.
type scopeKind uint8

const (
	scopeUnset   scopeKind = iota // zero value — never valid
	scopePrimary                  // the operator's own install
	scopeDomain                   // one hosted domain
)

// Scope is whose settings a read or write applies to.
//
// It is deliberately opaque and constructed only through the functions below,
// so a caller cannot assemble one field by field and end up addressing a domain
// nobody named.
type Scope struct {
	kind     scopeKind
	domainID string
}

// ForPrimary is the operator's own install — the domain this binary was
// configured with, and the scope every setting written before ADR-0153 belongs
// to.
func ForPrimary() Scope { return Scope{kind: scopePrimary} }

// ForDomain is one hosted domain's own settings.
//
// An empty id yields an UNSET scope rather than the primary. That is the whole
// point: "" is the primary domain's sentinel throughout this codebase, so
// resolving a blank id to the primary would hand a hosted domain the operator's
// configuration — silently, and looking exactly like working code. This has
// already been the shape of two separate defects in ADR-0152.
func ForDomain(id string) Scope {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return Scope{}
	}
	return Scope{kind: scopeDomain, domainID: id}
}

// Valid reports whether an authority was actually supplied.
func (s Scope) Valid() bool {
	return s.kind == scopePrimary || s.kind == scopeDomain
}

// IsPrimary reports whether this scope addresses the operator's own install.
func (s Scope) IsPrimary() bool { return s.kind == scopePrimary }

// DomainID returns the hosted domain this scope addresses, or "" for the
// primary and for an unset scope.
func (s Scope) DomainID() string { return s.domainID }

// key is the storage and cache key for this scope: "" for the primary, so the
// rows written before ADR-0153 keep their meaning without being rewritten, and
// the domain id otherwise.
//
// It is unexported on purpose. A caller holding the string could build a scope
// by concatenation and reach another domain's settings; holding the Scope, they
// can only reach what they were given.
func (s Scope) key() string {
	if s.kind == scopeDomain {
		return s.domainID
	}
	return ""
}

// String renders a scope for logs and audit entries.
func (s Scope) String() string {
	switch s.kind {
	case scopePrimary:
		return "primary"
	case scopeDomain:
		return "domain:" + s.domainID
	default:
		return "unset"
	}
}
