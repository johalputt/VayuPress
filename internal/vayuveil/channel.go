// SPDX-License-Identifier: Apache-2.0

// Package vayuveil is the Observation Contract registry for ADR-0150 (VayuVeil,
// endpoint observation control).
//
// It is deliberately the same construction as internal/vayushield/rule.go, for
// the same reason and at higher stakes. Every interface through which the screen,
// the keyboard, the clipboard or window content can be observed is a registered
// Channel with four obligations expressed as types whose ZERO VALUES ARE INVALID.
// A channel added without deciding its policy does not pass the tests.
//
// That is the only honest meaning of "no loophole": not that we thought of
// everything, but that anything we did not think of cannot be silently
// introduced.
//
// WHAT THIS PACKAGE IS NOT. It is the registry and the inventory — ADR-0150's P0.
// It does not enforce anything. Enforcement is P1 and later, and lives in a
// compositor, a sandbox and a MAC policy rather than in this binary. Every
// operator-facing surface built on this package must say so; a registry that
// reads as a defence is worse than no registry, because it is believed.
package vayuveil

import "strings"

// Asset is one of the ten observable assets enumerated in ADR-0150 §2. An
// unenumerated asset is an unprotected one, so the list is closed and the zero
// value is not a member of it.
type Asset uint8

const (
	assetUnset Asset = iota
	// AssetFramebuffer — the composited output: screenshots and recording.
	AssetFramebuffer
	// AssetWindowPixels — one surface's content, without the rest of the screen.
	AssetWindowPixels
	// AssetWindowText — window content AS TEXT, via accessibility APIs. A complete
	// capture channel that reads no pixels, and the one everyone forgets.
	AssetWindowText
	// AssetKeyboard — keystrokes. Strictly worse than screenshots.
	AssetKeyboard
	// AssetPointer — pointer position and clicks.
	AssetPointer
	// AssetClipboard — clipboard and primary selection.
	AssetClipboard
	// AssetNotifications — notification bodies.
	AssetNotifications
	// AssetWindowMetadata — window titles and the application list. Metadata leaks
	// intent even when no content is read.
	AssetWindowMetadata
	// AssetThumbnails — previews generated from files by something else.
	AssetThumbnails
	// AssetMemoryImage — suspend/hibernate images and core dumps, which contain
	// any of the above.
	AssetMemoryImage
)

// Disposition is what this install's policy says about the channel.
type Disposition uint8

const (
	// dispositionUnset is the zero value and deliberately NOT a valid answer.
	// rule.go learned this the hard way: a contract whose zero value is a valid
	// answer is the type answering its own question on the author's behalf.
	dispositionUnset Disposition = iota
	// DispositionAbsent — the interface is not present or not compiled in. An
	// absent protocol has no bypass, which is why P1 removes capture protocols
	// rather than gating them.
	DispositionAbsent
	// DispositionDeny — present, and refused.
	DispositionDeny
	// DispositionGrant — present, and reachable ONLY through a GrantModel that
	// requires the user to say yes. There is no disposition that allows without
	// asking; see Complete.
	DispositionGrant
)

// GrantModel is how a person says yes, if they can at all.
type GrantModel uint8

const (
	// grantUnset is the zero value and not a valid answer.
	grantUnset GrantModel = iota
	// GrantNone — nothing can be granted. Pairs with Absent or Deny.
	GrantNone
	// GrantPerUse — asked every time, nothing is remembered.
	GrantPerUse
	// GrantScopedTimed — one window or one output, time-boxed with a visible
	// countdown, revocable mid-stream. Never "everything" by default.
	GrantScopedTimed
	// GrantPinned — persists across restarts because the user explicitly pinned
	// it, and is listed permanently in the panel for as long as it holds.
	GrantPinned
)

// IndicatorKind is what the person sees while the channel is in use.
type IndicatorKind uint8

const (
	// indicatorUnset is the zero value and not a valid answer.
	indicatorUnset IndicatorKind = iota
	// IndicatorNone — nothing is shown, which is only honest when nothing can be
	// granted in the first place.
	IndicatorNone
	// IndicatorCompositor — drawn by the compositor on its own layer, above
	// everything, unreachable by clients. A client-drawn dialog can be spoofed;
	// this is the difference between a permission and a suggestion.
	IndicatorCompositor
	// IndicatorPanel — visible in this panel only. Weaker: a person looking at
	// their screen does not see it, so any surface using it must say as much.
	IndicatorPanel
)

// AuditLevel is what is written down.
type AuditLevel uint8

