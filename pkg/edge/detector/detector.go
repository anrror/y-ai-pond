// Package detector provides the YOLOv8n object detection inference pipeline
// for edge devices (RK3588 NPU / Jetson GPU). The Detector wraps a pluggable
// Backend (ONNX, RKNN, TensorRT, or Mock) and applies standard YOLO
// post-processing: confidence filtering, NMS (IoU=0.45), and detection
// aggregation into the Detection result struct.
//
// Real backends require platform-specific runtime libraries and are NOT
// exercised in CI on this dev machine (no NPU/GPU). CI tests use MockBackend.
//
// Usage (non-blocking pattern):
//
//	d := detector.NewDetector(detector.BackendConfig{
//	    Backend: detector.BackendONNX,
//	    ModelPath: "/data/models/yolov8n.onnx",
//	})
//	if err := d.Load(); err != nil { ... }
//
//	// Run Detect in its own goroutine to avoid blocking the main control loop.
//	go func() {
//	    det, err := d.Detect(frameJPEG)
//	    ...
//	}()
package detector

import (
	"fmt"
	"math"
	"sort"
)

// Fish represents a single detected fish with bounding box, class, and confidence.
type Fish struct {
	BBox       [4]float32 // [x1, y1, x2, y2] in pixel coordinates (top-left, bottom-right)
	Class      string     // Object class label (e.g., "fish")
	Confidence float32    // Detection confidence [0.0, 1.0]
}

// Detection aggregates the results of a single YOLO inference pass.
type Detection struct {
	Fishes     []Fish  // All detected fish after NMS filtering
	Count      int     // len(Fishes); convenience field
	AvgSizePx  float32 // Average bounding box area (width * height) in pixels; 0 if no fish
}

// RawDetection is the raw YOLO output before NMS. Backend implementations
// produce these from the model's output tensors.
type RawDetection struct {
	BBox       [4]float32 // [x_center, y_center, width, height] normalized to [0,1]
	ClassID    int
	Class      string
	Confidence float32
}

// Backend is the interface for model inference runtimes.
// Each backend wraps a platform-specific inference engine (ONNX Runtime,
// RKNN NPU driver, TensorRT, or in-memory mock).
type Backend interface {
	// LoadModel loads the model file into the inference engine.
	// path is the filesystem path to the model artifact (.onnx, .rknn, .engine).
	LoadModel(path string) error

	// Infer runs a forward pass on the preprocessed input tensor and returns
	// raw detections (before NMS). input should be a flat float32 slice
	// representing the preprocessed image tensor in NCHW format [1,3,640,640].
	// Values should be normalized to [0,1] (RGB order, model-dependent).
	Infer(input []float32) ([]RawDetection, error)

	// Name returns a human-readable backend identifier (e.g., "ONNX", "RKNN", "Mock").
	Name() string

	// Close releases backend resources.
	Close() error
}

// Detector orchestrates model loading and inference with post-processing.
type Detector struct {
	backend Backend

	confThreshold float32 // Minimum confidence to keep a detection (default 0.25)
	iouThreshold  float32 // IoU threshold for NMS (default 0.45)

	loaded bool
}

// DetectorOption is a functional option for NewDetector.
type DetectorOption func(*Detector)

// WithConfidenceThreshold sets the confidence threshold for filtering detections.
func WithConfidenceThreshold(t float32) DetectorOption {
	return func(d *Detector) { d.confThreshold = t }
}

// WithIoUThreshold sets the IoU threshold for NMS suppression.
func WithIoUThreshold(t float32) DetectorOption {
	return func(d *Detector) { d.iouThreshold = t }
}

