// Package rl provides the reinforcement learning inference pipeline for
// feeding strategy optimization. The Python side (python/rl/) handles offline
// DDPG training and exports ONNX models; the Go side loads the ONNX model
// via the RLPolicy interface and performs low-latency inference.
//
// Architecture:
//   - RLPolicy: pluggable interface for ONNX/Mock backends (mirrors T8's Backend).
//   - PolicyEngine: wraps a policy + state validation + output clamping.
//   - Multi-objective reward: FCR improvement × 0.4 + water stability × 0.3 + energy reduction × 0.3.
//
// Real ONNX inference requires github.com/yalue/onnxruntime_go (CGO wrapper
// around the ONNX Runtime C library). The ONNXPolicy stub returns a clear
// error with deployment instructions when the library is not linked.
// Use MockPolicy for CI testing and development.
//
// Safety boundary: RL output is advisory (feeding_rate in [0,1]).
// It does NOT override the fuzzy controller's hardware safety interlocks
// (DO < 4.0 → aerator ON, motor overcurrent → STOP, emergency stop).
package rl

import (
	"errors"
	"fmt"
	"math"
)

// ============================================================================
// Domain errors
// ============================================================================

var (
	// ErrInvalidState is returned when the state vector has the wrong length
	// or contains values outside documented bounds.
	ErrInvalidState = errors.New("rl: invalid state vector")
	// ErrModelNotLoaded is returned when Predict is called before LoadModel.
	ErrModelNotLoaded = errors.New("rl: model not loaded")
	// ErrInferenceFailed is returned when the ONNX runtime fails.
	ErrInferenceFailed = errors.New("rl: inference failed")
	// ErrShapeMismatch is returned when model input/output shapes do not match expectations.
	ErrShapeMismatch = errors.New("rl: model input/output shape mismatch")
)

// ============================================================================
// State definition
// ============================================================================

// StateLen is the required number of elements in a state vector.
const StateLen = 5

// State indices for documentation and clarity.
const (
	IdxDO         = 0 // Dissolved oxygen (mg/L)
	IdxTemp       = 1 // Water temperature (°C)
	IdxNH3        = 2 // Ammonia nitrogen (mg/L)
	IdxFishWeight = 3 // Average fish weight (g)
	IdxFCR        = 4 // Feed Conversion Ratio (dimensionless)
)

// State bounds (inclusive) for value range validation.
var (
	StateMin = [StateLen]float64{0, 0, 0, 0, 0.1}
	StateMax = [StateLen]float64{20, 50, 10, 1e6, 10.0}
)

// StateLabel returns a human-readable label for each state dimension.
func StateLabel(i int) string {
	switch i {
	case IdxDO:
		return "DO"
	case IdxTemp:
		return "Temp"
	case IdxNH3:
		return "NH3"
	case IdxFishWeight:
		return "FishWeight"
	case IdxFCR:
		return "FCR"
	default:
		return fmt.Sprintf("State[%d]", i)
	}
}

// ============================================================================
// RLPolicy interface
// ============================================================================

// RLPolicy is the interface for DDPG policy inference backends.
// Each backend wraps a specific runtime: onnxer (ONNX Runtime via CGO),
// or a deterministic in-memory mock for CI testing.
//
// Implementations must be safe for concurrent use.
type RLPolicy interface {
	// Predict runs a forward pass with the given state vector and returns
	// the recommended feeding rate. The state must contain exactly StateLen
	// elements: [DO, Temp, NH3, FishWeight, FCR].
	//
	// Returns a feeding_rate in [0, 1] (0 = no feeding, 1 = maximum).
	Predict(state []float64) (float64, error)

	// Name returns a human-readable backend identifier (e.g., "ONNX", "Mock").
	Name() string

	// Close releases backend resources.
	Close() error
}

// ============================================================================
// Configuration
// ============================================================================

// Config holds tunable parameters for the RL inference engine.
type Config struct {
	// ClipMin is the minimum feeding rate clamp. Default: 0.
	ClipMin float64

	// ClipMax is the maximum feeding rate clamp. Default: 1.
	ClipMax float64

	// EnableRangeCheck enables state value range validation (bounds check).
	// Disable for raw ONNX inference where the model handles outliers.
	// Default: true.
	EnableRangeCheck bool
}

// DefaultConfig returns the recommended configuration for RL inference.
func DefaultConfig() Config {
	return Config{
		ClipMin:          0,
		ClipMax:          1,
		EnableRangeCheck: true,
	}
}

// ============================================================================
// Multi-objective reward function
// ============================================================================

// RewardWeights holds the coefficients for the multi-objective reward function.
type RewardWeights struct {
	FCRWeight    float64 // Weight for FCR improvement (default 0.4)
	WaterWeight  float64 // Weight for water quality stability (default 0.3)
	EnergyWeight float64 // Weight for energy reduction (default 0.3)
}

