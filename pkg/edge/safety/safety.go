// Package safety implements hardware-level safety interlocks for the
// edge controller. Interlock rules always override fuzzy controller
// output and cannot be overridden by cloud commands:
//
//   - DO below the threshold forces the aerator on.
//   - Feeding motor overcurrent force-stops feeding.
//   - Emergency-stop GPIO low powers all actuators off.
//   - Water temperature above the threshold pauses feeding.
//   - Dosing is limited per dose, per interval, and per hour.
//
// The evaluator is pure and network-independent: it never depends on
// MQTT or cloud connectivity, so interlocks remain active offline.
package safety

import (
	"fmt"
	"time"
)

const (
	// DOThresholdDefault is the dissolved oxygen level below which the
	// aerator is forced on (mg/L).
	DOThresholdDefault = 4.0
	// MotorCurrentMaxDefault is the feeding motor current above which
	// feeding is force-stopped (A).
	MotorCurrentMaxDefault = 5.0
	// TempThresholdDefault is the water temperature above which feeding
	// is paused (°C).
	TempThresholdDefault = 38.0
	// MaxSingleDoseDefault is the maximum allowed single dose (mL).
	MaxSingleDoseDefault = 15.0
	// MinDoseIntervalDefault is the minimum time between two doses.
	MinDoseIntervalDefault = 600 * time.Second
	// MaxHourlyDoseDefault is the maximum total dose per hour (mL).
	MaxHourlyDoseDefault = 40.0
)

// SensorReadings carries the water-quality readings relevant to safety.
type SensorReadings struct {
	DO   float64 // mg/L
	Temp float64 // °C
}

// ActuatorStates carries the current state of actuators relevant to the
// safety evaluation. ProposedDose is the volume of the pending dosing
// request; LastDoseTime and HourlyDoseTotal describe recent dosing
// history used to enforce the dosing interlocks.
type ActuatorStates struct {
	FeedingMotorCurrent float64 // A
	FeedingRunning      bool
	ProposedDose        float64 // mL
	LastDoseTime        time.Time
	HourlyDoseTotal     float64 // mL dosed in the last 60 minutes
}

// EmergencyInput carries the emergency-stop signal state.
type EmergencyInput struct {
	// EStopActive is true when the emergency-stop GPIO is pulled low.
	EStopActive bool
}

// SafetyDecision is the outcome of a safety evaluation. It describes the
// forced actuator actions that override any fuzzy or cloud command.
type SafetyDecision struct {
	AeratorForced     bool
	FeedingForcedStop bool
	AllActuatorsOff   bool
	DosingAllowed     bool
	Reasons           []string
}

// Config holds the tunable thresholds for the safety interlocks.
type Config struct {
	DOThreshold     float64
	MotorCurrentMax float64
	TempThreshold   float64
	MaxSingleDose   float64
	MinDoseInterval time.Duration
	MaxHourlyDose   float64
}

// DefaultConfig returns the hardware-safe default thresholds.
func DefaultConfig() Config {
	return Config{
		DOThreshold:     DOThresholdDefault,
		MotorCurrentMax: MotorCurrentMaxDefault,
		TempThreshold:   TempThresholdDefault,
		MaxSingleDose:   MaxSingleDoseDefault,
		MinDoseInterval: MinDoseIntervalDefault,
		MaxHourlyDose:   MaxHourlyDoseDefault,
	}
}

// SafetyEvaluator applies the hardware interlock rules and produces a
// SafetyDecision whose priority is higher than the fuzzy controller.
type SafetyEvaluator struct {
	DOThreshold     float64
	MotorCurrentMax float64
	TempThreshold   float64
	MaxSingleDose   float64
	MinDoseInterval time.Duration
	MaxHourlyDose   float64
	now             func() time.Time
}

