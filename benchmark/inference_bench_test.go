package benchmark

import (
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/edge/detector"
)

// BenchmarkInference measures edge YOLOv8n inference pipeline latency.
// Target: avg < 20ms per frame across 1000 frames.
//
//	Uses MockBackend (in-memory, no NPU/GPU required).
//	Each frame processes ~50 fish detections through the full pipeline
//	(confidence filtering → NMS → detection aggregation).
func BenchmarkInference(b *testing.B) {
	const numFrames = 1000

	// Create a detector with 50-fish mock backend (realistic pond scenario).
	backend := detector.NewMockBackend(50)
	d := detector.NewDetector(backend)
	if err := d.Load(); err != nil {
		b.Fatalf("load: %v", err)
	}
	defer func() { _ = d.Close() }()

	// Generate deterministic frame data (640×640×3 RGB image bytes).
	// The mock backend interprets byte length for detection count.
	frame := make([]byte, 640*640*3)
	for i := range frame {
		frame[i] = byte(i % 251) // avoid 0xFF to prevent JPEG marker confusion
	}

	b.ResetTimer()

	allLatencies := make([]time.Duration, 0, b.N*numFrames)

	for iter := 0; iter < b.N; iter++ {
		for f := 0; f < numFrames; f++ {
			start := time.Now()
			det, err := d.Detect(frame)
			elapsed := time.Since(start)

			if err != nil {
				b.Fatalf("detect frame %d: %v", f, err)
			}
			if det.Count != 50 {
				b.Fatalf("expected 50 fish, got %d", det.Count)
			}

			allLatencies = append(allLatencies, elapsed)
		}
	}

	reportLatencyPercentiles(b, allLatencies)

	// Additionally report per-op metrics (matching GNN benchmark pattern).
	b.ReportMetric(float64(len(allLatencies)), "frames")
	b.ReportMetric(float64(numFrames), "frames_per_iter")
}
