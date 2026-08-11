package detector

import (
	"fmt"
)

// ONNXBackend wraps an ONNX Runtime inference session.
//
// Real implementation requires github.com/yalue/onnxruntime_go
// (which wraps the ONNX Runtime C API via CGO) or a pure-Go ONNX
// runtime like github.com/gorgonia/onnx-go.
//
// Dependencies (platform-specific):
//   - libonnxruntime.so (Linux aarch64/x86_64) or onnxruntime.dll (Windows)
//   - github.com/yalue/onnxruntime_go
//
// The current stub is a placeholder that returns an error when used.
// Use MockBackend for unit tests and integration development.
type ONNXBackend struct {
	modelPath string
}

// NewONNXBackend creates a new ONNX inference backend.
// The backend is not initialized until LoadModel is called.
func NewONNXBackend() *ONNXBackend {
	return &ONNXBackend{}
}

// LoadModel loads an ONNX model from the given path.
// The stub returns an error because the real ONNX Runtime is not linked.
func (b *ONNXBackend) LoadModel(path string) error {
	if path == "" {
		return fmt.Errorf("onnx backend: model path is empty")
	}
	b.modelPath = path
	return fmt.Errorf("onnx backend: ONNX Runtime not available (stub); use MockBackend for tests or link onnxruntime_go on target hardware")
}

// Infer would run a forward pass on the preprocessed tensor.
func (b *ONNXBackend) Infer(input []float32) ([]RawDetection, error) {
	return nil, fmt.Errorf("onnx backend: not implemented (stub)")
}

// Name returns the backend identifier.
func (b *ONNXBackend) Name() string {
	return "ONNX"
}

// Close releases ONNX Runtime resources.
func (b *ONNXBackend) Close() error {
	b.modelPath = ""
	return nil
}
