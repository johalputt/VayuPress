// SPDX-License-Identifier: Apache-2.0

package theme

import (
	"math"
	"testing"
)

func TestContrastRatioKnownValues(t *testing.T) {
	if cr := ContrastRatio("#000000", "#ffffff"); math.Abs(cr-21.0) > 0.05 {
		t.Errorf("black/white should be 21:1, got %.2f", cr)
	}
	if cr := ContrastRatio("#abcdef", "#abcdef"); math.Abs(cr-1.0) > 0.001 {
		t.Errorf("identical colours should be 1:1, got %.2f", cr)
	}
	// #rgb shorthand must expand identically to #rrggbb.
	if a, b := ContrastRatio("#fff", "#000"), ContrastRatio("#ffffff", "#000000"); math.Abs(a-b) > 0.001 {
		t.Errorf("#rgb and #rrggbb must agree: %.2f vs %.2f", a, b)
	}
}

// The guard this replaced returned 21 whenever the darker colour was pure
// black, so black on black passed every contrast check.
func TestContrastRatioBlackOnBlackIsOne(t *testing.T) {
	if cr := ContrastRatio("#000000", "#000000"); math.Abs(cr-1.0) > 0.001 {
		t.Errorf("black on black should be 1:1, got %.2f", cr)
	}
}
