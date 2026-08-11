package hal

import (
	"fmt"
	"math"
)

// ============================================================================
// Low-level bus interfaces
// ============================================================================

// PWMBus abstracts a PWM (Pulse Width Modulation) output line.
// In production this maps to a hardware PWM pin (via periph.io);
// in tests a mock records the last duty cycle.
type PWMBus interface {
	// SetDutyCycle sets the PWM duty cycle as a percentage [0, 100].
	// 0 = fully off, 100 = fully on.
	SetDutyCycle(pct float64) error
}

// GPIOBus abstracts a single digital GPIO output line.
// In production this maps to a periph.io GPIO pin; in tests a mock
// records the pin state.
type GPIOBus interface {
	// High sets the pin to logic HIGH (on).
	High() error
	// Low sets the pin to logic LOW (off).
	Low() error
	// Read returns true if the pin is HIGH.
	Read() (bool, error)
}

// ============================================================================
// Feeding Motor — PWM-controlled
// ============================================================================

// FeedingMotor implements Actuator for a PWM-controlled DC feeding motor
// (typically driven by an IRLZ44N MOSFET + H-bridge).
//
// The PWM duty cycle maps linearly to motor speed: 0% = stopped,
// 100% = full speed.
type FeedingMotor struct {
	name   string
	pwm    PWMBus
	health Health
	speed  float64 // last set speed [0, 100]
	on     bool
}

// NewFeedingMotor creates a PWM-based feeding motor actuator.
func NewFeedingMotor(name string, pwm PWMBus) *FeedingMotor {
	return &FeedingMotor{
		name:   name,
		pwm:    pwm,
		health: StatusOK,
		speed:  0,
		on:     false,
	}
}

// On enables the motor at the last set speed (default 100% if never set).
func (m *FeedingMotor) On() error {
	if m.speed == 0 {
		m.speed = 100
	}
	if err := m.pwm.SetDutyCycle(m.speed); err != nil {
		m.health = StatusError
		return fmt.Errorf("actuator %s: PWM on error: %w", m.name, err)
	}
	m.on = true
	m.health = StatusOK
	return nil
}

// Off stops the motor (sets PWM to 0%).
func (m *FeedingMotor) Off() error {
	if err := m.pwm.SetDutyCycle(0); err != nil {
		m.health = StatusError
		return fmt.Errorf("actuator %s: PWM off error: %w", m.name, err)
	}
	m.on = false
	m.speed = 0
	m.health = StatusOK
	return nil
}

// SetSpeed sets the motor speed as a percentage [0, 100].
// 0 stops the motor, 100 is full speed. The speed is capped to the
// valid range. If the motor is currently on, the PWM duty cycle is
// updated immediately.
func (m *FeedingMotor) SetSpeed(pct float64) error {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	pct = math.Round(pct*10) / 10 // round to 1 decimal
	m.speed = pct
	if m.on {
		if err := m.pwm.SetDutyCycle(pct); err != nil {
			m.health = StatusError
			return fmt.Errorf("actuator %s: PWM set speed error: %w", m.name, err)
		}
	}
	m.health = StatusOK
	return nil
}

// Status returns the actuator health.
func (m *FeedingMotor) Status() Health {
	return m.health
}

// Speed returns the current speed setting [0, 100].
func (m *FeedingMotor) Speed() float64 {
	return m.speed
}

// IsOn returns whether the motor is currently enabled.
func (m *FeedingMotor) IsOn() bool {
	return m.on
}

// ============================================================================
// Mock PWM Bus
// ============================================================================

// MockPWMBus records the last PWM duty cycle for test assertions.
type MockPWMBus struct {
	dutyCycle float64
	err       error
}

// NewMockPWMBus creates a MockPWMBus.
func NewMockPWMBus() *MockPWMBus {
	return &MockPWMBus{}
}

// SetError forces subsequent SetDutyCycle calls to return the given error.
func (m *MockPWMBus) SetError(err error) {
	m.err = err
}

// SetDutyCycle records the duty cycle.
func (m *MockPWMBus) SetDutyCycle(pct float64) error {
	if m.err != nil {
		return m.err
	}
	m.dutyCycle = pct
	return nil
}

// DutyCycle returns the last recorded duty cycle.
func (m *MockPWMBus) DutyCycle() float64 {
	return m.dutyCycle
}

// ============================================================================
// Aerator — GPIO-controlled
// ============================================================================

