package detector

import (
	"fmt"
)

// TensorRTBackend wraps the NVIDIA TensorRT inference engine (Jetson GPU platform).
//
// Real implementation requires:
//   - NVIDIA TensorRT runtime (libnvinfer.so for Linux aarch64)
//   - CUDA/cuDNN libraries
//   - Model converted to .engine format (FP16 precision) via trtexec
//
// Conversion workflow:
//   PT (PyTorch) → ONNX → TensorRT Engine (via trtexec CLI)
//   See tools/convert_yolo.py for the conversion script.
//
// The current stub is a placeholder that returns an error when used.
// Use MockBackend for unit tests on non-Jetson hardware.
type TensorRTBackend struct {
	modelPath string
}

// NewTensorRTBackend creates a new TensorRT inference backend.
// The backend is not initialized until LoadModel is called.
func NewTensorRTBackend() *TensorRTBackend {
	return &TensorRTBackend{}
}

// LoadModel loads a TensorRT engine from the given path.
// The stub returns an error because the TensorRT runtime is not available.
func (b *TensorRTBackend) LoadModel(path string) error {
	if path == "" {
		return fmt.Errorf("tensorrt backend: model path is empty")
	}
	b.modelPath = path
	return fmt.Errorf("tensorrt backend: libnvinfer.so not available (stub); deploy on Jetson hardware with TensorRT")
}

// Infer would run a forward pass using the Jetson GPU.
func (b *TensorRTBackend) Infer(input []float32) ([]RawDetection, error) {
	return nil, fmt.Errorf("tensorrt backend: not implemented (stub)")
}

// Name returns the backend identifier.
func (b *TensorRTBackend) Name() string {
	return "TensorRT"
}

// Close releases TensorRT GPU resources.
func (b *TensorRTBackend) Close() error {
	b.modelPath = ""
	return nil
}
