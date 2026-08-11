package rl

import (
	"math"
	"testing"
)

// ============================================================================
// Reward function tests
// ============================================================================

func TestComputeReward(t *testing.T) {
	tests := []struct {
		name             string
		fcrImprovement   float64
		waterStability   float64
		energyReduction  float64
		want             float64
	}{
		{
			name:            "all zero",
			fcrImprovement:  0,
			waterStability:  0,
			energyReduction: 0,
			want:            0,
		},
		{
			name:            "all one",
			fcrImprovement:  1.0,
			waterStability:  1.0,
			energyReduction: 1.0,
			want:            1.0, // 0.4 + 0.3 + 0.3 = 1.0
		},
		{
			name:            "only FCR",
			fcrImprovement:  0.5,
			waterStability:  0,
			energyReduction: 0,
			want:            0.2, // 0.4 * 0.5 = 0.2
		},
		{
			name:            "only water",
			fcrImprovement:  0,
			waterStability:  0.8,
			energyReduction: 0,
			want:            0.24, // 0.3 * 0.8 = 0.24
		},
		{
			name:            "only energy",
			fcrImprovement:  0,
			waterStability:  0,
			energyReduction: 0.6,
			want:            0.18, // 0.3 * 0.6 = 0.18
		},
		{
			name:            "mixed",
			fcrImprovement:  0.7,
			waterStability:  0.5,
			energyReduction: 0.3,
			want:            0.28 + 0.15 + 0.09, // 0.4*0.7 + 0.3*0.5 + 0.3*0.3 = 0.52
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeReward(tt.fcrImprovement, tt.waterStability, tt.energyReduction)
			if math.Abs(got-tt.want) > 1e-9 {
				t.Errorf("ComputeReward(%g, %g, %g) = %g, want %g",
					tt.fcrImprovement, tt.waterStability, tt.energyReduction, got, tt.want)
			}
		})
	}
}

func TestComputeRewardWeights(t *testing.T) {
	// Verify that DefaultRewardWeights sums to 1.0 and each weight is positive.
	w := DefaultRewardWeights()
	if sum := w.FCRWeight + w.WaterWeight + w.EnergyWeight; math.Abs(sum-1.0) > 1e-9 {
		t.Errorf("weights sum = %g, want 1.0", sum)
	}
	if w.FCRWeight <= 0 || w.WaterWeight <= 0 || w.EnergyWeight <= 0 {
		t.Error("all reward weights must be positive")
	}
}

// TestComputeRewardParity verifies Go reward matches the Python formula (0.4/0.3/0.3).
func TestComputeRewardParity(t *testing.T) {
	// This test documents the exact weights so that the Python feeding_env.py
	// can use the same formula. Any change here must be reflected in Python.
	w := DefaultRewardWeights()

	// FCR must be exactly 0.4
	if math.Abs(w.FCRWeight-0.4) > 1e-9 {
		t.Errorf("FCR weight changed: got %g, must be 0.4 for Python parity", w.FCRWeight)
	}
	// Water must be exactly 0.3
	if math.Abs(w.WaterWeight-0.3) > 1e-9 {
		t.Errorf("Water weight changed: got %g, must be 0.3 for Python parity", w.WaterWeight)
	}
	// Energy must be exactly 0.3
	if math.Abs(w.EnergyWeight-0.3) > 1e-9 {
		t.Errorf("Energy weight changed: got %g, must be 0.3 for Python parity", w.EnergyWeight)
	}
}

// ============================================================================
// State validation tests
// ============================================================================

func TestValidateState_OK(t *testing.T) {
	validState := []float64{7.5, 25.0, 0.1, 500.0, 1.5}
	if err := ValidateState(validState); err != nil {
		t.Errorf("ValidateState(%v) unexpected error: %v", validState, err)
	}
}

func TestValidateState_WrongLength(t *testing.T) {
	tests := []struct {
		name  string
		state []float64
	}{
		{"too few (4 elements)", []float64{7.5, 25.0, 0.1, 500.0}},
		{"too many (6 elements)", []float64{7.5, 25.0, 0.1, 500.0, 1.5, 99.0}},
		{"empty", []float64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateState(tt.state)
			if err == nil {
				t.Errorf("expected error for state of length %d, got nil", len(tt.state))
			}
			if !isErrInvalidState(err) {
				t.Errorf("expected ErrInvalidState, got %v", err)
			}
		})
	}
}

