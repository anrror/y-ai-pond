package controller

import (
	"testing"

	"github.com/anrror/y-ai-pond/pkg/edge/hal"
)

// ============================================================================
// TestFeedingDriverSetSpeed
// ============================================================================

func TestFeedingDriverSetSpeed(t *testing.T) {
	pwm := hal.NewMockPWMBus()
	dir := hal.NewMockGPIOBus()
	d := NewFeedingDriver(pwm, dir)

	if err := d.SetSpeed(50); err != nil {
		t.Fatalf("SetSpeed(50): %v", err)
	}
	if pwm.DutyCycle() != 50 {
		t.Errorf("DutyCycle = %.2f, want 50", pwm.DutyCycle())
	}
	if d.Speed() != 50 {
		t.Errorf("Speed() = %.2f, want 50", d.Speed())
	}

	// Test clamping > 100.
	if err := d.SetSpeed(150); err != nil {
		t.Fatalf("SetSpeed(150): %v", err)
	}
	if pwm.DutyCycle() != 100 {
		t.Errorf("DutyCycle after 150 = %.2f, want 100", pwm.DutyCycle())
	}

	// Test clamping < 0.
	if err := d.SetSpeed(-10); err != nil {
		t.Fatalf("SetSpeed(-10): %v", err)
	}
	if pwm.DutyCycle() != 0 {
		t.Errorf("DutyCycle after -10 = %.2f, want 0", pwm.DutyCycle())
	}
}

// ============================================================================
// TestFeedingDriverDirection
// ============================================================================

func TestFeedingDriverDirection(t *testing.T) {
	pwm := hal.NewMockPWMBus()
	dir := hal.NewMockGPIOBus()
	d := NewFeedingDriver(pwm, dir)

	// Forward.
	if err := d.SetDirection(true); err != nil {
		t.Fatalf("SetDirection(true): %v", err)
	}
	if !dir.State() {
		t.Error("dir.State() = false, want true after SetDirection(true)")
	}
	if !d.Forward() {
		t.Error("Forward() = false, want true")
	}

	// Reverse.
	if err := d.SetDirection(false); err != nil {
		t.Fatalf("SetDirection(false): %v", err)
	}
	if dir.State() {
		t.Error("dir.State() = true, want false after SetDirection(false)")
	}
	if d.Forward() {
		t.Error("Forward() = true, want false")
	}
}

// ============================================================================
// TestFeedingDriverStop
// ============================================================================

func TestFeedingDriverStop(t *testing.T) {
	pwm := hal.NewMockPWMBus()
	dir := hal.NewMockGPIOBus()
	d := NewFeedingDriver(pwm, dir)

	// Set speed first.
	if err := d.SetSpeed(50); err != nil {
		t.Fatalf("SetSpeed(50): %v", err)
	}
	if d.Speed() != 50 {
		t.Fatalf("Speed() = %.2f, want 50 before Stop", d.Speed())
	}

	// Stop.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	if pwm.DutyCycle() != 0 {
		t.Errorf("DutyCycle after Stop = %.2f, want 0", pwm.DutyCycle())
	}
	if d.Speed() != 0 {
		t.Errorf("Speed() after Stop = %.2f, want 0", d.Speed())
	}
}
