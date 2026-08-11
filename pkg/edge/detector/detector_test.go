package detector

import (
	"testing"
)

// TestLoadModel verifies that a Detector with MockBackend loads successfully.
func TestLoadModel(t *testing.T) {
	mock := NewMockBackend(0)
	d := NewDetector(mock)

	if err := d.LoadWithPath("/fake/path/model.onnx"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}
	_ = mock.Close()

	if !d.loaded {
		t.Error("expected detector to be marked as loaded")
	}

	if name := d.BackendName(); name != "Mock" {
		t.Errorf("expected backend name 'Mock', got %q", name)
	}
}

// TestLoadModelEmptyPath verifies error on empty model path.
func TestLoadModelEmptyPath(t *testing.T) {
	mock := NewMockBackend(0)
	d := NewDetector(mock)

	if err := d.LoadWithPath(""); err == nil {
		t.Error("expected error for empty model path, got nil")
	}
}

// TestDetectStaticImage verifies that the detector returns the expected
// fish count (±5%) for a known test scenario. Uses MockBackend configured
// to return 10 detections.
func TestDetectStaticImage(t *testing.T) {
	expectedCount := 10
	mock := NewMockBackend(expectedCount)
	if err := mock.LoadModel("mock"); err != nil {
		t.Fatalf("Mock LoadModel failed: %v", err)
	}
	d := NewDetector(mock)
	if err := d.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Use a non-empty fake image bytes
	fakeFrame := []byte{0xFF, 0xD8, 0xFF, 0xE0} // JPEG SOI marker
	det, err := d.Detect(fakeFrame)
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	tolerance := 0.05
	lower := int(float64(expectedCount) * (1 - tolerance))
	upper := int(float64(expectedCount) * (1 + tolerance))

	if det.Count < lower || det.Count > upper {
		t.Errorf("expected fish count %d (±%d), got %d", expectedCount, expectedCount/20, det.Count)
	}

	if det.Count > 0 && det.AvgSizePx <= 0 {
		t.Error("expected non-zero AvgSizePx when fish are detected")
	}

	if len(det.Fishes) != det.Count {
		t.Errorf("Fishes length %d != Count %d", len(det.Fishes), det.Count)
	}
}

// TestDetectModelNotLoaded verifies error when Detect is called before Load.
func TestDetectModelNotLoaded(t *testing.T) {
	mock := NewMockBackend(10)
	d := NewDetector(mock)

	_, err := d.Detect([]byte{1, 2, 3})
	if err == nil {
		t.Error("expected error for unloaded model, got nil")
	}
}

// TestDetectEmptyFrame verifies that an empty frame returns zero detections.
func TestDetectEmptyFrame(t *testing.T) {
	mock := NewMockBackend(10)
	if err := mock.LoadModel("mock"); err != nil {
		t.Fatalf("Mock LoadModel failed: %v", err)
	}
	d := NewDetector(mock)
	if err := d.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	det, err := d.Detect(nil)
	if err != nil {
		t.Fatalf("Detect(nil) failed: %v", err)
	}
	if det.Count != 0 {
		t.Errorf("expected 0 detections for nil frame, got %d", det.Count)
	}
}

