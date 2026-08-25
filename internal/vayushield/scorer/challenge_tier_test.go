// SPDX-License-Identifier: Apache-2.0

package scorer

import (
	"testing"

	"github.com/johalputt/vayupress/internal/vayushield/botdb"
)

// Generic HTTP libraries classify into the SOLVABLE challenge band: above the
// PoW threshold so they are never waved through, below the block threshold so
// a human behind curl keeps a path and never earns a jail (2025 audit).
func TestChallengeTierAutomationIsNotAConviction(t *testing.T) {
	got := Score(Input{StaticMatch: &botdb.Signature{
		Name:           "python-requests",
		Classification: botdb.ClassUnknown,
	}})
	if got.ClientType != botdb.TypeUnknown || got.Classification != botdb.ClassUnknown {
		t.Fatalf("challenge tier must stay Unknown, got %s/%s", got.ClientType, got.Classification)
	}
	if got.Authoritative {
		t.Fatal("challenge-tier match must not be authoritative")
	}
	if got.BotScore < 0.4 {
		t.Fatalf("automation client scored %.2f below the challenge threshold", got.BotScore)
	}
	if got.BotScore >= 0.8 {
		t.Fatalf("automation client scored %.2f into block territory", got.BotScore)
	}
}

func TestDedicatedHostileAutomationStillConvicted(t *testing.T) {
	got := Score(Input{StaticMatch: &botdb.Signature{
		Name:           "sqlmap",
		Classification: botdb.ClassBadBot,
	}})
	if got.ClientType != botdb.TypeBadBot || !got.Authoritative || got.BotScore < 0.9 {
		t.Fatalf("sqlmap must remain an authoritative conviction, got %s/%.2f", got.ClientType, got.BotScore)
	}
}