// DefaultRewardWeights returns the standard multi-objective reward weights.
// FCR improvement × 0.4 + water quality stability × 0.3 + energy reduction × 0.3.
func DefaultRewardWeights() RewardWeights {
	return RewardWeights{
		FCRWeight:    0.4,
		WaterWeight:  0.3,
		EnergyWeight: 0.3,
	}
}

// ComputeReward calculates the multi-objective reward for a given step.
//
//	fcrImprovement:  improvement in Feed Conversion Ratio (higher = better).
//	waterStability:  water quality stability score (higher = more stable).
//	energyReduction: energy savings relative to baseline (higher = more savings).
//
// All inputs are expected to be normalized to comparable ranges (e.g., [0, 1]).
// Returns the weighted sum.
func ComputeReward(fcrImprovement, waterStability, energyReduction float64) float64 {
	return computeRewardWithWeights(fcrImprovement, waterStability, energyReduction, DefaultRewardWeights())
}

// computeRewardWithWeights is the test-injectable variant.
func computeRewardWithWeights(fcrImprovement, waterStability, energyReduction float64, w RewardWeights) float64 {
	return w.FCRWeight*fcrImprovement + w.WaterWeight*waterStability + w.EnergyWeight*energyReduction
}

// ============================================================================
// State validation
// ============================================================================

// ValidateState checks that a state vector has the correct length and all
// values are within documented bounds. Returns nil if valid, or an error
// describing the first violation found.
func ValidateState(state []float64) error {
	if len(state) != StateLen {
		return fmt.Errorf("%w: expected %d elements, got %d", ErrInvalidState, StateLen, len(state))
	}
	for i, v := range state {
		if math.IsNaN(v) {
			return fmt.Errorf("%w: %s is NaN", ErrInvalidState, StateLabel(i))
		}
		if math.IsInf(v, 0) {
			return fmt.Errorf("%w: %s is ±Inf", ErrInvalidState, StateLabel(i))
		}
		if v < StateMin[i] {
			return fmt.Errorf("%w: %s value %g below minimum %g", ErrInvalidState, StateLabel(i), v, StateMin[i])
		}
		if v > StateMax[i] {
			return fmt.Errorf("%w: %s value %g above maximum %g", ErrInvalidState, StateLabel(i), v, StateMax[i])
		}
	}
	return nil
}

// ============================================================================
// PolicyEngine
// ============================================================================

// PolicyEngine wraps an RLPolicy with state validation and output clamping.
// It is the primary entry point for feeding strategy inference.
//
// Usage:
//
//	policy := rl.NewMockPolicy()
//	engine := rl.NewPolicyEngine(policy, rl.DefaultConfig())
//	rate, err := engine.Predict([]float64{7.5, 25.0, 0.1, 500.0, 1.5})
type PolicyEngine struct {
	policy RLPolicy
	cfg    Config
}

// EngineOption is a functional option for NewPolicyEngine.
type EngineOption func(*PolicyEngine)

// WithConfig sets a custom configuration.
func WithConfig(cfg Config) EngineOption {
	return func(e *PolicyEngine) { e.cfg = cfg }
}

// NewPolicyEngine creates a PolicyEngine wrapping the given RLPolicy.
// It does NOT load the model; call LoadModel (if the policy supports it) before Predict.
func NewPolicyEngine(policy RLPolicy, opts ...EngineOption) *PolicyEngine {
	e := &PolicyEngine{
		policy: policy,
		cfg:    DefaultConfig(),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Predict validates the state vector, runs inference, and clamps the output
// to [ClipMin, ClipMax]. Returns the recommended feeding rate.
//
// State must contain exactly 5 elements: [DO (mg/L), Temp (°C), NH3 (mg/L),
// FishWeight (g), FCR].
func (e *PolicyEngine) Predict(state []float64) (float64, error) {
	if e.policy == nil {
		return 0, fmt.Errorf("rl: policy is nil")
	}

	// State validation
	if e.cfg.EnableRangeCheck {
		if err := ValidateState(state); err != nil {
			return 0, err
		}
	} else if len(state) != StateLen {
		return 0, fmt.Errorf("%w: expected %d elements, got %d", ErrInvalidState, StateLen, len(state))
	}

	// Inference
	action, err := e.policy.Predict(state)
	if err != nil {
		return 0, fmt.Errorf("rl predict: %w", err)
	}

	// Clamp output
	return clamp(action, e.cfg.ClipMin, e.cfg.ClipMax), nil
}

// PolicyName returns the name of the underlying policy backend.
func (e *PolicyEngine) PolicyName() string {
	if e.policy == nil {
		return "nil"
	}
	return e.policy.Name()
}

// Close releases the underlying policy backend resources.
func (e *PolicyEngine) Close() error {
	if e.policy == nil {
		return nil
	}
	return e.policy.Close()
}

// clamp restricts v to the range [lo, hi].
func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
