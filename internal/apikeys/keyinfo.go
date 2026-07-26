// SPDX-License-Identifier: Apache-2.0

package apikeys

import "time"

// KeyInfo is the resolved, enforcement-time view of an authenticated API key:
// everything a permission-checking middleware needs, and nothing sensitive (no
// raw token, no hash). Store.Resolve returns it on a successful verification, and
// it is stamped into the request context so downstream middleware/handlers can
// ask "may this key do <section>:<action>?" and read the per-key rate budget.
type KeyInfo struct {
	ID         string      // stable opaque key id
	Label      string      // human label (for audit lines)
	Scope      string      // ScopeInternal | ScopeExternal
	Owner      string      // owning users.id ("" = operator/system-owned)
	Perms      Permissions // fine-grained grant set
	ExpiresAt  *time.Time  // hard expiry, or nil for never
	RatePerMin int         // per-key request budget; 0 = use the global default
}

// SuperuserKeyInfo returns a KeyInfo that permits every capability, used for the
// static bootstrap key (config API_KEY) and the internal/system key so existing
// automation keeps working with no grants configured.
func SuperuserKeyInfo(id, label, scope string) KeyInfo {
	return KeyInfo{ID: id, Label: label, Scope: scope, Perms: Superuser()}
}

// Can reports whether this key may perform action a in section s. Internal-scope
// keys are implicit superusers regardless of their stored grants.
func (ki KeyInfo) Can(s Section, a Action) bool {
	if ki.Scope == ScopeInternal {
		return true
	}
	return ki.Perms.Has(s, a)
}

// IsSuperuser reports whether the key can do everything (internal scope or an
// explicit "*:*" grant).
func (ki KeyInfo) IsSuperuser() bool {
	return ki.Scope == ScopeInternal || ki.Perms.IsSuperuser()
}
