package rl

import (
	"fmt"
)

// ONNXPolicy wraps an ONNX Runtime inference session for the DDPG actor network.
//
// Real implementation requires github.com/yalue/onnxruntime_go
// (which wraps the ONNX Runtime C API via CGO, requiring libonnxruntime.so/dll).
//
// Dependencies (platform-specific):
//   - libonnxruntime.so (Linux aarch64/x86_64) or onnxruntime.dll (Windows)
//   - github.com/yalue/onnxruntime_go
//
// The current stub is a placeholder that returns an error when used.
// Use MockPolicy for unit tests and integration development.
//
// When onnxruntime is available, wire it as follows:
//
//	import ort "github.com/yalue/onnxruntime_go"
//
//	func init() {
//	    ort.SetSharedLibraryPath("path/to/onnxruntime.dll")
//	    _ = ort.InitializeEnvironment()
//	}
type ONNXPolicy struct {
	modelPath string
	loaded    bool

	// inputShape  = [1, 5]  — batch=1, state features
	// outputShape = [1, 1]  — batch=1, feeding_rate
	expectedInputDim  int
	expectedOutputDim int
}

// NewONNXPolicy creates an ONNX inference backend for the DDPG actor.
// The backend is not initialized until LoadModel is called.
func NewONNXPolicy() *ONNXPolicy {
	return &ONNXPolicy{
		expectedInputDim:  StateLen,
		expectedOutputDim: 1,
	}
}

// LoadModel loads an ONNX model from the given path.
//
// The stub returns an error because the real ONNX Runtime library is not linked.
// On a real deployment machine, this would:
//  1. Call ort.NewDynamicAdvancedSession(path, []string{"input"}, []string{"output"}, nil, nil)
//  2. Validate input tensor shape matches [1, 5]
//  3. Validate output tensor shape matches [1, 1]
func (b *ONNXPolicy) LoadModel(path string) error {
	if path == "" {
		return fmt.Errorf("onnx policy: model path is empty")
	}
	b.modelPath = path
	return fmt.Errorf(
		"onnx policy: ONNX Runtime not available (stub); "+
			"use MockPolicy for tests or link onnxruntime_go on target hardware. "+
			"Deployment: install libonnxruntime, add github.com/yalue/onnxruntime_go to go.mod, "+
			"then initialize with ort.SetSharedLibraryPath() + ort.InitializeEnvironment()")
}

// Predict would run inference: state[5] → actor network → feeding_rate[1].
func (b *ONNXPolicy) Predict(state []float64) (float64, error) {
	return 0, fmt.Errorf("onnx policy: not implemented (stub)")
}

// Name returns the backend identifier.
func (b *ONNXPolicy) Name() string {
	return "ONNX"
}

// Close releases ONNX Runtime resources.
func (b *ONNXPolicy) Close() error {
	b.modelPath = ""
	b.loaded = false
	return nil
}

// ValidateShapes checks whether the loaded model's input and output
// dimensions match the expected DDPG actor architecture.
//
//	inputShape: expected [1, 5] (batch=1, state features)
//	outputShape: expected [1, 1] (batch=1, feeding_rate)
//
// Returns ErrShapeMismatch with a descriptive message on mismatch.
func ValidateShapes(inputDim, outputDim int) error {
	var issues []string
	if inputDim != StateLen {
		issues = append(issues,
			fmt.Sprintf("input dimension mismatch: expected %d, got %d", StateLen, inputDim),
		)
	}
	if outputDim != 1 {
		issues = append(issues,
			fmt.Sprintf("output dimension mismatch: expected 1, got %d", outputDim),
		)
	}
	if len(issues) > 0 {
		return fmt.Errorf("%w: %s", ErrShapeMismatch, joinIssues(issues))
	}
	return nil
}

// ValidateInputShape is a convenience wrapper around ValidateShapes for
// input-only checks (used when output shape is not yet known).
func ValidateInputShape(inputDim int) error {
	if inputDim != StateLen {
		return fmt.Errorf("%w: expected input dimension %d, got %d", ErrShapeMismatch, StateLen, inputDim)
	}
	return nil
}

// ValidateOutputShape checks that the output dimension matches the expected
// single-action (feeding_rate) output.
func ValidateOutputShape(outputDim int) error {
	if outputDim != 1 {
		return fmt.Errorf("%w: expected output dimension 1, got %d", ErrShapeMismatch, outputDim)
	}
	return nil
}

func joinIssues(issues []string) string {
	result := ""
	for i, s := range issues {
		if i > 0 {
			result += "; "
		}
		result += s
	}
	return result
}