const (
	// auditUnset is the zero value and not a valid answer.
	auditUnset AuditLevel = iota
	// AuditAll — every attempt, allowed or refused. Refusals matter as much as
	// grants: they are how you discover something has been trying for a month.
	AuditAll
	// AuditGrantsOnly — weaker, and a channel choosing it must say why in its
	// rationale.
	AuditGrantsOnly
	// AuditNone — nothing recorded. Valid only where the channel cannot be
	// observed by this install at all.
	AuditNone
)

// ChannelID names one interface through which observation is possible.
type ChannelID string

// Channel declares one observation interface's obligations.
type Channel struct {
	ID        ChannelID
	Name      string
	Asset     Asset
	Default   Disposition
	Grant     GrantModel
	Indicator IndicatorKind
	Audit     AuditLevel
	Rationale string

	// Needs is what enforcing this channel would REQUIRE. It is how the posture
	// report tells an operator "declared" from "defended" — and, since it is
	// rendered on every row, it is worded as a requirement rather than as a
	// forthcoming phase, so the product cannot imply work that is not coming.
	Needs Needs

	// Probe reports whether the interface is present on the running host, and
	// whether this process can reach it. nil means presence cannot be determined
	// from inside this binary, which is reported as unknown rather than as absent
	// — absent evidence is never a pass.
	Probe func(Host) Observation
}

// Needs is WHAT ENFORCING a channel would require.
//
// It used to be a build phase — P1 Screen, P4 Sandbox — and that was a mistake
// worth recording, because it did not stay in the document. Every channel row on
// the panel and in the posture report rendered its phase, so the product told an
// operator, twenty times over, that enforcement was coming in a numbered phase.
// No such phase is coming: ADR-0150 §0 records that the compositor, the sandbox
// policy and the mandatory-access-control set need a desktop operating system,
// and this is a web server.
//
// Naming the REQUIREMENT instead says a true thing about the world rather than a
// promise about a roadmap. It still separates declared from defended, which is
// what the field was for, and it cannot be read as "soon".
type Needs uint8

const (
	// needsUnset is the zero value and not a valid answer.
	needsUnset Needs = iota
	// NeedsCompositor — a display server that mediates capture, draws its own
	// consent and honours a per-surface no-capture flag. Screen, window pixels,
	// input, clipboard and window metadata all sit behind it.
	NeedsCompositor
	// NeedsAccessibilityMediation — a mediator on the accessibility bus, which
	// reads window text without touching a pixel. Distinct from the compositor
	// because a screen reader is a legitimate grant, not an exemption.
	NeedsAccessibilityMediation
	// NeedsSandboxPolicy — per-application namespaces and a mandatory
	// access-control policy set, installed host-wide and enforced by the kernel.
	NeedsSandboxPolicy
	// NeedsHostConfiguration — a decision made outside this process and outside
	// any application: encrypted swap, hibernation off, a core-dump policy. The
	// server track enforces the process-scoped subset and says so; the rest is
	// the host's.
	NeedsHostConfiguration
)

func (n Needs) String() string {
	switch n {
	case NeedsCompositor:
		return "a capture-mediating compositor"
	case NeedsAccessibilityMediation:
		return "a mediator on the accessibility bus"
	case NeedsSandboxPolicy:
		return "a sandbox and mandatory-access-control policy"
	case NeedsHostConfiguration:
		return "host configuration outside this process"
	}
	return "unset"
}

// Complete reports whether every obligation has been answered, and whether the
// answers are consistent with each other.
//
// The consistency half is the part that matters. Four separately-valid answers
// can still describe something incoherent — a channel that grants access with no
// way to say yes, or shows no indicator while it can be granted — and an
// incoherent declaration is how a loophole gets written down as policy.
func (c Channel) Complete() bool {
	if c.Asset == assetUnset || c.Default == dispositionUnset || c.Grant == grantUnset ||
		c.Indicator == indicatorUnset || c.Audit == auditUnset || c.Needs == needsUnset {
		return false
	}
	if strings.TrimSpace(string(c.ID)) == "" || strings.TrimSpace(c.Name) == "" ||
		strings.TrimSpace(c.Rationale) == "" {
		return false
	}
	// Default-deny: a channel that can be used must require a person to say yes.
	if c.Default == DispositionGrant && c.Grant == GrantNone {
		return false
	}
	// The converse: a channel nothing can reach must not advertise a grant model,
	// which would describe a door that is not there.
	if c.Default != DispositionGrant && c.Grant != GrantNone {
		return false
	}
	// An indicator is required exactly when something can be granted. "No
	// indicator" over a grantable channel is silent observation by another name.
	if c.Grant == GrantNone && c.Indicator != IndicatorNone {
		return false
	}
	if c.Grant != GrantNone && c.Indicator == IndicatorNone {
		return false
	}
	// Silence is only honest where this install cannot see the channel at all.
	if c.Audit == AuditNone && c.Default != DispositionAbsent {
		return false
	}
	return true
}
