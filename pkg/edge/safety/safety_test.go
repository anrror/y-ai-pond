package safety

import (
	"testing"
	"time"
)

// ============================================================================
// Test helpers
// ============================================================================

func normalReadings() SensorReadings {
	return SensorReadings{DO: 6.5, Temp: 25.0}
}

func normalStates() ActuatorStates {
	return ActuatorStates{}
}

func normalEmergency() EmergencyInput {
	return EmergencyInput{}
}

// ============================================================================
// TestDOBelowThresholdForcesAerator
// ============================================================================

func TestDOBelowThresholdForcesAerator(t *testing.T) {
	e := NewEvaluator(DefaultConfig())

	t.Run("belowThreshold", func(t *testing.T) {
		dec := e.Evaluate(SensorReadings{DO: 3.5, Temp: 25.0}, normalStates(), normalEmergency())
		if !dec.AeratorForced {
			t.Error("DO=3.5: aerator should be forced on")
		}
		if dec.FeedingForcedStop {
			t.Error("DO=3.5: feeding must not be force-stopped")
		}
		if dec.AllActuatorsOff {
			t.Error("DO=3.5: actuators must not all be off")
		}
		if len(dec.Reasons) == 0 {
			t.Error("DO=3.5: interlock must not be silent")
		}
	})

	t.Run("boundaryNotForced", func(t *testing.T) {
		dec := e.Evaluate(SensorReadings{DO: 4.0, Temp: 25.0}, normalStates(), normalEmergency())
		if dec.AeratorForced {
			t.Error("DO=4.0 exactly: should NOT force aerator (< 4.0 only)")
		}
	})

	t.Run("normalNotForced", func(t *testing.T) {
		dec := e.Evaluate(normalReadings(), normalStates(), normalEmergency())
		if dec.AeratorForced {
			t.Error("normal DO: aerator should not be forced")
		}
		if dec.FeedingForcedStop || dec.AllActuatorsOff {
			t.Error("normal conditions: no interlock should trigger")
		}
		if !dec.DosingAllowed {
			t.Error("normal conditions: dosing should be allowed")
		}
	})
}

// ============================================================================
// TestMotorOvercurrentStops
// ============================================================================

func TestMotorOvercurrentStops(t *testing.T) {
	e := NewEvaluator(DefaultConfig())

	dec := e.Evaluate(normalReadings(), ActuatorStates{FeedingMotorCurrent: 6.0}, normalEmergency())
	if !dec.FeedingForcedStop {
		t.Error("motor current 6.0 A: feeding should be force-stopped")
	}
	if dec.AeratorForced || dec.AllActuatorsOff {
		t.Error("overcurrent: only feeding stop should trigger")
	}

	ok := e.Evaluate(normalReadings(), ActuatorStates{FeedingMotorCurrent: 5.0}, normalEmergency())
	if ok.FeedingForcedStop {
		t.Error("motor current 5.0 A exactly: should NOT force-stop (> threshold only)")
	}

	idle := e.Evaluate(normalReadings(), ActuatorStates{FeedingMotorCurrent: 1.2}, normalEmergency())
	if idle.FeedingForcedStop {
		t.Error("motor current 1.2 A: no overcurrent interlock")
	}
}

// ============================================================================
// TestEmergencyStopKillsAll
// ============================================================================

func TestEmergencyStopKillsAll(t *testing.T) {
	e := NewEvaluator(DefaultConfig())

	dec := e.Evaluate(
		SensorReadings{DO: 3.0, Temp: 39.5},
		ActuatorStates{FeedingMotorCurrent: 8.0, ProposedDose: 10.0},
		EmergencyInput{EStopActive: true},
	)
	if !dec.AllActuatorsOff {
		t.Error("emergency stop: all actuators must be off")
	}
	if dec.DosingAllowed {
		t.Error("emergency stop: dosing must be blocked")
	}
	if dec.AeratorForced {
		t.Error("emergency stop has absolute priority: aerator forced-on must be suppressed")
	}
	if dec.FeedingForcedStop {
		t.Error("emergency stop has absolute priority: feeding stop flag is redundant")
	}
	if len(dec.Reasons) == 0 {
		t.Error("emergency stop: interlock must not be silent")
	}
}

