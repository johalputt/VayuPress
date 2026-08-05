// SPDX-License-Identifier: Apache-2.0

// Package vayuflow is the deterministic automation engine (ADR-0151).
//
// The claim it exists to make true, worded to be defensible:
//
//	Every automated action this install takes was authorised in advance by a
//	named operator, is reproducible from its recorded inputs, and could not have
//	exceeded the blast radius declared when it was armed — including when a
//	model chose its content.
//
// That is an AUTHORITY claim, not a quality one. It does not say the automation
// is smart or that it will pick the right moment. It is the claim worth making
// unbreakable, because the failure mode of an automation engine is never "it
// wrote a mediocre summary" — it is "it published 400 of them", "it emailed the
// list twice", or "a comment told it to and it did".
//
// This file holds the capability registry: what each action is allowed to
// touch. It is the rule.go pattern (internal/vayushield/rule.go) applied to a
// second subsystem for the same reason — four obligations re-implemented by
// hand at twenty action sites is four obligations forgotten at the
// twenty-first, silently.
package vayuflow

import (
	"fmt"
	"sort"
)

// Kind is the family of effect an action has on the world.
type Kind uint8

const (
	// kindUnset is the zero value and is deliberately NOT a valid answer, for
	// the reason rule.go records: a contract whose zero value is a valid answer
	// is not a contract. An action literal that omits Kind must fail, not
	// silently register as the most harmless family.
	kindUnset Kind = iota
	// KindContent — creates or edits posts, pages, tags.
	KindContent
	// KindMail — sends mail through the built-in sender. Never reversible.
	KindMail
	// KindEgress — reaches a remote host. Always via safefetch, so always
	// closed by SetBlockClearnetEgress in a Tor Space.
	KindEgress
	// KindModel — calls a model provider. Remote providers are also egress;
	// local ones are not, and the posture report must tell them apart rather
	// than reporting "AI: enabled".
	KindModel
	// KindAdmin — settings, users, keys, domains, shield tiers, payment config.
	// Registered with NO MEMBERS in v1. See TestAdminKindHasNoMembers: the
	// registry makes that a visible, testable emptiness rather than an absence
	// nobody audited.
	KindAdmin
)

func (k Kind) String() string {
	switch k {
	case KindContent:
		return "content"
	case KindMail:
		return "mail"
	case KindEgress:
		return "egress"
	case KindModel:
		return "model"
	case KindAdmin:
		return "admin"
	}
	return "unset"
}

// WritePolicy is the highest level of write an action may perform. It is a
// ceiling, not a description: §6 of the ADR turns "a bad generation cannot
// publish itself" into a property of this type rather than a hope.
type WritePolicy uint8

const (
	// writeUnset — zero value, not a valid answer.
	writeUnset WritePolicy = iota
	// WriteNone — the action reads, or affects nothing persistent.
	WriteNone
	// WriteDraft — may create or edit unpublished content only.
	WriteDraft
	// WriteLive — may publish, send, or otherwise affect what the public sees.
	WriteLive
)

func (w WritePolicy) String() string {
	switch w {
	case WriteNone:
		return "none"
	case WriteDraft:
		return "draft"
	case WriteLive:
		return "live"
	}
	return "unset"
}

// atMost reports whether w is within the ceiling c. The ordering is the
// constant order above, which is why the zero value sits below WriteNone: an
// unset policy compares as beneath every real one and so can never satisfy a
// ceiling by accident.
func (w WritePolicy) atMost(c WritePolicy) bool { return w != writeUnset && c != writeUnset && w <= c }

// OnionPolicy is what an action does under VAYUOS_MODE=tor.
//
// Deliberately named and shaped like vayushield's, because it answers the same
// question and an operator reading both panels should not have to learn two
// vocabularies for one concept.
type OnionPolicy uint8

const (
	// onionUnset — zero value, not a valid answer.
	onionUnset OnionPolicy = iota
	// OnionInert — the action cannot run in a Tor Space. Every egress action is
	// this, because an outbound callback is precisely what ADR-0141 exists to
	// prevent. Egress already routes through safefetch and is therefore already
	// closed by SetBlockClearnetEgress; this entry makes that explicit and
	// testable instead of relying on the call site staying correct.
	OnionInert
	// OnionActive — the action runs, and Rationale says why that is still
	// correct when the install has no clearnet path.
	OnionActive
)

func (o OnionPolicy) String() string {
	switch o {
	case OnionInert:
		return "inert"
	case OnionActive:
		return "active"
	}
	return "unset"
}

// Reversible is whether an operator can undo an action, and it drives the UI
// honestly: an irreversible action gets a confirmation and a distinct chip. A
// "send mail" step cannot be undone and must never render like a draft edit.
type Reversible uint8

const (
	// reversibleUnset — zero value, not a valid answer.
	reversibleUnset Reversible = iota
	// ReversibleByOperator — an operator can undo it from the panel.
	ReversibleByOperator
	// Irreversible — it cannot be undone by anyone. Mail that has left, an
	// egress call a remote party has already acted on.
	Irreversible
)

func (r Reversible) String() string {
	switch r {
	case ReversibleByOperator:
		return "reversible"
	case Irreversible:
		return "irreversible"
	}
	return "unset"
}

