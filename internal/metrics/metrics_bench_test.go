package metrics

import (
	"testing"
	"time"
)

func BenchmarkHistogramRecord(b *testing.B) {
	var h Histogram
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.Record(time.Duration(i%1000+1) * time.Millisecond)
	}
}

func BenchmarkHistogramPercentile(b *testing.B) {
	var h Histogram
	for i := 0; i < 1000; i++ {
		h.Record(time.Duration(i+1) * time.Millisecond)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.Percentile(95)
	}
}

func BenchmarkCacheHitRatio(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = CacheHitRatio()
	}
}