func TestValidateState_OutOfRange(t *testing.T) {
	tests := []struct {
		name  string
		state []float64
		desc  string
	}{
		{"DO below min", []float64{-1, 25, 0.1, 500, 1.5}, "DO < 0"},
		{"DO above max", []float64{21, 25, 0.1, 500, 1.5}, "DO > 20"},
		{"Temp below min", []float64{7.5, -5, 0.1, 500, 1.5}, "Temp < 0"},
		{"Temp above max", []float64{7.5, 55, 0.1, 500, 1.5}, "Temp > 50"},
		{"NH3 below min", []float64{7.5, 25, -0.5, 500, 1.5}, "NH3 < 0"},
		{"NH3 above max", []float64{7.5, 25, 15, 500, 1.5}, "NH3 > 10"},
		{"FishWeight below min", []float64{7.5, 25, 0.1, -100, 1.5}, "FishWeight < 0"},
		{"FCR below min", []float64{7.5, 25, 0.1, 500, 0.05}, "FCR < 0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateState(tt.state)
			if err == nil {
				t.Errorf("expected error for %s, got nil", tt.desc)
			}
			if !isErrInvalidState(err) {
				t.Errorf("expected ErrInvalidState for %s, got %v", tt.desc, err)
			}
		})
	}
}

func TestValidateState_NaN_Inf(t *testing.T) {
	tests := []struct {
		name  string
		state []float64
	}{
		{"DO is NaN", appendNaN([]float64{25, 0.1, 500, 1.5}, 0)},
		{"Temp is NaN", appendNaN([]float64{7.5, 0.1, 500, 1.5}, 1)},
		{"DO is +Inf", appendInf([]float64{25, 0.1, 500, 1.5}, 0, 1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateState(tt.state)
			if err == nil {
				t.Errorf("expected error for state with NaN/Inf, got nil")
			}
		})
	}
}

// ============================================================================
// MockPolicy tests
// ============================================================================

func TestMockPolicy_OutputRange(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("mock"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}

	states := [][]float64{
		{7.5, 25.0, 0.1, 500.0, 1.5}, // normal
		{3.0, 30.0, 0.8, 200.0, 2.5}, // stressed
		{9.0, 22.0, 0.05, 800.0, 1.2}, // excellent
		{5.0, 15.0, 0.3, 300.0, 1.8}, // moderate
		{2.0, 35.0, 1.5, 100.0, 3.5}, // poor
	}

	for i, state := range states {
		rate, err := mp.Predict(state)
		if err != nil {
			t.Errorf("state %d: Predict failed: %v", i, err)
			continue
		}
		if rate < 0 || rate > 1 {
			t.Errorf("state %d: feeding_rate %g out of [0,1]", i, rate)
		}
	}
}

func TestMockPolicy_DifferentStatesDifferentOutputs(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("mock"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}

	// Good state (high DO, low NH3) should produce higher feeding rate
	goodState := []float64{9.0, 25.0, 0.05, 800.0, 1.2}
	badState := []float64{3.0, 30.0, 1.5, 200.0, 3.0}

	goodRate, err := mp.Predict(goodState)
	if err != nil {
		t.Fatalf("Predict(good) failed: %v", err)
	}
	badRate, err := mp.Predict(badState)
	if err != nil {
		t.Fatalf("Predict(bad) failed: %v", err)
	}

	if goodRate <= badRate {
		t.Errorf("good state feeding rate %g should be > bad state rate %g", goodRate, badRate)
	}
}

func TestMockPolicy_NotLoaded(t *testing.T) {
	mp := NewMockPolicy()
	// Not calling LoadModel
	_, err := mp.Predict([]float64{7.5, 25.0, 0.1, 500.0, 1.5})
	if err == nil {
		t.Error("expected error when model not loaded, got nil")
	}
}

func TestMockPolicy_WrongLength(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("mock"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}

	_, err := mp.Predict([]float64{7.5, 25.0, 0.1, 500.0}) // 4 elements
	if err == nil {
		t.Error("expected error for wrong state length, got nil")
	}
}

func TestMockPolicy_Name(t *testing.T) {
	mp := NewMockPolicy()
	if got := mp.Name(); got != "Mock" {
		t.Errorf("Name() = %q, want %q", got, "Mock")
	}
}