// Aerator implements Actuator for a GPIO-controlled aeration pump
// (IRLZ44N MOSFET driving a DC air pump).
//
// The aerator is binary (on/off). SetSpeed(pct) enables the aerator
// when pct > 0 and disables it when pct == 0.
type Aerator struct {
	name   string
	gpio   GPIOBus
	health Health
	on     bool
}

// NewAerator creates a GPIO-based aerator actuator.
func NewAerator(name string, gpio GPIOBus) *Aerator {
	return &Aerator{
		name:   name,
		gpio:   gpio,
		health: StatusOK,
		on:     false,
	}
}

// On turns the aerator on (GPIO HIGH).
func (a *Aerator) On() error {
	if err := a.gpio.High(); err != nil {
		a.health = StatusError
		return fmt.Errorf("actuator %s: GPIO high error: %w", a.name, err)
	}
	a.on = true
	a.health = StatusOK
	return nil
}

// Off turns the aerator off (GPIO LOW).
func (a *Aerator) Off() error {
	if err := a.gpio.Low(); err != nil {
		a.health = StatusError
		return fmt.Errorf("actuator %s: GPIO low error: %w", a.name, err)
	}
	a.on = false
	a.health = StatusOK
	return nil
}

// SetSpeed enables the aerator when pct > 0, disables when pct == 0.
func (a *Aerator) SetSpeed(pct float64) error {
	if pct > 0 {
		return a.On()
	}
	return a.Off()
}

// Status returns the actuator health.
func (a *Aerator) Status() Health {
	return a.health
}

// IsOn returns whether the aerator is currently enabled.
func (a *Aerator) IsOn() bool {
	return a.on
}

// ============================================================================
// Circulation Pump — GPIO-controlled
// ============================================================================

// CirculationPump implements Actuator for a GPIO-controlled water
// circulation pump (IRLZ44N MOSFET).
//
// Operates identically to the Aerator: binary on/off GPIO.
type CirculationPump struct {
	name   string
	gpio   GPIOBus
	health Health
	on     bool
}

// NewCirculationPump creates a GPIO-based circulation pump actuator.
func NewCirculationPump(name string, gpio GPIOBus) *CirculationPump {
	return &CirculationPump{
		name:   name,
		gpio:   gpio,
		health: StatusOK,
		on:     false,
	}
}

// On turns the pump on (GPIO HIGH).
func (p *CirculationPump) On() error {
	if err := p.gpio.High(); err != nil {
		p.health = StatusError
		return fmt.Errorf("actuator %s: GPIO high error: %w", p.name, err)
	}
	p.on = true
	p.health = StatusOK
	return nil
}

// Off turns the pump off (GPIO LOW).
func (p *CirculationPump) Off() error {
	if err := p.gpio.Low(); err != nil {
		p.health = StatusError
		return fmt.Errorf("actuator %s: GPIO low error: %w", p.name, err)
	}
	p.on = false
	p.health = StatusOK
	return nil
}

// SetSpeed enables the pump when pct > 0, disables when pct == 0.
func (p *CirculationPump) SetSpeed(pct float64) error {
	if pct > 0 {
		return p.On()
	}
	return p.Off()
}

// Status returns the actuator health.
func (p *CirculationPump) Status() Health {
	return p.health
}

// IsOn returns whether the pump is currently enabled.
func (p *CirculationPump) IsOn() bool {
	return p.on
}

// ============================================================================
// Mock GPIO Bus
// ============================================================================

// MockGPIOBus records GPIO state for test assertions.
type MockGPIOBus struct {
	state bool
	err   error
}

// NewMockGPIOBus creates a MockGPIOBus (initially LOW).
func NewMockGPIOBus() *MockGPIOBus {
	return &MockGPIOBus{state: false}
}

// SetError forces subsequent operations to return the given error.
func (m *MockGPIOBus) SetError(err error) {
	m.err = err
}

// High sets the pin to HIGH.
func (m *MockGPIOBus) High() error {
	if m.err != nil {
		return m.err
	}
	m.state = true
	return nil
}

// Low sets the pin to LOW.
func (m *MockGPIOBus) Low() error {
	if m.err != nil {
		return m.err
	}
	m.state = false
	return nil
}

// Read returns the current pin state.
func (m *MockGPIOBus) Read() (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	return m.state, nil
}

// State returns the current pin state without error.
func (m *MockGPIOBus) State() bool {
	return m.state
}
