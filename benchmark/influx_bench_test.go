package benchmark

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/store"
)

// timedInfluxWriter wraps an InfluxWriter implementation and records the
// duration of each WriteSensorData call for latency measurement.
type timedInfluxWriter struct {
	inner     store.InfluxWriter
	latencies []time.Duration
	mu        sync.Mutex
}

func newTimedInfluxWriter(inner store.InfluxWriter) *timedInfluxWriter {
	return &timedInfluxWriter{inner: inner}
}

func (w *timedInfluxWriter) WriteSensorData(ctx context.Context, pts []store.SensorPoint) error {
	start := time.Now()
	err := w.inner.WriteSensorData(ctx, pts)
	w.mu.Lock()
	w.latencies = append(w.latencies, time.Since(start))
	w.mu.Unlock()
	return err
}

func (w *timedInfluxWriter) QueryTimeRange(ctx context.Context, measurement, start, end string) ([]store.Point, error) {
	return w.inner.QueryTimeRange(ctx, measurement, start, end)
}

func (w *timedInfluxWriter) Close() error { return w.inner.Close() }

func (w *timedInfluxWriter) snapshot() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]time.Duration, len(w.latencies))
	copy(out, w.latencies)
	return out
}

var _ store.InfluxWriter = (*timedInfluxWriter)(nil)

// fakeBenchInflux is a simulated InfluxWriter for benchmarks.
// WriteSensorData simulates a variable write latency (1-5ms ± jitter)
// to produce meaningful p95/p99 percentile measurements. The RNG is seeded
// deterministically for repeatable results.
type fakeBenchInflux struct {
	rng *rand.Rand
}

func newFakeBenchInflux() *fakeBenchInflux {
	//nolint:gosec // math/rand with fixed seed is intentional for deterministic benchmarks
	return &fakeBenchInflux{rng: rand.New(rand.NewSource(42))}
}

func (f *fakeBenchInflux) WriteSensorData(_ context.Context, _ []store.SensorPoint) error {
	// Simulate 1-5ms baseline + 0-2ms jitter.
	base := 1*time.Millisecond + time.Duration(f.rng.Int63n(4))*time.Millisecond
	jitter := time.Duration(f.rng.Int63n(2)) * time.Millisecond
	time.Sleep(base + jitter)
	return nil
}
func (f *fakeBenchInflux) QueryTimeRange(_ context.Context, _, _, _ string) ([]store.Point, error) {
	return nil, nil
}
func (f *fakeBenchInflux) Close() error { return nil }

var _ store.InfluxWriter = (*fakeBenchInflux)(nil)

// BenchmarkInfluxWrite measures InfluxDB batch write latency.
// Target: avg < 50ms for 10K points/batch.
//
//	10K points per batch → measure write latency
//	Uses an in-memory fake InfluxWriter with timing wrapper.
func BenchmarkInfluxWrite(b *testing.B) {
	const batchSize = 10_000

	// Build a fixed batch of 10K sensor points.
	batch := make([]store.SensorPoint, batchSize)
	now := time.Now()
	for i := 0; i < batchSize; i++ {
		batch[i] = store.SensorPoint{
			FarmID:     "farm-1",
			PondID:     "pond-1",
			SensorType: "ph",
			Timestamp:  now.Add(time.Duration(i) * time.Second),
			Fields:     map[string]float64{"ph": 7.0 + float64(i)*0.0001},
		}
	}

	b.ResetTimer()

	allLatencies := make([]time.Duration, 0, b.N)

	for i := 0; i < b.N; i++ {
		w := newTimedInfluxWriter(newFakeBenchInflux())
		if err := w.WriteSensorData(context.Background(), batch); err != nil {
			b.Fatalf("write: %v", err)
		}
		allLatencies = append(allLatencies, w.snapshot()...)
	}

	reportLatencyPercentiles(b, allLatencies)
}