// ============================================================================
// TestTempAbove38PausesFeeding
// ============================================================================

func TestTempAbove38PausesFeeding(t *testing.T) {
	e := NewEvaluator(DefaultConfig())

	dec := e.Evaluate(SensorReadings{DO: 6.0, Temp: 38.5}, normalStates(), normalEmergency())
	if !dec.FeedingForcedStop {
		t.Error("temp=38.5°C: feeding should be paused")
	}
	if dec.AeratorForced || dec.AllActuatorsOff {
		t.Error("high temp: only feeding pause should trigger")
	}

	boundary := e.Evaluate(SensorReadings{DO: 6.0, Temp: 38.0}, normalStates(), normalEmergency())
	if boundary.FeedingForcedStop {
		t.Error("temp=38.0°C exactly: should NOT pause feeding (> 38.0 only)")
	}
}

// ============================================================================
// TestDosingLimits — 15 mL single dose / 600 s interval / 40 mL per hour
// ============================================================================

func TestDosingLimits(t *testing.T) {
	e := NewEvaluator(DefaultConfig())
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }

	t.Run("singleDoseMax15mL", func(t *testing.T) {
		ok := e.Evaluate(normalReadings(), ActuatorStates{ProposedDose: 15.0}, normalEmergency())
		if !ok.DosingAllowed {
			t.Error("dose of exactly 15 mL should be allowed")
		}
		over := e.Evaluate(normalReadings(), ActuatorStates{ProposedDose: 15.5}, normalEmergency())
		if over.DosingAllowed {
			t.Error("dose of 15.5 mL should be blocked")
		}
	})

	t.Run("minInterval600s", func(t *testing.T) {
		tooSoon := e.Evaluate(normalReadings(), ActuatorStates{
			ProposedDose: 10.0,
			LastDoseTime: now.Add(-300 * time.Second),
		}, normalEmergency())
		if tooSoon.DosingAllowed {
			t.Error("dose 300 s after last dose should be blocked")
		}

		ok := e.Evaluate(normalReadings(), ActuatorStates{
			ProposedDose: 10.0,
			LastDoseTime: now.Add(-600 * time.Second),
		}, normalEmergency())
		if !ok.DosingAllowed {
			t.Error("dose exactly 600 s after last dose should be allowed")
		}
	})

	t.Run("hourlyMax40mL", func(t *testing.T) {
		ok := e.Evaluate(normalReadings(), ActuatorStates{
			ProposedDose:    5.0,
			HourlyDoseTotal: 35.0,
		}, normalEmergency())
		if !ok.DosingAllowed {
			t.Error("hourly total 35 + 5 = 40 mL should be allowed")
		}

		over := e.Evaluate(normalReadings(), ActuatorStates{
			ProposedDose:    10.0,
			HourlyDoseTotal: 35.0,
		}, normalEmergency())
		if over.DosingAllowed {
			t.Error("hourly total 35 + 10 = 45 mL should be blocked")
		}
	})
}

// ============================================================================
// TestInterlockPriority — safety outranks fuzzy controller output
// ============================================================================

func TestInterlockPriority(t *testing.T) {
	e := NewEvaluator(DefaultConfig())

	dec := e.Evaluate(SensorReadings{DO: 3.2, Temp: 25.0}, normalStates(), normalEmergency())
	if !dec.AeratorForced {
		t.Error("low DO must force the aerator on regardless of fuzzy output")
	}
	if !dec.DosingAllowed {
		t.Error("no dosing request: dosing interlock must stay permissive")
	}
}