// NewDetector creates a Detector with the given backend.
// It does NOT load the model; call Load() explicitly to do so.
func NewDetector(backend Backend, opts ...DetectorOption) *Detector {
	d := &Detector{
		backend:       backend,
		confThreshold: 0.25,
		iouThreshold:  0.45,
	}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Load calls the backend's LoadModel. Must be called before Detect.
func (d *Detector) Load() error {
	if err := d.backend.LoadModel(""); err != nil {
		return fmt.Errorf("detector load: %w", err)
	}
	d.loaded = true
	return nil
}

// LoadWithPath calls backend.LoadModel with the given path.
// Use when the backend model path is configured externally.
func (d *Detector) LoadWithPath(modelPath string) error {
	if modelPath == "" {
		return fmt.Errorf("detector: model path is empty")
	}
	if err := d.backend.LoadModel(modelPath); err != nil {
		return fmt.Errorf("detector load %q: %w", modelPath, err)
	}
	d.loaded = true
	return nil
}

// Detect runs inference on the given image bytes and returns the post-processed
// Detection. frame should be raw JPEG/PNG image bytes. Preprocessing
// (resize to 640x640, normalize) is handled by the backend.
//
// This function may block for the duration of inference (~10ms on NPU).
// Callers should invoke it in a dedicated goroutine to avoid blocking
// the main control loop.
func (d *Detector) Detect(frame []byte) (Detection, error) {
	if !d.loaded {
		return Detection{}, fmt.Errorf("detector: model not loaded")
	}
	if len(frame) == 0 {
		return Detection{}, nil
	}

	// The backend handles image preprocessing and inference internally.
	// For mock backends, Infer expects preprocessed tensors or handles raw bytes.
	raws, err := d.backend.Infer(bytesToFloat32(frame))
	if err != nil {
		return Detection{}, fmt.Errorf("detector infer: %w", err)
	}

	// Post-processing pipeline
	fishes := postProcess(raws, d.confThreshold, d.iouThreshold)

	var avgSizePx float32
	if len(fishes) > 0 {
		var totalArea float32
		for _, f := range fishes {
			w := f.BBox[2] - f.BBox[0]
			h := f.BBox[3] - f.BBox[1]
			if w > 0 && h > 0 {
				totalArea += w * h
			}
		}
		if totalArea > 0 {
			avgSizePx = totalArea / float32(len(fishes))
		}
	}

	return Detection{
		Fishes:    fishes,
		Count:     len(fishes),
		AvgSizePx: avgSizePx,
	}, nil
}

// BackendName returns the name of the underlying backend.
func (d *Detector) BackendName() string {
	return d.backend.Name()
}

// Close releases backend resources.
func (d *Detector) Close() error {
	return d.backend.Close()
}

// bytesToFloat32 converts raw image bytes to a dummy float32 slice.
// Real backends override this with actual image preprocessing (resize, normalize).
// The mock backend interprets the byte length as a proxy for detections.
func bytesToFloat32(b []byte) []float32 {
	out := make([]float32, len(b))
	for i, v := range b {
		out[i] = float32(v) / 255.0
	}
	return out
}

// postProcess applies confidence filtering and NMS to raw detections.
func postProcess(raws []RawDetection, confThresh, iouThresh float32) []Fish {
	// 1. Confidence filtering
	filtered := make([]RawDetection, 0, len(raws))
	for _, r := range raws {
		if r.Confidence >= confThresh {
			filtered = append(filtered, r)
		}
	}

	// 2. Sort by confidence descending
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Confidence > filtered[j].Confidence
	})

	// 3. NMS
	fishes := make([]Fish, 0, len(filtered))
	suppressed := make([]bool, len(filtered))

	for i := range filtered {
		if suppressed[i] {
			continue
		}
		f := filtered[i]
		fishes = append(fishes, Fish{
			BBox:       rawToPixelBBox(f.BBox),
			Class:      f.Class,
			Confidence: f.Confidence,
		})
		for j := i + 1; j < len(filtered); j++ {
			if suppressed[j] {
				continue
			}
			if computeIoU(filtered[i].BBox, filtered[j].BBox) >= iouThresh {
				suppressed[j] = true
			}
		}
	}

	return fishes
}

// rawToPixelBBox converts normalized [x_center, y_center, w, h] to
// pixel coordinates [x1, y1, x2, y2] assuming 640x640 input.
func rawToPixelBBox(norm [4]float32) [4]float32 {
	const imgSize float32 = 640.0
	cx, cy, w, h := norm[0], norm[1], norm[2], norm[3]
	return [4]float32{
		(cx - w/2) * imgSize,
		(cy - h/2) * imgSize,
		(cx + w/2) * imgSize,
		(cy + h/2) * imgSize,
	}
}

// computeIoU calculates the Intersection-over-Union of two normalized bboxes.
// Each bbox is [x_center, y_center, width, height] in [0,1].
func computeIoU(a, b [4]float32) float32 {
	// Convert center-format to corner-format
	ax1 := a[0] - a[2]/2
	ay1 := a[1] - a[3]/2
	ax2 := a[0] + a[2]/2
	ay2 := a[1] + a[3]/2

	bx1 := b[0] - b[2]/2
	by1 := b[1] - b[3]/2
	bx2 := b[0] + b[2]/2
	by2 := b[1] + b[3]/2

	// Intersection
	ix1 := float32(math.Max(float64(ax1), float64(bx1)))
	iy1 := float32(math.Max(float64(ay1), float64(by1)))
	ix2 := float32(math.Min(float64(ax2), float64(bx2)))
	iy2 := float32(math.Min(float64(ay2), float64(by2)))

	iw := float32(math.Max(0, float64(ix2-ix1)))
	ih := float32(math.Max(0, float64(iy2-iy1)))
	intersection := iw * ih

	areaA := a[2] * a[3]
	areaB := b[2] * b[3]
	union := areaA + areaB - intersection

	if union == 0 {
		return 0
	}
	return intersection / union
}
