package detector

import (
	"fmt"
)

// RKNBackend wraps the Rockchip RKNN NPU inference engine (RK3588 platform).
//
// Real implementation requires:
//   - Rockchip RKNN SDK (librknnrt.so for Linux aarch64)
//   - Model converted to .rknn format (INT8 quantized) via rknn-toolkit2
//
// Conversion workflow:
//   PT (PyTorch) → ONNX → RKNN (via rknn-toolkit2 Python API)
//   See tools/convert_yolo.py for the conversion script.
//
// The current stub is a placeholder that returns an error when used.
// Use MockBackend for unit tests on non-RK3588 hardware.
type RKNBackend struct {
	modelPath string
}

// NewRKNBackend creates a new RKNN inference backend.
// The backend is not initialized until LoadModel is called.
func NewRKNBackend() *RKNBackend {
	return &RKNBackend{}
}

// LoadModel loads an RKNN model from the given path.
// The stub returns an error because the RKNN runtime library is not available.
func (b *RKNBackend) LoadModel(path string) error {
	if path == "" {
		return fmt.Errorf("rknn backend: model path is empty")
	}
	b.modelPath = path
	return fmt.Errorf("rknn backend: librknnrt.so not available (stub); deploy on RK3588 hardware with RKNN SDK")
}

// Infer would run a forward pass using the RK3588 NPU.
func (b *RKNBackend) Infer(input []float32) ([]RawDetection, error) {
	return nil, fmt.Errorf("rknn backend: not implemented (stub)")
}

// Name returns the backend identifier.
func (b *RKNBackend) Name() string {
	return "RKNN"
}

// Close releases RKNN NPU resources.
func (b *RKNBackend) Close() error {
	b.modelPath = ""
	return nil
}
