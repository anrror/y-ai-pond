package rl

import (
	"fmt"
	"math"
)

// MockPolicy is a deterministic in-memory policy for unit testing.
// It implements a simplified feeding strategy heuristic:
//
//	feeding_rate = sigmoid(w1*DO_norm + w2*Temp_score + w3*NH3_score + w4*Weight_score + w5*FCR_score)
//
// where all sub-scores are normalised to [-1, 1] and sigmoid(x) = 1/(1+e^(-x)).
// The weights approximate a reasonable feeding strategy:
//   - High DO → more feeding
//   - Moderate temp (20-30°C) → more feeding
//   - Low NH3 → more feeding
//   - Larger fish → slightly higher feeding rate
//   - Higher FCR (less efficient) → slightly lower feeding rate
//
// Use NewMockPolicy() for the default configuration.
// Feed the output into PolicyEngine for validation and clamping.
type MockPolicy struct {
	labeled bool // true after LoadModel is called (not strictly needed for mock, but mirrors real backends)
}

// compile-time interface check
var _ RLPolicy = (*MockPolicy)(nil)

// NewMockPolicy creates a MockPolicy with the default heuristic weights.
func NewMockPolicy() *MockPolicy {
	return &MockPolicy{}
}

// Predict computes a deterministic feeding rate from the state vector.
// The state must contain exactly 5 elements: [DO, Temp, NH3, FishWeight, FCR].
//
// Returns a value in approximately [0, 1] (sigmoid output, bounded away
// from exact 0/1 by the sigmoid asymptotes).
func (m *MockPolicy) Predict(state []float64) (float64, error) {
	if len(state) != StateLen {
		return 0, fmt.Errorf("mock policy: expected %d state elements, got %d", StateLen, len(state))
	}
	if !m.labeled {
		// Not loaded — still works for mock, but warn via error for API correctness.
		return 0, fmt.Errorf("mock policy: model not loaded; call LoadModel first")
	}

	// --- Heuristic feature scoring (each normalised to [-1, 1]) ---

	// DO: optimal around 6-8 mg/L. Score: 0 at 0 mg/L, peaks at 7, drops at 14+.
	do := state[IdxDO]
	doScore := math.Tanh((do-3.5)/3.5) // tanh maps 0→-0.76, 7→0.76, 14→0.96

	// Temp: optimal 22-28°C for most species. Score peaks at 25°C.
	temp := state[IdxTemp]
	tempDiff := math.Abs(temp - 25.0)
	tempScore := 1.0 - tempDiff/25.0 // 25→1.0, 0 or 50→0.0
	tempScore = math.Max(-1, math.Min(1, tempScore))

	// NH3: lower is better. Score: 0→1.0, 1.0→0.0, 5.0→-1.0.
	nh3 := state[IdxNH3]
	nh3Score := 1.0 - nh3 // 0.0→1, 0.5→0.5, 1.0→0
	nh3Score = math.Max(-1, math.Min(1, nh3Score))

	// FishWeight: moderate weight (200-1000g) → maintain feeding; small fish → higher rate.
	weight := state[IdxFishWeight]
	weightNorm := math.Log1p(weight) / 10.0 // log-scaled, approx [0, 1] for 0-1e6g
	weightScore := weightNorm*2 - 1          // map to [-1, 1]

	// FCR: lower is better. Typical FCR range 0.5-5.0.
	fcr := state[IdxFCR]
	fcrScore := 1.0 - (fcr-0.5)/4.5*2 // 0.5→1, 2.75→0, 5.0→-1
	fcrScore = math.Max(-1, math.Min(1, fcrScore))

	// --- Weighted sum ---
	// Approximate hand-tuned weights reflecting the reward decomposition
	// (FCR × 0.4, water × 0.3, energy × 0.3).
	// Water quality: DO + NH3. Energy: feeding rate self-regulates via temperature.
	// FCR: directly penalizes overfeeding.
	const (
		wDO         = 0.25
		wTemp       = 0.15
		wNH3        = 0.20
		wWeight     = 0.15
		wFCR        = 0.25
	)
	z := wDO*doScore + wTemp*tempScore + wNH3*nh3Score + wWeight*weightScore + wFCR*fcrScore

	// Sigmoid activation: maps ℝ → (0, 1)
	feedingRate := 1.0 / (1.0 + math.Exp(-z))

	return feedingRate, nil
}

// Name returns the backend identifier.
func (m *MockPolicy) Name() string {
	return "Mock"
}

// Close is a no-op for the mock policy.
func (m *MockPolicy) Close() error {
	return nil
}

// LoadModel is a no-op that marks the policy as loaded.
// This mirrors the real backend lifecycle but does nothing for the mock.
func (m *MockPolicy) LoadModel(path string) error {
	m.labeled = true
	return nil
}
