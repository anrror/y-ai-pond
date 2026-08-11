package alert

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

// ============================================================================
// AnomalyDetector
// ============================================================================

// AnomalyDetector flags points whose deviation from a local trend exceeds
// sigma multiples of the residual standard deviation (3σ rule).
//
// This is a pure-Go STL-style residual detector that keeps a sliding window
// of recent values, computes a moving-average trend, and fires when the
// latest residual exceeds sigma · σ. No external dependencies are required.
type AnomalyDetector struct {
	mu     sync.Mutex
	sigma  float64
	window int
	values []float64
}

// NewAnomalyDetector creates a detector with the given sigma (typically 3.0)
// and sliding window size (e.g. 30 points).
func NewAnomalyDetector(sigma float64, window int) *AnomalyDetector {
	return &AnomalyDetector{
		sigma:  sigma,
		window: window,
	}
}

// Detect checks whether the latest value is anomalous relative to the
// sliding window. It returns an alert when the residual exceeds sigma · σ
// and σ > 0, or nil when the window is not yet full or the value is normal.
func (d *AnomalyDetector) Detect(_ context.Context, farmID, pondID string, value float64, now time.Time) (*Alert, error) {
	d.mu.Lock()
	d.values = append(d.values, value)
	if len(d.values) > d.window {
		d.values = d.values[len(d.values)-d.window:]
	}
	if len(d.values) < d.window {
		d.mu.Unlock()
		return nil, nil
	}
	// Snapshot to release lock early.
	vals := make([]float64, len(d.values))
	copy(vals, d.values)
	d.mu.Unlock()

	mean, stddev := stats(vals)
	if stddev <= 0 {
		return nil, nil
	}

	last := vals[len(vals)-1]
	residual := last - mean
	threshold := d.sigma * stddev
	if math.Abs(residual) <= threshold {
		return nil, nil
	}

	direction := "high"
	if residual < 0 {
		direction = "low"
	}

	return &Alert{
		FarmID:    farmID,
		PondID:    pondID,
		Type:      "anomaly_" + direction,
		Level:     LevelWarning,
		Message:   fmt.Sprintf("Anomaly detected: value %.3f, residual %.3f > %.1f·σ (σ=%.3f)", last, residual, d.sigma, stddev),
		Value:     value,
		Timestamp: now,
	}, nil
}

// stats computes the mean and sample standard deviation of a slice.
func stats(vals []float64) (mean, stddev float64) {
	n := float64(len(vals))
	for _, v := range vals {
		mean += v
	}
	mean /= n

	var ss float64
	for _, v := range vals {
		d := v - mean
		ss += d * d
	}
	stddev = math.Sqrt(ss / n)
	return
}
