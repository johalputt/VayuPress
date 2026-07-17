package botdb

import (
	"context"
	"testing"
)

// seedBadBot inserts an auto-learned bad_bot candidate at the detection-seed
// confidence (0.7), as jailBadActor does on a hard block.
func seedBadBot(t *testing.T, s *Store, fph string) {
	t.Helper()
	if err := s.Observe(context.Background(), Observation{
		FingerprintHash: fph, Classification: ClassBadBot, Confidence: 0.7, AutoLearned: true,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func confidenceOf(t *testing.T, s *Store, fph string) (conf float64, fp int64) {
	t.Helper()
	sig, ok := s.Lookup(context.Background(), fph)
	if !ok {
		t.Fatalf("fingerprint %q not found", fph)
	}
	return sig.Confidence, sig.FalsePositives
}

// TestPromotionCurveClosesLoop: a recurring auto-learned bad_bot candidate with
// NO false positive reaches the scorer's action threshold (0.80) within a few
// sightings from the 0.7 seed — so the learning loop actually closes and repeat
// bots are blocked by the learned signature, not re-derived each time.
func TestPromotionCurveClosesLoop(t *testing.T) {
	s := testStore(t)
	seedBadBot(t, s, "fp-bad") // request_count=1, conf 0.70
	// Sightings 2..8: the +0.10/sighting bump starts once request_count+1>=5.
	for i := 0; i < 7; i++ {
		if err := s.Observe(context.Background(), Observation{FingerprintHash: "fp-bad", Classification: ClassBadBot, AutoLearned: true}); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	conf, fp := confidenceOf(t, s, "fp-bad")
	if fp != 0 {
		t.Fatalf("no false positive expected, got %d", fp)
	}
	if conf < 0.80 {
		t.Fatalf("recurring clean bad_bot must reach the 0.80 action threshold, got %.2f", conf)
	}
}

// TestFalsePositiveFreezesPromotion: once a human solves a challenge for a
// fingerprint (false_positive_count>0), its confidence must STOP climbing — so a
// coarse fingerprint many real users share is never escalated to a hard block.
func TestFalsePositiveFreezesPromotion(t *testing.T) {
	s := testStore(t)
	seedBadBot(t, s, "fp-shared")
	// A real user proves human on this fingerprint.
	if err := s.ReportFalsePositive(context.Background(), "fp-shared"); err != nil {
		t.Fatalf("report fp: %v", err)
	}
	before, fp := confidenceOf(t, s, "fp-shared")
	if fp == 0 {
		t.Fatal("false positive not recorded")
	}
	// Many further sightings must NOT climb the confidence now.
	for i := 0; i < 10; i++ {
		_ = s.Observe(context.Background(), Observation{FingerprintHash: "fp-shared", Classification: ClassBadBot, AutoLearned: true})
	}
	after, _ := confidenceOf(t, s, "fp-shared")
	if after > before {
		t.Fatalf("false-positive fingerprint must not gain confidence: %.2f -> %.2f", before, after)
	}
}
