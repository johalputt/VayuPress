// SPDX-License-Identifier: Apache-2.0

package analytics

import (
	"fmt"
	"testing"
)

func TestHLLEstimateAccuracy(t *testing.T) {
	for _, n := range []int{10, 100, 1000, 10000} {
		h := &HLL{}
		for i := 0; i < n; i++ {
			h.Add(fmt.Sprintf("visitor-%d", i))
		}
		got := h.Estimate()
		tol := float64(n) * 0.05 // HLL σ≈0.0081; 5% is generous but honest
		if got < float64(n)-tol || got > float64(n)+tol {
			t.Fatalf("n=%d: estimate %.0f outside ±5%%", n, got)
		}
	}
}

func TestHLLMerge(t *testing.T) {
	a, b, all := &HLL{}, &HLL{}, &HLL{}
	for i := 0; i < 500; i++ {
		k := fmt.Sprintf("v-%d", i)
		all.Add(k)
		if i%2 == 0 {
			a.Add(k)
		} else {
			b.Add(k)
		}
	}
	a.Merge(b)
	if e := a.Estimate(); e < 475 || e > 525 {
		t.Fatalf("merged estimate %.0f not ≈500", e)
	}
	if e := all.Estimate(); e < 475 || e > 525 {
		t.Fatalf("direct estimate %.0f not ≈500", e)
	}
}

func TestHLLMarshalRoundTrip(t *testing.T) {
	h := &HLL{}
	for i := 0; i < 300; i++ {
		h.Add(fmt.Sprintf("k-%d", i))
	}
	back, err := UnmarshalHLL(h.Marshal())
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Estimate() != h.Estimate() {
		t.Fatalf("round trip changed estimate")
	}
	if _, err := UnmarshalHLL([]byte{1, 2, 3}); err == nil {
		t.Fatal("short blob accepted")
	}
}

func TestHLLEmptyIsZero(t *testing.T) {
	h := &HLL{}
	if e := h.Estimate(); e != 0 {
		t.Fatalf("empty sketch estimates %v", e)
	}
}