// TestNMS verifies that Non-Maximum Suppression correctly suppresses
// highly overlapping detections.
func TestNMS(t *testing.T) {
	tests := []struct {
		name         string
		raws         []RawDetection
		confThresh   float32
		iouThresh    float32
		wantMin      int
		wantMax      int
	}{
		{
			name: "two overlapping boxes → one suppressed",
			raws: []RawDetection{
				{BBox: [4]float32{0.5, 0.5, 0.2, 0.2}, ClassID: 0, Class: "fish", Confidence: 0.9},
				{BBox: [4]float32{0.52, 0.52, 0.2, 0.2}, ClassID: 0, Class: "fish", Confidence: 0.8},
			},
			confThresh: 0.25,
			iouThresh:  0.45,
			wantMin:    1,
			wantMax:    1,
		},
		{
			name: "two far-apart boxes → both kept",
			raws: []RawDetection{
				{BBox: [4]float32{0.2, 0.2, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.9},
				{BBox: [4]float32{0.7, 0.7, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.8},
			},
			confThresh: 0.25,
			iouThresh:  0.45,
			wantMin:    2,
			wantMax:    2,
		},
		{
			name: "low confidence filtered out",
			raws: []RawDetection{
				{BBox: [4]float32{0.5, 0.5, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.9},
				{BBox: [4]float32{0.3, 0.3, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.1},
			},
			confThresh: 0.25,
			iouThresh:  0.45,
			wantMin:    1,
			wantMax:    1,
		},
		{
			name: "triple overlap → only highest kept",
			raws: []RawDetection{
				{BBox: [4]float32{0.5, 0.5, 0.15, 0.15}, ClassID: 0, Class: "fish", Confidence: 0.9},
				{BBox: [4]float32{0.51, 0.51, 0.14, 0.14}, ClassID: 0, Class: "fish", Confidence: 0.85},
				{BBox: [4]float32{0.49, 0.49, 0.16, 0.16}, ClassID: 0, Class: "fish", Confidence: 0.75},
			},
			confThresh: 0.25,
			iouThresh:  0.45,
			wantMin:    1,
			wantMax:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockBackendWithDetections(tt.raws)
			if err := mock.LoadModel("mock"); err != nil {
				t.Fatalf("Mock LoadModel failed: %v", err)
			}
			d := NewDetector(mock, WithConfidenceThreshold(tt.confThresh), WithIoUThreshold(tt.iouThresh))
			if err := d.Load(); err != nil {
				t.Fatalf("Load failed: %v", err)
			}

			det, err := d.Detect([]byte{0xFF, 0xD8})
			if err != nil {
				t.Fatalf("Detect failed: %v", err)
			}

			if det.Count < tt.wantMin || det.Count > tt.wantMax {
				t.Errorf("expected %d-%d fish after NMS, got %d", tt.wantMin, tt.wantMax, det.Count)
			}

			// Verify that all kept detections have confidence >= threshold
			for _, f := range det.Fishes {
				if f.Confidence < tt.confThresh {
					t.Errorf("fish confidence %f below threshold %f", f.Confidence, tt.confThresh)
				}
			}
		})
	}
}

// TestComputeIoU verifies IoU calculation for various box configurations.
func TestComputeIoU(t *testing.T) {
	tests := []struct {
		name string
		a, b [4]float32
		want float32
	}{
		{
			name: "identical boxes",
			a:    [4]float32{0.5, 0.5, 0.2, 0.2},
			b:    [4]float32{0.5, 0.5, 0.2, 0.2},
			want: 1.0,
		},
		{
			name: "half overlap",
			a:    [4]float32{0.5, 0.5, 0.2, 0.2},
			b:    [4]float32{0.6, 0.5, 0.2, 0.2},
			want: 1.0 / 3.0, // intersection=0.02, union=0.06, IoU=1/3≈0.333
		},
		{
			name: "no overlap",
			a:    [4]float32{0.1, 0.1, 0.05, 0.05},
			b:    [4]float32{0.9, 0.9, 0.05, 0.05},
			want: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeIoU(tt.a, tt.b)
			if diff := got - tt.want; diff < -0.01 || diff > 0.01 {
				t.Errorf("computeIoU(%v, %v) = %f, want %f", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestRawToPixelBBox verifies the conversion from normalized center-format
// to pixel corner-format coordinates.
func TestRawToPixelBBox(t *testing.T) {
	norm := [4]float32{0.5, 0.5, 0.1, 0.2} // center at (0.5, 0.5), w=0.1, h=0.2
	pixel := rawToPixelBBox(norm)

	expectedX1 := float32((0.5 - 0.05) * 640) // x_center - w/2
	expectedY1 := float32((0.5 - 0.10) * 640) // y_center - h/2
	expectedX2 := float32((0.5 + 0.05) * 640)
	expectedY2 := float32((0.5 + 0.10) * 640)

	if pixel[0] != expectedX1 {
		t.Errorf("x1: got %f, want %f", pixel[0], expectedX1)
	}
	if pixel[1] != expectedY1 {
		t.Errorf("y1: got %f, want %f", pixel[1], expectedY1)
	}
	if pixel[2] != expectedX2 {
		t.Errorf("x2: got %f, want %f", pixel[2], expectedX2)
	}
	if pixel[3] != expectedY2 {
		t.Errorf("y2: got %f, want %f", pixel[3], expectedY2)
	}
}

// TestConfidenceThreshold verifies that custom confidence threshold works.
func TestConfidenceThreshold(t *testing.T) {
	raws := []RawDetection{
		{BBox: [4]float32{0.2, 0.2, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.9},
		{BBox: [4]float32{0.5, 0.5, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.3},
		{BBox: [4]float32{0.8, 0.8, 0.1, 0.1}, ClassID: 0, Class: "fish", Confidence: 0.1},
	}

	mock := NewMockBackendWithDetections(raws)
	if err := mock.LoadModel("mock"); err != nil {
		t.Fatalf("Mock LoadModel failed: %v", err)
	}
	d := NewDetector(mock, WithConfidenceThreshold(0.5))
	if err := d.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	det, err := d.Detect([]byte{0xFF, 0xD8})
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	if det.Count != 1 {
		t.Errorf("expected 1 fish with conf >= 0.5, got %d", det.Count)
	}
}

// TestDetectNoFish verifies zero detections result.
func TestDetectNoFish(t *testing.T) {
	mock := NewMockBackend(0)
	if err := mock.LoadModel("mock"); err != nil {
		t.Fatalf("Mock LoadModel failed: %v", err)
	}
	d := NewDetector(mock)
	if err := d.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	det, err := d.Detect([]byte{0xFF, 0xD8})
	if err != nil {
		t.Fatalf("Detect failed: %v", err)
	}

	if det.Count != 0 {
		t.Errorf("expected 0 fish, got %d", det.Count)
	}
	if det.AvgSizePx != 0 {
		t.Errorf("expected AvgSizePx=0 with no fish, got %f", det.AvgSizePx)
	}
}

// TestMockBackendName verifies each backend returns correct name.
func TestMockBackendName(t *testing.T) {
	backends := []struct {
		name    string
		backend Backend
		want    string
	}{
		{"mock", NewMockBackend(0), "Mock"},
		{"onnx", NewONNXBackend(), "ONNX"},
		{"rknn", NewRKNBackend(), "RKNN"},
		{"tensorrt", NewTensorRTBackend(), "TensorRT"},
	}

	for _, b := range backends {
		t.Run(b.name, func(t *testing.T) {
			if got := b.backend.Name(); got != b.want {
				t.Errorf("Name() = %q, want %q", got, b.want)
			}
		})
	}
}

// BenchmarkDetect measures the full Detect pipeline (inference + post-processing)
// with a mock backend returning 10 fish. Target: < 20ms/frame (20,000,000 ns).
func BenchmarkDetect(b *testing.B) {
	mock := NewMockBackend(10)
	if err := mock.LoadModel("bench"); err != nil {
		b.Fatalf("Mock LoadModel failed: %v", err)
	}
	d := NewDetector(mock)
	if err := d.Load(); err != nil {
		b.Fatalf("Load failed: %v", err)
	}

	fakeFrame := make([]byte, 4096) // ~4KB image
	for i := range fakeFrame {
		fakeFrame[i] = byte(i % 256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := d.Detect(fakeFrame)
		if err != nil {
			b.Fatalf("Detect failed: %v", err)
		}
	}
}

// BenchmarkNMS measures only the NMS post-processing step.
func BenchmarkNMS(b *testing.B) {
	raws := make([]RawDetection, 100)
	for i := 0; i < 100; i++ {
		raws[i] = RawDetection{
			BBox:       [4]float32{0.1 + float32(i)*0.008, 0.1 + float32(i)*0.008, 0.05, 0.05},
			ClassID:    0,
			Class:      "fish",
			Confidence: 0.95 - float32(i)*0.008,
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		postProcess(raws, 0.25, 0.45)
	}
}