func TestMockPolicy_Close(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

// ============================================================================
// ONNXPolicy stub tests
// ============================================================================

func TestONNXPolicy_Name(t *testing.T) {
	op := NewONNXPolicy()
	if got := op.Name(); got != "ONNX" {
		t.Errorf("Name() = %q, want %q", got, "ONNX")
	}
}

func TestONNXPolicy_LoadModel_StubError(t *testing.T) {
	op := NewONNXPolicy()
	err := op.LoadModel("/fake/model.onnx")
	if err == nil {
		t.Error("expected stub error from ONNXPolicy.LoadModel, got nil")
	}
}

func TestONNXPolicy_LoadModel_EmptyPath(t *testing.T) {
	op := NewONNXPolicy()
	err := op.LoadModel("")
	if err == nil {
		t.Error("expected error for empty model path, got nil")
	}
}

func TestONNXPolicy_Predict_StubError(t *testing.T) {
	op := NewONNXPolicy()
	_, err := op.Predict([]float64{7.5, 25.0, 0.1, 500.0, 1.5})
	if err == nil {
		t.Error("expected stub error from ONNXPolicy.Predict, got nil")
	}
}

func TestONNXPolicy_Close(t *testing.T) {
	op := NewONNXPolicy()
	if err := op.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

// ============================================================================
// Model validation tests
// ============================================================================

func TestValidateShapes_OK(t *testing.T) {
	if err := ValidateShapes(StateLen, 1); err != nil {
		t.Errorf("ValidateShapes(5, 1) unexpected error: %v", err)
	}
}

func TestValidateShapes_WrongInput(t *testing.T) {
	err := ValidateShapes(4, 1)
	if err == nil {
		t.Error("expected error for input dim 4, got nil")
	}
	if !isErrShapeMismatch(err) {
		t.Errorf("expected ErrShapeMismatch, got %v", err)
	}
}

func TestValidateShapes_WrongOutput(t *testing.T) {
	err := ValidateShapes(StateLen, 3)
	if err == nil {
		t.Error("expected error for output dim 3, got nil")
	}
	if !isErrShapeMismatch(err) {
		t.Errorf("expected ErrShapeMismatch, got %v", err)
	}
}

func TestValidateShapes_BothWrong(t *testing.T) {
	err := ValidateShapes(2, 5)
	if err == nil {
		t.Error("expected error for both wrong, got nil")
	}
}

func TestValidateInputShape(t *testing.T) {
	if err := ValidateInputShape(StateLen); err != nil {
		t.Errorf("ValidateInputShape(5) unexpected error: %v", err)
	}
	if err := ValidateInputShape(3); err == nil {
		t.Error("expected error for input dim 3, got nil")
	}
}

func TestValidateOutputShape(t *testing.T) {
	if err := ValidateOutputShape(1); err != nil {
		t.Errorf("ValidateOutputShape(1) unexpected error: %v", err)
	}
	if err := ValidateOutputShape(2); err == nil {
		t.Error("expected error for output dim 2, got nil")
	}
}

// ============================================================================
// PolicyEngine tests
// ============================================================================

func TestPolicyEngine_Predict(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("mock"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}

	engine := NewPolicyEngine(mp)
	state := []float64{7.5, 25.0, 0.1, 500.0, 1.5}

	rate, err := engine.Predict(state)
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}
	if rate < 0 || rate > 1 {
		t.Errorf("feeding_rate %g out of [0,1]", rate)
	}
}

func TestPolicyEngine_StateValidation(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("mock"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}

	engine := NewPolicyEngine(mp)

	// Wrong length
	_, err := engine.Predict([]float64{7.5, 25.0, 0.1, 500.0})
	if err == nil {
		t.Error("expected error for 4-element state, got nil")
	}

	// Out of range
	_, err = engine.Predict([]float64{99, 99, 99, 99, 99})
	if err == nil {
		t.Error("expected error for out-of-range state, got nil")
	}
}

func TestPolicyEngine_DisableRangeCheck(t *testing.T) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("mock"); err != nil {
		t.Fatalf("LoadModel failed: %v", err)
	}

	cfg := DefaultConfig()
	cfg.EnableRangeCheck = false
	engine := NewPolicyEngine(mp, WithConfig(cfg))

	// Out of range values pass (only length check applies)
	_, err := engine.Predict([]float64{99, 99, 99, 99, 99})
	if err != nil {
		t.Errorf("expected no error with range check disabled, got: %v", err)
	}

	// Wrong length still fails
	_, err = engine.Predict([]float64{1, 2, 3, 4})
	if err == nil {
		t.Error("expected error for wrong length even with range check disabled")
	}
}

func TestPolicyEngine_NilPolicy(t *testing.T) {
	engine := NewPolicyEngine(nil)
	_, err := engine.Predict([]float64{7.5, 25.0, 0.1, 500.0, 1.5})
	if err == nil {
		t.Error("expected error for nil policy, got nil")
	}
}

func TestPolicyEngine_PolicyName(t *testing.T) {
	mp := NewMockPolicy()
	engine := NewPolicyEngine(mp)
	if got := engine.PolicyName(); got != "Mock" {
		t.Errorf("PolicyName() = %q, want %q", got, "Mock")
	}

	nilEngine := NewPolicyEngine(nil)
	if got := nilEngine.PolicyName(); got != "nil" {
		t.Errorf("PolicyName() for nil = %q, want %q", got, "nil")
	}
}

