package metrics

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Atomic counters
var (
	MetricArticlesCreated         int64
	MetricArticlesUpdated         int64
	MetricArticlesDeleted         int64
	MetricMeiliErrors             int64
	MetricQueueProcessed          int64
	MetricQueueFailed             int64
	MetricCacheHits               int64
	MetricCacheMisses             int64
	MetricQueueStuckResets        int64
	MetricPluginPanics            int64
	MetricAuthLockouts            int64
	MetricPluginPoolDropped       int64
	MetricPluginDisabled          int64
	MetricWALCheckpoints          int64
	MetricSlowQueries             int64
	MetricDeadLetterJobs          int64
	MetricWALCheckpointDurationMS int64
	MetricWALAdaptiveCheckpoints  int64
	MetricMigrationDriftDetected  int64
	MetricPoisonJobsQuarantined   int64
	MetricPprofAccesses           int64
	MetricVacuumRejected          int64
	MetricHealthDegradedEvents    int64
	MetricCSPViolations           int64
	MetricFullScanWarnings        int64

	WorkerLiveness     int64
	WorkerLastActivity sync.Map
	WorkerWg           sync.WaitGroup
	CachedStorageBytes int64
)

// Histogram

type Histogram struct {
	mu              sync.Mutex
	buckets         [16]int64
	count, sum, max int64
}

var HistBoundMS = [16]int64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 1 << 62}

func (h *Histogram) Record(d time.Duration) {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	h.mu.Lock()
	h.count++
	h.sum += ms
	if ms > h.max {
		h.max = ms
	}
	bucket := 0
	for bucket < 15 && ms > HistBoundMS[bucket] {
		bucket++
	}
	h.buckets[bucket]++
	h.mu.Unlock()
}

func (h *Histogram) Snapshot() (buckets [16]int64, count, sum, max int64) {
	h.mu.Lock()
	buckets = h.buckets
	count = h.count
	sum = h.sum
	max = h.max
	h.mu.Unlock()
	return
}

func (h *Histogram) Prometheus(name, help string) string {
	buckets, count, sum, _ := h.Snapshot()
	var sb strings.Builder
	fmt.Fprintf(&sb, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	cumulative := int64(0)
	for i, bound := range HistBoundMS {
		cumulative += buckets[i]
		if bound == 1<<62 {
			fmt.Fprintf(&sb, "%s_bucket{le=\"+Inf\"} %d\n", name, cumulative)
		} else {
			fmt.Fprintf(&sb, "%s_bucket{le=\"%.3f\"} %d\n", name, float64(bound)/1000.0, cumulative)
		}
	}
	fmt.Fprintf(&sb, "%s_sum %d\n%s_count %d\n", name, sum, name, count)
	return sb.String()
}

func (h *Histogram) Percentile(pct float64) int64 {
	buckets, count, _, _ := h.Snapshot()
	if count == 0 {
		return 0
	}
	target := int64(float64(count) * pct / 100.0)
	if target < 1 {
		target = 1
	}
	cumulative := int64(0)
	for i, b := range buckets {
		cumulative += b
		if cumulative >= target {
			if HistBoundMS[i] == 1<<62 && i > 0 {
				return HistBoundMS[i-1] * 2
			}
			return HistBoundMS[i]
		}
	}
	return HistBoundMS[14]
}

func (h *Histogram) Mean() float64 {
	_, count, sum, _ := h.Snapshot()
	if count == 0 {
		return 0
	}
	return float64(sum) / float64(count)
}

var (
	HTTPLatency        Histogram
	RenderLatency      Histogram
	QueueJobLatency    Histogram
	SQLiteWriteLatency Histogram
)

func CacheHitRatio() float64 {
	hits := atomic.LoadInt64(&MetricCacheHits)
	misses := atomic.LoadInt64(&MetricCacheMisses)
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}
