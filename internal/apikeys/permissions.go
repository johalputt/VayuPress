// SPDX-License-Identifier: Apache-2.0

package apikeys

// permissions.go — the fine-grained capability model for VayuAPI keys.
//
// A capability is a (Section, Action) pair, e.g. (posts, write). A key carries a
// grant set — for each section, the set of actions it may perform. Enforcement
// asks Permissions.Has(section, action); the answer honours two wildcards:
//
//   - action "*"  within a section grants every action of that section;
//   - section "*" (with action "*") is a superuser grant — every capability.
//
// The grant set is persisted as compact JSON in the vayu_api_keys.permissions
// column (see migration 062):
//
//	{"v":1,"grants":{"posts":["read","write","delete"],"media":["read"]}}
//
// An empty object ("{}", the column default) is deny-all — the safe default for a
// freshly minted external key that has not been granted anything yet.

import (
	"encoding/json"
	"sort"
)

// permsSchemaVersion is the on-disk schema version of the permissions blob, so a
// future shape change can be migrated forward without guessing.
const permsSchemaVersion = 1

// Section is a coarse area of the VayuPress API a key may be granted access to.
// The set is closed and mirrors the console's own areas + the public API surface.
type Section string

// Action is a verb a key may be permitted to perform within a section.
type Action string

// Canonical sections (12) + the superuser wildcard.
const (
	SectionPosts     Section = "posts"
	SectionThemes    Section = "themes"
	SectionDesign    Section = "design"
	SectionPlugins   Section = "plugins"
	SectionDomains   Section = "domains"
	SectionAnalytics Section = "analytics"
	SectionMedia     Section = "media"
	SectionComments  Section = "comments"
	SectionMembers   Section = "members"
	SectionMail      Section = "mail"
	SectionSettings  Section = "settings"
	SectionBackup    Section = "backup"
	SectionAll       Section = "*" // superuser (paired with ActionAll)
)

// Canonical actions (6) + the all-actions wildcard.
const (
	ActionRead    Action = "read"
	ActionWrite   Action = "write"
	ActionDelete  Action = "delete"
	ActionApply   Action = "apply"
	ActionInstall Action = "install"
	ActionExport  Action = "export"
	ActionAll     Action = "*" // every action of a section
)

// AllSections is the ordered, canonical section vocabulary — the single source of
// truth for the admin permission grid, the VCB validator, and API docs.
var AllSections = []Section{
	SectionPosts, SectionThemes, SectionDesign, SectionPlugins, SectionDomains,
	SectionAnalytics, SectionMedia, SectionComments, SectionMembers, SectionMail,
	SectionSettings, SectionBackup,
}

// AllActions is the ordered, canonical action vocabulary.
var AllActions = []Action{
	ActionRead, ActionWrite, ActionDelete, ActionApply, ActionInstall, ActionExport,
}

// validSections / validActions back O(1) membership checks (built from the
// canonical slices above; wildcards are accepted separately).
var validSections = func() map[Section]bool {
	m := map[Section]bool{SectionAll: true}
	for _, s := range AllSections {
		m[s] = true
	}
	return m
}()

var validActions = func() map[Action]bool {
	m := map[Action]bool{ActionAll: true}
	for _, a := range AllActions {
		m[a] = true
	}
	return m
}()

// ValidSection reports whether s is a known section (including the "*" wildcard).
func ValidSection(s Section) bool { return validSections[s] }

// ValidAction reports whether a is a known action (including the "*" wildcard).
func ValidAction(a Action) bool { return validActions[a] }

// Permissions is a key's grant set: section -> set of granted actions. The zero
// value (nil) is a valid, deny-all permission set.
type Permissions map[Section]map[Action]bool

// NewPermissions returns an empty (deny-all) grant set ready to Grant into.
func NewPermissions() Permissions { return Permissions{} }

// Superuser returns a grant set that permits every capability. Used for the
// internal/system key and the static bootstrap key (never persisted as JSON).
func Superuser() Permissions { return Permissions{SectionAll: {ActionAll: true}} }

// Grant adds a single (section, action) capability. Unknown tokens are ignored so
// a malformed blob can never widen access.
func (p Permissions) Grant(s Section, a Action) {
	if !ValidSection(s) || !ValidAction(a) {
		return
	}
	if p[s] == nil {
		p[s] = map[Action]bool{}
	}
	p[s][a] = true
}