func TestPolicyEngine_Close(t *testing.T) {
	mp := NewMockPolicy()
	engine := NewPolicyEngine(mp)
	if err := engine.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

// ============================================================================
// Interface compliance test
// ============================================================================

// TestInterfaceCompliance verifies that MockPolicy and ONNXPolicy satisfy RLPolicy
// at compile time (via var _ RLPolicy = (*MockPolicy)(nil) in mock.go)
// and at runtime.
func TestInterfaceCompliance(t *testing.T) {
	// MockPolicy runtime compliance
	mp := NewMockPolicy()
	name := mp.Name()
	if name != "Mock" {
		t.Errorf("MockPolicy.Name() = %q, want Mock", name)
	}

	// ONNXPolicy runtime compliance
	op := NewONNXPolicy()
	onnxName := op.Name()
	if onnxName != "ONNX" {
		t.Errorf("ONNXPolicy.Name() = %q, want ONNX", onnxName)
	}

	// Verify RLPolicy interface is usable with both backends
	policies := []RLPolicy{mp, op}
	for _, p := range policies {
		if n := p.Name(); n != "Mock" && n != "ONNX" {
			t.Errorf("unexpected policy name: %q", n)
		}
	}
}

// ============================================================================
// State label tests
// ============================================================================

func TestStateLabel(t *testing.T) {
	labels := []string{"DO", "Temp", "NH3", "FishWeight", "FCR"}
	for i, want := range labels {
		if got := StateLabel(i); got != want {
			t.Errorf("StateLabel(%d) = %q, want %q", i, got, want)
		}
	}
	// Out of range
	if got := StateLabel(StateLen); got != "State[5]" {
		t.Errorf("StateLabel(5) = %q, want State[5]", got)
	}
}

// ============================================================================
// Config defaults
// ============================================================================

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ClipMin != 0 {
		t.Errorf("ClipMin = %g, want 0", cfg.ClipMin)
	}
	if cfg.ClipMax != 1 {
		t.Errorf("ClipMax = %g, want 1", cfg.ClipMax)
	}
	if !cfg.EnableRangeCheck {
		t.Error("EnableRangeCheck should default to true")
	}
}

// ============================================================================
// Benchmarks
// ============================================================================

// BenchmarkPredict measures MockPolicy inference latency.
// Target: < 1ms (1,000,000 ns). Real ONNX adds ~0.1ms.
func BenchmarkPredict(b *testing.B) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("bench"); err != nil {
		b.Fatalf("LoadModel failed: %v", err)
	}
	state := []float64{7.5, 25.0, 0.1, 500.0, 1.5}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := mp.Predict(state)
		if err != nil {
			b.Fatalf("Predict failed: %v", err)
		}
	}
}

// BenchmarkPolicyEngine measures the full PolicyEngine pipeline (validation + inference + clamp).
func BenchmarkPolicyEngine(b *testing.B) {
	mp := NewMockPolicy()
	if err := mp.LoadModel("bench"); err != nil {
		b.Fatalf("LoadModel failed: %v", err)
	}
	engine := NewPolicyEngine(mp)
	state := []float64{7.5, 25.0, 0.1, 500.0, 1.5}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Predict(state)
		if err != nil {
			b.Fatalf("Predict failed: %v", err)
		}
	}
}

// ============================================================================
// Test helpers
// ============================================================================

// isErrInvalidState checks if err wraps ErrInvalidState.
func isErrInvalidState(err error) bool {
	for e := err; e != nil; {
		// Check unwrapped
		if e == ErrInvalidState {
			return true
		}
		// Check message pattern
		if e.Error()[:len("rl: invalid state vector")] == "rl: invalid state vector" {
			return true
		}
		// Try unwrap
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}

// isErrShapeMismatch checks if err wraps ErrShapeMismatch.
func isErrShapeMismatch(err error) bool {
	for e := err; e != nil; {
		if e == ErrShapeMismatch {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := e.(unwrapper)
		if !ok {
			break
		}
		e = u.Unwrap()
	}
	return false
}

// appendNaN inserts NaN at the given index.
func appendNaN(base []float64, idx int) []float64 {
	result := make([]float64, StateLen)
	j := 0
	for i := 0; i < StateLen; i++ {
		if i == idx {
			result[i] = math.NaN()
			continue
		}
		result[i] = base[j]
		j++
	}
	return result
}

// appendInf inserts +Inf or -Inf at the given index.
func appendInf(base []float64, idx int, sign int) []float64 {
	result := make([]float64, StateLen)
	j := 0
	for i := 0; i < StateLen; i++ {
		if i == idx {
			result[i] = math.Inf(sign)
			continue
		}
		result[i] = base[j]
		j++
	}
	return result
}
