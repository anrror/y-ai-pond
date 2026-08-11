// Package fuzzypid implements a Mamdani fuzzy-PID hybrid controller for
// aquaculture feeding automation. It combines fuzzification of 6 sensor
// inputs, a 30-rule Mamdani inference engine, center-of-gravity
// defuzzification, incremental PID smoothing, and hardware safety interlocks
// to produce a PWM duty cycle [0, 100] for feeding motor speed control.
//
//   - Fuzzifier: triangular + trapezoidal membership functions, 5 levels
//     (VL/L/M/H/VH) over each input normalized to [0, 1].
//   - RuleBase: 30 Mamdani rules (AND=min, OR=max).
//   - Defuzzifier: center of gravity (COG) over the aggregated output MF.
//   - PID: incremental form Δu = Kp·e(k) + Ki·Σe + Kd·(e(k)-e(k-1)).
//   - Safety interlock: DO < 4.0 mg/L or temp > 38°C forces STOP (PWM=0).
package fuzzypid

import (
	"math"
)

// MembershipLevel represents the 5 linguistic levels used in fuzzification.
type MembershipLevel int

const (
	VL MembershipLevel = iota
	L
	M
	H
	VH
)

// OutputAction represents the 5 feeding output actions (consequents).
type OutputAction int

const (
	STOP     OutputAction = iota // PWM ≈ 0
	DECREASE                     // PWM ≈ 25
	HOLD                         // PWM ≈ 50
	INCREASE                     // PWM ≈ 75
	MAX                          // PWM ≈ 100
)

// Variable identifies the 6 normalized input variables [0, 1].
type Variable int

const (
	VarDensity         Variable = iota // fish count / area
	VarSize                            // avg body length (normalized)
	VarFeedingIntensity                // texture + behavior score
	VarDO                              // dissolved oxygen normalized
	VarTemp                            // water temperature normalized
	VarNH3                             // ammonia concentration normalized
)

// Input holds raw sensor readings before normalization.
// Density, Size, and FeedingIntensity are expected in [0, 1].
// DO is in mg/L [0, 20], Temp in °C [0, 50], NH3 in mg/L [0, 10].
type Input struct {
	Density          float64
	Size             float64
	FeedingIntensity float64
	DO               float64
	Temp             float64
	NH3              float64
}

// NormalizedInput holds all 6 variables clamped to [0, 1].
type NormalizedInput struct {
	Density          float64
	Size             float64
	FeedingIntensity float64
	DO               float64
	Temp             float64
	NH3              float64
}