// hasExact reports a literal grant with no wildcard interpretation.
func (p Permissions) hasExact(s Section, a Action) bool {
	m, ok := p[s]
	if !ok {
		return false
	}
	return m[a]
}

// Has reports whether the key may perform action a in section s, honouring the
// section-all and action-all wildcards. It is the single enforcement predicate.
func (p Permissions) Has(s Section, a Action) bool {
	if len(p) == 0 {
		return false // deny-all
	}
	// Superuser: section "*" with action "*".
	if p.hasExact(SectionAll, ActionAll) {
		return true
	}
	// All actions of this specific section.
	if p.hasExact(s, ActionAll) {
		return true
	}
	// Exact capability.
	return p.hasExact(s, a)
}

// IsSuperuser reports whether this grant set permits every capability.
func (p Permissions) IsSuperuser() bool { return p.hasExact(SectionAll, ActionAll) }

// Covers reports whether p grants every capability in other — i.e. other is a
// subset of p, wildcards honoured (a "*:*" or "section:*" grant in p satisfies
// the matching entries in other). Used to enforce that a key can never mint or
// widen a grant it does not itself hold. An empty other is trivially covered.
func (p Permissions) Covers(other Permissions) bool {
	for s, acts := range other {
		for a := range acts {
			if !p.Has(s, a) {
				return false
			}
		}
	}
	return true
}

// IsEmpty reports whether the grant set permits nothing (deny-all).
func (p Permissions) IsEmpty() bool {
	for _, acts := range p {
		if len(acts) > 0 {
			return false
		}
	}
	return true
}

// permsWire is the JSON envelope persisted in the permissions column.
type permsWire struct {
	V      int                 `json:"v"`
	Grants map[string][]string `json:"grants"`
}

// MarshalJSON renders the grant set as the versioned {v,grants} envelope with
// deterministic (sorted) ordering so equal grant sets serialise identically.
func (p Permissions) MarshalJSON() ([]byte, error) {
	grants := make(map[string][]string, len(p))
	for s, acts := range p {
		if len(acts) == 0 {
			continue
		}
		list := make([]string, 0, len(acts))
		for a := range acts {
			list = append(list, string(a))
		}
		sort.Strings(list)
		grants[string(s)] = list
	}
	return json.Marshal(permsWire{V: permsSchemaVersion, Grants: grants})
}

// UnmarshalJSON parses the {v,grants} envelope, silently dropping any unknown
// section/action token so a hand-edited or forward-version blob can never grant
// a capability the running binary does not understand (fail-closed).
func (p *Permissions) UnmarshalJSON(b []byte) error {
	var w permsWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	out := Permissions{}
	for s, acts := range w.Grants {
		sec := Section(s)
		if !ValidSection(sec) {
			continue
		}
		for _, a := range acts {
			out.Grant(sec, Action(a))
		}
	}
	*p = out
	return nil
}

// MarshalString returns the compact JSON string stored in the DB column.
func (p Permissions) MarshalString() string {
	b, err := p.MarshalJSON()
	if err != nil {
		return "{}"
	}
	return string(b)
}

// ParsePermissions parses a stored permissions column value. An empty/blank value
// or a parse error yields a deny-all set (fail-closed), never an error the caller
// must handle at every use site.
func ParsePermissions(s string) Permissions {
	if s == "" {
		return Permissions{}
	}
	var p Permissions
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return Permissions{}
	}
	return p
}

// Capabilities returns the flat, sorted list of "section:action" strings this
// grant set contains, for display and audit.
func (p Permissions) Capabilities() []string {
	var out []string
	for s, acts := range p {
		for a := range acts {
			out = append(out, string(s)+":"+string(a))
		}
	}
	sort.Strings(out)
	return out
}

// ParseCapability splits a "section:action" token into its parts, reporting
// whether it is well-formed and both parts are known.
func ParseCapability(cap string) (Section, Action, bool) {
	for i := 0; i < len(cap); i++ {
		if cap[i] == ':' {
			s, a := Section(cap[:i]), Action(cap[i+1:])
			return s, a, ValidSection(s) && ValidAction(a)
		}
	}
	return "", "", false
}
