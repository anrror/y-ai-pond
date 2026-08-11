// Package benchmark holds performance benchmarks for y-ai-pond's
// core subsystems: MQTT throughput, InfluxDB write latency, API
// concurrency, and edge inference pipeline (T32).
//
// All benchmarks report p95/p99 percentiles via b.ReportMetric() in
// addition to the standard ns/op output. Benchmarks are opt-in via
// -bench flag and are NOT run in CI (per plan Must NOT).
package benchmark

import (
	"sort"
	"testing"
	"time"
)

// latencyPercentile computes p95 and p99 from a sorted slice of durations.
// The caller is responsible for sorting the slice first (ascending).
func latencyPercentile(sorted []time.Duration, pct float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	// Nearest-rank method: index = ceil(pct * n) - 1
	idx := int(pct*float64(len(sorted))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// reportLatencyPercentiles sorts the latency slice, computes p95/p99/avg,
// and reports them to the benchmark framework via b.ReportMetric.
// Metrics reported: p95_ns, p99_ns, avg_ns.
func reportLatencyPercentiles(b *testing.B, latencies []time.Duration) {
	b.Helper()
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	var total time.Duration
	for _, d := range latencies {
		total += d
	}
	avg := total / time.Duration(len(latencies))
	p95 := latencyPercentile(latencies, 0.95)
	p99 := latencyPercentile(latencies, 0.99)

	b.ReportMetric(float64(avg.Nanoseconds()), "avg_ns")
	b.ReportMetric(float64(p95.Nanoseconds()), "p95_ns")
	b.ReportMetric(float64(p99.Nanoseconds()), "p99_ns")
}
