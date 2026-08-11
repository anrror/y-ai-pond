package detector

import (
	"fmt"
	"math/rand"
)

// MockBackend is a deterministic in-memory backend for unit testing.
// It returns pre-configured detections without any real model inference.
//
// Use NewMockBackend(count) to set the number of fish to detect.
// Use NewMockBackendWithDetections(raws) for fine-grained control.
type MockBackend struct {
	modelPath string
	loaded    bool
	raws      []RawDetection // Fixed detections to return on Infer
}

// NewMockBackend creates a MockBackend that returns count evenly-spaced
// fish detections with class="fish" and confidence=0.9.
func NewMockBackend(count int) *MockBackend {
	raws := make([]RawDetection, count)
	for i := 0; i < count; i++ {
		// Spread detections evenly across the image (normalized 0-1)
		raws[i] = RawDetection{
			BBox: [4]float32{
				float32(i+1) * 0.08, // x_center
				float32(i+1) * 0.08, // y_center
				0.05,                // width
				0.05,                // height
			},
			ClassID:    0,
			Class:      "fish",
			Confidence: 0.90,
		}
	}
	return &MockBackend{raws: raws}
}

// NewMockBackendWithDetections creates a MockBackend with explicit raw detections.
func NewMockBackendWithDetections(raws []RawDetection) *MockBackend {
	return &MockBackend{raws: raws}
}

// LoadModel records the model path and marks the backend as loaded.
func (m *MockBackend) LoadModel(path string) error {
	m.modelPath = path
	m.loaded = true
	return nil
}

// Infer returns the pre-configured raw detections. The input parameter
// is ignored; if the input is empty, it returns nil (no detections).
func (m *MockBackend) Infer(input []float32) ([]RawDetection, error) {
	if !m.loaded {
		return nil, fmt.Errorf("mock backend: model not loaded")
	}
	if len(input) == 0 {
		return nil, nil
	}
	return m.raws, nil
}

// Name returns the backend identifier.
func (m *MockBackend) Name() string {
	return "Mock"
}

// Close is a no-op for the mock backend.
func (m *MockBackend) Close() error {
	return nil
}

// NewMockBackendRandom creates a MockBackend that returns count random-ish
// fish detections (for benchmark variability). Each call to Infer returns
// a freshly generated set based on a seeded RNG so results are repeatable.
//
//nolint:gosec // math/rand with fixed seed is intentional for deterministic benchmarks
func NewMockBackendRandom(count int, seed int64) *MockBackend {
	rng := rand.New(rand.NewSource(seed))
	raws := make([]RawDetection, count)
	for i := 0; i < count; i++ {
		raws[i] = RawDetection{
			BBox: [4]float32{
				rng.Float32() * 0.9,
				rng.Float32() * 0.9,
				0.03 + rng.Float32()*0.07,
				0.03 + rng.Float32()*0.07,
			},
			ClassID:    0,
			Class:      "fish",
			Confidence: 0.5 + rng.Float32()*0.45,
		}
	}
	return &MockBackend{raws: raws}
}
