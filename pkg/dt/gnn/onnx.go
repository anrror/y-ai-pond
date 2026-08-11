package gnn

import "fmt"

// ONNXGNN wraps an ONNX Runtime inference session for the ST-GNN model.
//
// Real implementation requires github.com/yalue/onnxruntime_go
// (which wraps the ONNX Runtime C API via CGO, requiring libonnxruntime.so/dll).
//
// Dependencies (platform-specific):
//   - libonnxruntime.so (Linux aarch64/x86_64) or onnxruntime.dll (Windows)
//   - github.com/yalue/onnxruntime_go
//
// The current stub is a placeholder that returns an error when used.
// Use MockGNN for unit tests and integration development.
//
// When onnxruntime is available, wire it as follows:
//
//	import ort "github.com/yalue/onnxruntime_go"
//
//	func init() {
//	    ort.SetSharedLibraryPath("path/to/onnxruntime.dll")
//	    _ = ort.InitializeEnvironment()
//	}
type ONNXGNN struct {
	modelPath string
	loaded    bool
}

// NewONNXGNN creates an ONNX inference backend for ST-GNN multi-step
// water-quality forecasting. The backend is not initialized until LoadModel.
func NewONNXGNN() *ONNXGNN {
	return &ONNXGNN{}
}

// LoadModel loads an ONNX model from the given path.
//
// The stub returns an error because the real ONNX Runtime library is not
// linked. On a real deployment machine, this would:
//  1. Call ort.NewDynamicAdvancedSession(path, []string{"sensor_matrix"}, []string{"forecast"}, nil, nil)
//  2. Validate input tensor shape matches [N, 8] features
//  3. Validate output tensor shape matches [N, 3] horizons
func (b *ONNXGNN) LoadModel(path string) error {
	if path == "" {
		return fmt.Errorf("gnn: model path is empty")
	}
	b.modelPath = path
	return fmt.Errorf(
		"gnn: ONNX Runtime not available (stub); "+
			"use MockGNN for tests or link onnxruntime_go on target hardware. "+
			"Deployment: install libonnxruntime, add github.com/yalue/onnxruntime_go to go.mod, "+
			"then initialize with ort.SetSharedLibraryPath() + ort.InitializeEnvironment()")
}

// Predict runs inference over the flat sensor matrix (N*FeatureLen elements).
// It returns one Prediction per node (three DO horizons each).
func (b *ONNXGNN) Predict(matrix []float64) ([]Prediction, error) {
	if len(matrix) == 0 || len(matrix)%FeatureLen != 0 {
		return nil, fmt.Errorf("%w: len %d is not a multiple of %d", ErrInvalidInput, len(matrix), FeatureLen)
	}
	return nil, fmt.Errorf("gnn: not implemented (stub)")
}

// Name returns the backend identifier.
func (b *ONNXGNN) Name() string { return "ONNX" }

// Close releases ONNX Runtime resources.
func (b *ONNXGNN) Close() error {
	b.modelPath = ""
	b.loaded = false
	return nil
}