// SPDX-License-Identifier: Apache-2.0

package analytics

// hll.go: a compact HyperLogLog for distinct-visitor sketches in the daily
// rollups (2025 plan Wave 4, item 6).
//
// Why: COUNT(DISTINCT visitor_id) over raw pageviews is O(rows) and dominates
// long-range dashboards. A daily sketch merges in O(registers) regardless of
// how much traffic the day carried, so a 90-day unique count becomes 90 sketch
// unions instead of a full-table scan.
//
// Parameters: p=12 (4096 registers ≈ 4 kB/sketch, σ≈0.0081 relative error),
// 64-bit hashes via SHA-256 truncation — deterministic across processes, which
// stored sketches require. Linear counting covers the sparse regime exactly,
// keeping small sites accurate where HLL bias would otherwise dominate.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

const (
	hllPrecision = 12
	hllRegisters = 1 << hllPrecision
	hllAlpha     = 0.7213 / (1 + 1.079/float64(hllRegisters))
)

// errBadSketch is returned for stored bytes that are not a current-format HLL.
var errBadSketch = errors.New("analytics: not an hll sketch")

// HLL is a mutable distinct-count sketch. The zero value is empty and usable.
type HLL struct {
	Regs [hllRegisters]byte
}

func hash64(key string) uint64 {
	sum := sha256.Sum256([]byte(key))
	return binary.LittleEndian.Uint64(sum[:8])
}

// Add folds one key into the sketch.
func (h *HLL) Add(key string) {
	x := hash64(key)
	idx := x >> (64 - hllPrecision)
	w := x << hllPrecision // remaining 52 bits
	rank := byte(bits.LeadingZeros64(w) + 1)
	if r := byte(64 - hllPrecision + 1); rank > r { // clamp: w==0 ⇒ max rank
		rank = r
	}
	if rank > h.Regs[idx] {
		h.Regs[idx] = rank
	}
}

// Merge unions other into h (register-wise max). Sketches from different key
// spaces must never be merged; callers own that discipline.
func (h *HLL) Merge(other *HLL) {
	for i := 0; i < hllRegisters; i++ {
		if other.Regs[i] > h.Regs[i] {
			h.Regs[i] = other.Regs[i]
		}
	}
}

// Estimate returns the current distinct-key estimate.
func (h *HLL) Estimate() float64 {
	sum := 0.0
	zeros := 0
	for _, r := range h.Regs {
		sum += math.Pow(2, -float64(r)) // Σ 2^-r
		if r == 0 {
			zeros++
		}
	}
	raw := hllAlpha * float64(hllRegisters) * float64(hllRegisters) / sum
	if raw <= 2.5*float64(hllRegisters) && zeros > 0 {
		return float64(hllRegisters) * math.Log(float64(hllRegisters)/float64(zeros))
	}
	return raw
}

// Marshal encodes the sketch for storage with a versioned header.
func (h *HLL) Marshal() []byte {
	out := make([]byte, 2+hllRegisters)
	binary.LittleEndian.PutUint16(out[:2], uint16(hllPrecision))
	copy(out[2:], h.Regs[:])
	return out
}

// UnmarshalHLL decodes a stored sketch.
func UnmarshalHLL(b []byte) (*HLL, error) {
	if len(b) != 2+hllRegisters || binary.LittleEndian.Uint16(b[:2]) != uint16(hllPrecision) {
		return nil, errBadSketch
	}
	h := &HLL{}
	copy(h.Regs[:], b[2:])
	return h, nil
}