// Capability is what one action type is allowed to touch. A registration
// missing any answer fails a test, not a review.
type Capability struct {
	// Action is the stable identifier a Step references. It is what appears in
	// the audit trail, so it is renamed only with a migration.
	Action string
	// Kind is the family of effect.
	Kind Kind
	// Writes is the ceiling on what this action may write.
	Writes WritePolicy
	// Onion is what it does in a Tor Space.
	Onion OnionPolicy
	// Undo says whether an operator can reverse it.
	Undo Reversible
	// MinRole is the authority floor, checked against the flow's OWNER at RUN
	// time rather than at save time. A flow armed by an admin who is later
	// demoted must stop working; without the run-time check a flow is a
	// permanent capability grant that outlives the grant.
	MinRole string
	// Rationale is why these answers, in prose, shown in the panel. A
	// registration whose rationale is empty has not been thought about.
	Rationale string
}

// Complete reports whether every obligation has been answered. The zero value
// of Capability answers nothing, which is the point.
func (c Capability) Complete() error {
	if c.Action == "" {
		return fmt.Errorf("vayuflow: capability has no Action name")
	}
	if c.Kind == kindUnset {
		return fmt.Errorf("vayuflow: action %q does not say what KIND of effect it has", c.Action)
	}
	if c.Writes == writeUnset {
		return fmt.Errorf("vayuflow: action %q does not declare its WRITE ceiling", c.Action)
	}
	if c.Onion == onionUnset {
		return fmt.Errorf("vayuflow: action %q does not say what it does under VAYUOS_MODE=tor", c.Action)
	}
	if c.Undo == reversibleUnset {
		return fmt.Errorf("vayuflow: action %q does not say whether an operator can undo it", c.Action)
	}
	if !validOwnerRole(c.MinRole) {
		return fmt.Errorf("vayuflow: action %q has no usable MinRole (got %q)", c.Action, c.MinRole)
	}
	if c.Rationale == "" {
		return fmt.Errorf("vayuflow: action %q gives no rationale for its answers", c.Action)
	}
	// An action that cannot be undone and can write to what the public sees is
	// the combination worth naming out loud, so the registry cannot acquire one
	// without the rationale saying so.
	return nil
}

// capabilities is the registry. Every action a Step can name must appear here
// exactly once, so a new action cannot be added without stating what it
// touches, what it may write, what it does in a Tor Space, whether it can be
// undone, and who has to own the flow for it to run.
//
// KindAdmin is intentionally absent. Settings, users, keys, domains, VayuShield
// tiers and payment configuration are not automatable in v1, and
// TestAdminKindHasNoMembers pins that so the emptiness is a decision on record
// rather than something nobody got round to.
var capabilities = []Capability{
	{
		Action: "content.draft.create", Kind: KindContent,
		Writes: WriteDraft, Onion: OnionActive, Undo: ReversibleByOperator,
		MinRole: RoleEditor,
		Rationale: "Creates an unpublished post. Nothing reaches a reader until a human publishes it, " +
			"which is why this is the one content action available before the panel gains a review step. " +
			"Active in a Tor Space: writing a local draft touches no network.",
	},
	{
		Action: "model.draft.generate", Kind: KindModel,
		// WriteNone is the whole point: a model step produces a VALUE and
		// touches nothing. Whatever it returns can only become a draft because
		// the step that WRITES is capped at draft — so "a bad generation cannot
		// publish itself" is a property of two registrations rather than a
		// promise about prompt quality.
		Writes: WriteNone,
		// Inert names the REMOTE case, which is the dangerous one: a hosted
		// provider makes this an outbound call, and a Tor Space forbids that.
		// A LOCAL provider is not egress, and Effects.Model allows it there —
		// so the posture report must say which of the two an install has rather
		// than reporting "AI: enabled". Registering Active instead would claim
		// a remote model is safe in a Tor Space, which is the exact overstated
		// claim this project treats as a defect.
		Onion: OnionInert,
		// A generation cannot be un-generated, but it also changes nothing on
		// its own — the reversibility that matters belongs to the step that
		// consumes the value.
		Undo:    ReversibleByOperator,
		MinRole: RoleEditor,
		Rationale: "Calls the configured model and hands the result to the next step as a value. " +
			"It writes nothing itself; the step that writes carries its own ceiling. Inert in a Tor " +
			"Space when the provider is remote, permitted when it runs on this host.",
	},
	{
		Action: "content.draft.update", Kind: KindContent,
		Writes: WriteDraft, Onion: OnionActive, Undo: ReversibleByOperator,
		MinRole: RoleEditor,
		Rationale: "Edits an existing unpublished post. Reversible because the prior revision is kept, " +
			"so an operator can restore it from the editor's history.",
	},
}

// registry is capabilities indexed by action name, built once at init so a
// duplicate registration is a startup failure rather than a last-write-wins
// surprise at run time.
var registry = func() map[string]Capability {
	m := make(map[string]Capability, len(capabilities))
	for _, c := range capabilities {
		if _, dup := m[c.Action]; dup {
			panic("vayuflow: duplicate capability registration for " + c.Action)
		}
		m[c.Action] = c
	}
	return m
}()

// CapabilityFor returns the contract for an action name. An action with no
// registration cannot be invoked — that is the whole point of the registry, and
// it is why this returns an error rather than a zero Capability.
func CapabilityFor(action string) (Capability, error) {
	c, ok := registry[action]
	if !ok {
		return Capability{}, fmt.Errorf("vayuflow: no registered capability for action %q", action)
	}
	return c, nil
}

// Capabilities returns every registration, sorted by action name so the panel
// and the tests both see a stable order.
func Capabilities() []Capability {
	out := make([]Capability, 0, len(capabilities))
	out = append(out, capabilities...)
	sort.Slice(out, func(i, j int) bool { return out[i].Action < out[j].Action })
	return out
}

// CapabilitiesOfKind returns every registration in one family.
func CapabilitiesOfKind(k Kind) []Capability {
	var out []Capability
	for _, c := range Capabilities() {
		if c.Kind == k {
			out = append(out, c)
		}
	}
	return out
}
