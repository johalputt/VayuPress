package metrics

import (
	"testing"
	"time"
)

func TestHistogramRecord(t *testing.T) {
	var h Histogram
	h.Record(5 * time.Millisecond)
	h.Record(50 * time.Millisecond)
	h.Record(500 * time.Millisecond)
	_, count, sum, max := h.Snapshot()
	if count != 3 {
		t.Fatalf("count: want 3, got %d", count)
	}
	if sum == 0 {
		t.Fatal("sum should be non-zero")
	}
	if max < 500 {
		t.Fatalf("max: want >= 500ms, got %d", max)
	}
}

func TestHistogramPercentile(t *testing.T) {
	var h Histogram
	for i := 0; i < 100; i++ {
		h.Record(time.Duration(i+1) * time.Millisecond)
	}
	p50 := h.Percentile(50)
	p99 := h.Percentile(99)
	if p50 <= 0 {
		t.Fatalf("p50 should be positive, got %d", p50)
	}
	if p99 <= p50 {
		t.Fatalf("p99 (%d) should be > p50 (%d)", p99, p50)
	}
}

func TestHistogramMean(t *testing.T) {
	var h Histogram
	h.Record(10 * time.Millisecond)
	h.Record(20 * time.Millisecond)
	mean := h.Mean()
	if mean <= 0 {
		t.Fatalf("mean should be positive, got %f", mean)
	}
}

func TestHistogramPrometheus(t *testing.T) {
	var h Histogram
	h.Record(10 * time.Millisecond)
	out := h.Prometheus("test_metric", "help text")
	if out == "" {
		t.Fatal("Prometheus output should not be empty")
	}
	if len(out) < 10 {
		t.Fatalf("Prometheus output too short: %q", out)
	}
}

func TestCacheHitRatio(t *testing.T) {
	MetricCacheHits = 0
	MetricCacheMisses = 0
	ratio := CacheHitRatio()
	if ratio != 0 {
		t.Fatalf("empty ratio: want 0, got %f", ratio)
	}
	MetricCacheHits = 8
	MetricCacheMisses = 2
	ratio = CacheHitRatio()
	if ratio < 0.79 || ratio > 0.81 {
		t.Fatalf("ratio: want ~0.8, got %f", ratio)
	}
}

func TestHistBoundMS(t *testing.T) {
	if HistBoundMS[0] != 1 {
		t.Fatalf("first bound: want 1, got %d", HistBoundMS[0])
	}
	for i := 1; i < len(HistBoundMS)-1; i++ {
		if HistBoundMS[i] <= HistBoundMS[i-1] {
			t.Fatalf("bounds not monotonic at index %d: %d <= %d", i, HistBoundMS[i], HistBoundMS[i-1])
		}
	}
}