// Normalize scales raw Input values into [0, 1] and clamps extremes.
func (in *Input) Normalize() NormalizedInput {
	return NormalizedInput{
		Density:          clamp01(in.Density),
		Size:             clamp01(in.Size),
		FeedingIntensity: clamp01(in.FeedingIntensity),
		DO:               clamp01(in.DO / 20.0),
		Temp:             clamp01(in.Temp / 50.0),
		NH3:              clamp01(in.NH3 / 10.0),
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// MembershipMatrix stores the 5-level membership values for all 6 inputs.
type MembershipMatrix struct {
	Density          [5]float64
	Size             [5]float64
	FeedingIntensity [5]float64
	DO               [5]float64
	Temp             [5]float64
	NH3              [5]float64
}

// Get retrieves the membership value for a given variable-level pair.
func (m *MembershipMatrix) Get(v Variable, lvl MembershipLevel) float64 {
	switch v {
	case VarDensity:
		return m.Density[lvl]
	case VarSize:
		return m.Size[lvl]
	case VarFeedingIntensity:
		return m.FeedingIntensity[lvl]
	case VarDO:
		return m.DO[lvl]
	case VarTemp:
		return m.Temp[lvl]
	case VarNH3:
		return m.NH3[lvl]
	}
	return 0
}

// Fuzzifier computes fuzzy membership values using triangular and
// trapezoidal membership functions.
type Fuzzifier struct{}

// Fuzzify converts a normalized input into a membership matrix.
func (f *Fuzzifier) Fuzzify(n NormalizedInput) *MembershipMatrix {
	return &MembershipMatrix{
		Density:          f.fuzzifyVar(n.Density),
		Size:             f.fuzzifyVar(n.Size),
		FeedingIntensity: f.fuzzifyVar(n.FeedingIntensity),
		DO:               f.fuzzifyVar(n.DO),
		Temp:             f.fuzzifyVar(n.Temp),
		NH3:              f.fuzzifyVar(n.NH3),
	}
}

// fuzzifyVar computes 5-level membership for a single [0, 1] variable.
// VL: left trapezoid  (μ=1 at 0, ramp 0.1→0.3)
// L:  triangle  (0.1, 0.25, 0.5)
// M:  triangle  (0.25, 0.5, 0.75)
// H:  triangle  (0.5, 0.75, 0.9)
// VH: right trapezoid (ramp 0.7→0.9, μ=1 at 1)
func (f *Fuzzifier) fuzzifyVar(x float64) [5]float64 {
	return [5]float64{
		trapezoidLeft(x, 0.1, 0.3),
		triangleMF(x, 0.1, 0.25, 0.5),
		triangleMF(x, 0.25, 0.5, 0.75),
		triangleMF(x, 0.5, 0.75, 0.9),
		trapezoidRight(x, 0.7, 0.9),
	}
}

// triangleMF returns triangular membership: 0 outside [a, c], peak 1 at b.
// Guards against division by zero when a=b or b=c.
func triangleMF(x, a, b, c float64) float64 {
	if x <= a || x >= c {
		return 0
	}
	if x <= b {
		if b-a == 0 {
			return 0
		}
		return (x - a) / (b - a)
	}
	if c-b == 0 {
		return 0
	}
	return (c - x) / (c - b)
}

// trapezoidLeft returns left-shoulder membership: μ=1 for x ≤ b,
// linear ramp to 0 at x = c.
func trapezoidLeft(x, b, c float64) float64 {
	if x <= b {
		return 1
	}
	if x >= c {
		return 0
	}
	if c-b == 0 {
		return 0
	}
	return (c - x) / (c - b)
}

// trapezoidRight returns right-shoulder membership: μ=0 for x ≤ a,
// linear ramp to 1 at x = b.
func trapezoidRight(x, a, b float64) float64 {
	if x <= a {
		return 0
	}
	if x >= b {
		return 1
	}
	if b-a == 0 {
		return 0
	}
	return (x - a) / (b - a)
}

// Defuzzifier performs center-of-gravity (COG) defuzzification over the
// aggregated output membership functions discretized at the given resolution.
type Defuzzifier struct {
	resolution int // number of sample points in [0, 100]
}

// NewDefuzzifier creates a Defuzzifier with the specified resolution.
// resolution must be ≥ 2; 101 (1% step) is a good default.
func NewDefuzzifier(resolution int) *Defuzzifier {
	if resolution < 2 {
		resolution = 101
	}
	return &Defuzzifier{resolution: resolution}
}

// Defuzzify computes the COG from aggregated rule outputs.
// aggregated maps each OutputAction to its firing strength (after max-union).
func (d *Defuzzifier) Defuzzify(aggregated map[OutputAction]float64) float64 {
	sumNum := 0.0
	sumDen := 0.0

	n := d.resolution
	step := 100.0 / float64(n-1)

	for i := 0; i < n; i++ {
		y := float64(i) * step

		// Aggregate all output MFs clipped at their firing strength via max.
		mu := 0.0
		for action, fire := range aggregated {
			if mf, ok := outputMFs[action]; ok {
				clipped := math.Min(fire, mf(y))
				if clipped > mu {
					mu = clipped
				}
			}
		}

		sumNum += mu * y
		sumDen += mu
	}

	if sumDen == 0 {
		return 0
	}
	return sumNum / sumDen
}

// outputMFs defines the membership functions for each output action
// over the PWM domain [0, 100].
var outputMFs = map[OutputAction]func(float64) float64{
	STOP:     func(y float64) float64 { return trapezoidLeft(y, 10, 30) },
	DECREASE: func(y float64) float64 { return triangleMF(y, 10, 25, 40) },
	HOLD:     func(y float64) float64 { return triangleMF(y, 35, 50, 65) },
	INCREASE: func(y float64) float64 { return triangleMF(y, 60, 75, 90) },
	MAX:      func(y float64) float64 { return trapezoidRight(y, 70, 90) },
}

// PID implements an incremental PID controller.
//
// Formula: Δu = Kp·e(k) + Ki·Σe + Kd·(e(k) - e(k-1))
// Output: position = clamp(prev_position + Δu, outMin, outMax).
type PID struct {
	Kp, Ki, Kd float64
	output     float64
	prevError  float64
	integral   float64
	outputMin  float64
	outputMax  float64
}

// NewPID creates a PID with the given gains and output range.
func NewPID(kp, ki, kd, outMin, outMax float64) *PID {
	return &PID{
		Kp:        kp,
		Ki:        ki,
		Kd:        kd,
		outputMin: outMin,
		outputMax: outMax,
	}
}

// Step runs one control iteration.
// setpoint is the target value; processVar is the current measured value.
func (p *PID) Step(setpoint, processVar float64) float64 {
	e := setpoint - processVar
	p.integral += e
	deriv := e - p.prevError
	p.prevError = e

	du := p.Kp*e + p.Ki*p.integral + p.Kd*deriv

	p.output += du
	if p.output < p.outputMin {
		p.output = p.outputMin
	}
	if p.output > p.outputMax {
		p.output = p.outputMax
	}
	return p.output
}

// SafetyOverride checks hardware safety conditions that take priority over
// any fuzzy controller output. These are hardware-level interlocks:
//   - DO < DOThreshold → force STOP (PWM = 0).
//   - Temp > TempThreshold → force STOP (PWM = 0).
type SafetyOverride struct {
	DOThreshold   float64
	TempThreshold float64
}

// IsTriggered reports whether a safety condition is active and the forced PWM.
func (s *SafetyOverride) IsTriggered(in *Input) (bool, float64) {
	if in.DO < s.DOThreshold {
		return true, 0
	}
	if in.Temp > s.TempThreshold {
		return true, 0
	}
	return false, 0
}

// SafetyOverrideInterface defines the contract for safety interlock checks.
type SafetyOverrideInterface interface {
	IsTriggered(in *Input) (bool, float64)
}

// Config holds tuneable parameters for FuzzyPIDController.
type Config struct {
	Kp, Ki, Kd   float64
	DOThreshold   float64
	TempThreshold float64
	PIDOutMin     float64
	PIDOutMax     float64
}

// DefaultConfig returns sensible default gains and thresholds.
func DefaultConfig() Config {
	return Config{
		Kp:           0.8,
		Ki:           0.05,
		Kd:           0.1,
		DOThreshold:   4.0,
		TempThreshold: 38.0,
		PIDOutMin:     0,
		PIDOutMax:     100,
	}
}

// FuzzyPIDController orchestrates the full control pipeline:
//
//	sensor inputs → normalize → fuzzify → infer → defuzzify → PID → PWM.
type FuzzyPIDController struct {
	fuzzifier   *Fuzzifier
	ruleBase    *RuleBase
	defuzzifier *Defuzzifier
	pid         *PID
	safety      SafetyOverrideInterface
}

// New creates a FuzzyPIDController with the given config and rule base.
func New(cfg Config, rb *RuleBase) *FuzzyPIDController {
	return &FuzzyPIDController{
		fuzzifier:   &Fuzzifier{},
		ruleBase:    rb,
		defuzzifier: NewDefuzzifier(101),
		pid:         NewPID(cfg.Kp, cfg.Ki, cfg.Kd, cfg.PIDOutMin, cfg.PIDOutMax),
		safety: &SafetyOverride{
			DOThreshold:   cfg.DOThreshold,
			TempThreshold: cfg.TempThreshold,
		},
	}
}

// NewDefault creates a controller with DefaultConfig and DefaultRuleBase.
func NewDefault() *FuzzyPIDController {
	return New(DefaultConfig(), DefaultRuleBase())
}

// Step processes one control cycle.
// Returns the PWM duty cycle [0, 100] and whether a safety override stopped
// feeding (fuzzy and PID were bypassed).
func (c *FuzzyPIDController) Step(in *Input) (float64, bool) {
	// 1. Safety interlock — highest priority, cannot be overridden.
	if triggered, action := c.safety.IsTriggered(in); triggered {
		return action, true
	}

	// 2. Normalize raw inputs to [0, 1].
	ni := in.Normalize()

	// 3. Fuzzification.
	mm := c.fuzzifier.Fuzzify(ni)

	// 4. Mamdani min-max inference.
	aggregated := c.ruleBase.Infer(mm)

	// 5. COG defuzzification → target feeding rate setpoint.
	setpoint := c.defuzzifier.Defuzzify(aggregated)

	// 6. PID smoothing toward the setpoint (self-feedback prevents windup).
	pwm := c.pid.Step(setpoint, c.pid.output)

	return pwm, false
}

// Reset clears the PID internal state (useful between test cases).
func (c *FuzzyPIDController) Reset() {
	c.pid.integral = 0
	c.pid.prevError = 0
	c.pid.output = 0
}

// Output returns the current PID position (may be stale after multiple steps).
func (c *FuzzyPIDController) Output() float64 {
	return c.pid.output
}