// NewEvaluator creates a SafetyEvaluator from cfg, filling any zero
// field with its DefaultConfig value.
func NewEvaluator(cfg Config) *SafetyEvaluator {
	def := DefaultConfig()
	if cfg.DOThreshold == 0 {
		cfg.DOThreshold = def.DOThreshold
	}
	if cfg.MotorCurrentMax == 0 {
		cfg.MotorCurrentMax = def.MotorCurrentMax
	}
	if cfg.TempThreshold == 0 {
		cfg.TempThreshold = def.TempThreshold
	}
	if cfg.MaxSingleDose == 0 {
		cfg.MaxSingleDose = def.MaxSingleDose
	}
	if cfg.MinDoseInterval == 0 {
		cfg.MinDoseInterval = def.MinDoseInterval
	}
	if cfg.MaxHourlyDose == 0 {
		cfg.MaxHourlyDose = def.MaxHourlyDose
	}
	return &SafetyEvaluator{
		DOThreshold:     cfg.DOThreshold,
		MotorCurrentMax: cfg.MotorCurrentMax,
		TempThreshold:   cfg.TempThreshold,
		MaxSingleDose:   cfg.MaxSingleDose,
		MinDoseInterval: cfg.MinDoseInterval,
		MaxHourlyDose:   cfg.MaxHourlyDose,
		now:             time.Now,
	}
}

// Evaluate computes the interlock decision for the given sensor
// readings, actuator states, and emergency input. The emergency stop
// has absolute priority: when active, every actuator is powered off and
// all other forced states are suppressed.
func (e *SafetyEvaluator) Evaluate(reads SensorReadings, states ActuatorStates, emerg EmergencyInput) SafetyDecision {
	dec := SafetyDecision{DosingAllowed: true}

	if emerg.EStopActive {
		dec.AllActuatorsOff = true
		dec.DosingAllowed = false
		dec.Reasons = append(dec.Reasons, "emergency stop active: all actuators off")
		return dec
	}

	if reads.DO < e.DOThreshold {
		dec.AeratorForced = true
		dec.Reasons = append(dec.Reasons,
			fmt.Sprintf("DO %.2f mg/L below %.1f: aerator forced on", reads.DO, e.DOThreshold))
	}

	if states.FeedingMotorCurrent > e.MotorCurrentMax {
		dec.FeedingForcedStop = true
		dec.Reasons = append(dec.Reasons,
			fmt.Sprintf("feeding motor current %.2f A above %.1f A: feeding force-stopped",
				states.FeedingMotorCurrent, e.MotorCurrentMax))
	}

	if reads.Temp > e.TempThreshold {
		dec.FeedingForcedStop = true
		dec.Reasons = append(dec.Reasons,
			fmt.Sprintf("water temperature %.2f °C above %.1f °C: feeding paused",
				reads.Temp, e.TempThreshold))
	}

	if reason := e.checkDosing(states); reason != "" {
		dec.DosingAllowed = false
		dec.Reasons = append(dec.Reasons, reason)
	}

	return dec
}

// checkDosing returns a reason string when the pending dosing request
// violates a dosing interlock, or "" when dosing is allowed.
func (e *SafetyEvaluator) checkDosing(states ActuatorStates) string {
	if states.ProposedDose <= 0 {
		return ""
	}
	if states.ProposedDose > e.MaxSingleDose {
		return fmt.Sprintf("single dose %.1f mL exceeds limit %.1f mL: dosing blocked",
			states.ProposedDose, e.MaxSingleDose)
	}
	if !states.LastDoseTime.IsZero() {
		elapsed := e.now().Sub(states.LastDoseTime)
		if elapsed < e.MinDoseInterval {
			return fmt.Sprintf("dose interval %.0fs below minimum %.0fs: dosing blocked",
				elapsed.Seconds(), e.MinDoseInterval.Seconds())
		}
	}
	if states.HourlyDoseTotal+states.ProposedDose > e.MaxHourlyDose {
		return fmt.Sprintf("hourly dose total %.1f mL would exceed limit %.1f mL: dosing blocked",
			states.HourlyDoseTotal+states.ProposedDose, e.MaxHourlyDose)
	}
	return ""
}
