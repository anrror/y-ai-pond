package hal

import (
	"errors"
	"math"
	"testing"
)

// ============================================================================
// MockReader — injectable Reader for sensor tests
// ============================================================================

// mockReader implements Reader with a programmable value and error.
type mockReader struct {
	value float64
	err   error
}

func newMockReader(value float64) *mockReader {
	return &mockReader{value: value}
}

func (r *mockReader) SetValue(v float64) { r.value = v }
func (r *mockReader) SetError(err error) { r.err = err }
func (r *mockReader) Read() (float64, error) {
	if r.err != nil {
		return 0, r.err
	}
	return r.value, nil
}

// ============================================================================
// Health
// ============================================================================

func TestHealthString(t *testing.T) {
	tests := []struct {
		h    Health
		want string
	}{
		{StatusOK, "OK"},
		{StatusError, "ERROR"},
		{Health(99), "UNKNOWN(99)"},
	}
	for _, tt := range tests {
		got := tt.h.String()
		if got != tt.want {
			t.Errorf("Health(%d).String() = %q, want %q", tt.h, tt.want, got)
		}
	}
}

func TestCalibrationCoeffsIsZero(t *testing.T) {
	if !DefaultCalibration().IsZero() {
		t.Error("DefaultCalibration should be zero (identity)")
	}
	nonZero := CalibrationCoeffs{Slope: 2, Intercept: 0}
	if nonZero.IsZero() {
		t.Error("non-identity calibration should not be zero")
	}
}

// ============================================================================
// MockSensor — Read / Calibrate / Status
// ============================================================================

func TestMockSensorRead(t *testing.T) {
	reader := newMockReader(7.0)
	s := NewPHSensor("pH-probe-01", reader)

	val, err := s.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Default calibration is identity, so raw == engineering
	const epsilon = 0.01
	if math.Abs(val-7.0) > epsilon {
		t.Errorf("Read() = %f, want 7.0", val)
	}
}

func TestMockSensorRead_Calibrated(t *testing.T) {
	reader := newMockReader(7.0)
	s := NewPHSensor("pH-probe-01", reader)
	ph, ok := s.(*pHSensor)
	if !ok {
		t.Fatal("type assertion failed")
	}

	// Apply a calibration: slope=0.5, intercept=2 → 0.5*raw + 2
	ph.setCalibration(0.5, 2)
	val, err := s.Read()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := 0.5*7.0 + 2 // = 5.5
	const epsilon = 0.01
	if math.Abs(val-expected) > epsilon {
		t.Errorf("Read() = %f, want %f", val, expected)
	}
}

func TestMockSensorStatus_OK(t *testing.T) {
	reader := newMockReader(7.0)
	s := NewPHSensor("pH-probe-01", reader)

	if s.Status() != StatusOK {
		t.Error("new sensor should have StatusOK")
	}
	if _, err := s.Read(); err != nil {
		t.Fatal(err)
	}
	if s.Status() != StatusOK {
		t.Error("after successful read, status should be StatusOK")
	}
}

func TestMockSensorStatus_Error(t *testing.T) {
	reader := newMockReader(0)
	reader.SetError(errors.New("i2c timeout"))
	s := NewPHSensor("pH-broken", reader)

	_, err := s.Read()
	if err == nil {
		t.Fatal("expected error from broken sensor")
	}
	if s.Status() != StatusError {
		t.Errorf("broken sensor should be StatusError, got %v", s.Status())
	}
}

func TestMockSensorOutOfRange(t *testing.T) {
	reader := newMockReader(999) // way beyond valid pH range
	s := NewPHSensor("pH-outlier", reader)

	_, err := s.Read()
	if err == nil {
		t.Fatal("expected error for out-of-range raw value")
	}
	if s.Status() != StatusError {
		t.Errorf("out-of-range sensor should be StatusError, got %v", s.Status())
	}
}

// ============================================================================
// MockSensor — Calibrate & Calibration math
// ============================================================================

func TestSensorCalibration_3Point(t *testing.T) {
	reader := newMockReader(0)
	s := NewPHSensor("pH-calib", reader)
	ph, ok := s.(*pHSensor)
	if !ok {
		t.Fatal("type assertion failed")
	}

	// Simulate 3-point calibration:
	// Buffer 4.00 → raw=4.00 (ideal ADC), Buffer 7.00 → raw=7.00, Buffer 10.00 → raw=10.00
	reference := []float64{4.00, 7.00, 10.00}
	raw := []float64{4.00, 7.00, 10.00}

	err := ph.CalibrateWithPoints(reference, raw)
	if err != nil {
		t.Fatalf("CalibrateWithPoints failed: %v", err)
	}

	// With perfect raw values, slope should be 1.0 and intercept ~0
	const epsilon = 0.001
	if math.Abs(ph.calib.Slope-1.0) > epsilon {
		t.Errorf("expected slope ~1.0, got %f", ph.calib.Slope)
	}
	if math.Abs(ph.calib.Intercept) > epsilon {
		t.Errorf("expected intercept ~0, got %f", ph.calib.Intercept)
	}
}

func TestSensorCalibration_OffsetGain(t *testing.T) {
	reader := newMockReader(0)
	s := NewPHSensor("pH-offset", reader)
	ph, ok := s.(*pHSensor)
	if !ok {
		t.Fatal("type assertion failed")
	}

	// Simulate an ADC with gain error and offset:
	// Known pH:   4.00   7.00   10.00
	// ADC reads:  3.50   6.80   10.10  (slightly off)
	reference := []float64{4.00, 7.00, 10.00}
	raw := []float64{3.50, 6.80, 10.10}

	err := ph.CalibrateWithPoints(reference, raw)
	if err != nil {
		t.Fatalf("CalibrateWithPoints failed: %v", err)
	}

	// Calibration should correct the offset: slope should compensate for
	// the gain error, intercept for the offset. After calibration, pH=4
	// should map to ~4 from raw=3.5, etc.
	const epsilon = 0.05
	for i := range reference {
		calibrated := ph.calib.Slope*raw[i] + ph.calib.Intercept
		if math.Abs(calibrated-reference[i]) > epsilon {
			t.Errorf("After calibration: raw=%.2f → calibrated=%.3f, want %.2f",
				raw[i], calibrated, reference[i])
		}
	}
}

func TestSensorCalibration_MismatchLength(t *testing.T) {
	s := NewPHSensor("pH-mismatch", newMockReader(0))
	ph, ok := s.(*pHSensor)
	if !ok {
		t.Fatal("type assertion failed")
	}

	err := ph.CalibrateWithPoints([]float64{4.0, 7.0}, []float64{4.0})
	if err == nil {
		t.Fatal("expected error for mismatched reference/raw lengths")
	}
}

func TestSensorCalibration_TooFewPoints(t *testing.T) {
	s := NewPHSensor("pH-toofew", newMockReader(0))
	ph, ok := s.(*pHSensor)
	if !ok {
		t.Fatal("type assertion failed")
	}

	err := ph.CalibrateWithPoints([]float64{4.0}, []float64{4.0})
	if err == nil {
		t.Fatal("expected error for single-point calibration")
	}
}

// ============================================================================
// MockActuator — On / Off / SetSpeed
// ============================================================================

func TestMockActuatorOnOff(t *testing.T) {
	pwm := NewMockPWMBus()
	motor := NewFeedingMotor("feed-motor", pwm)

	// Initial state
	if motor.IsOn() {
		t.Error("motor should start off")
	}

	// On
	if err := motor.On(); err != nil {
		t.Fatalf("On failed: %v", err)
	}
	if !motor.IsOn() {
		t.Error("motor should be on after On()")
	}
	if motor.Status() != StatusOK {
		t.Errorf("status should be OK, got %v", motor.Status())
	}

	// Off
	if err := motor.Off(); err != nil {
		t.Fatalf("Off failed: %v", err)
	}
	if motor.IsOn() {
		t.Error("motor should be off after Off()")
	}
	if motor.Status() != StatusOK {
		t.Errorf("status should be OK, got %v", motor.Status())
	}
}

func TestMockActuatorSetSpeed(t *testing.T) {
	pwm := NewMockPWMBus()
	motor := NewFeedingMotor("feed-motor", pwm)

	// Set speed while off — should not update PWM yet
	if err := motor.SetSpeed(75.0); err != nil {
		t.Fatalf("SetSpeed failed: %v", err)
	}
	if motor.Speed() != 75.0 {
		t.Errorf("Speed() = %f, want 75.0", motor.Speed())
	}
	if motor.IsOn() {
		t.Error("motor should still be off after SetSpeed while off")
	}

	// On now — PWM should receive last speed
	if err := motor.On(); err != nil {
		t.Fatalf("On failed: %v", err)
	}
	if pwm.DutyCycle() != 75.0 {
		t.Errorf("PWM duty cycle = %f, want 75.0", pwm.DutyCycle())
	}

	// Set speed while on — PWM should update immediately
	if err := motor.SetSpeed(30.0); err != nil {
		t.Fatalf("SetSpeed failed: %v", err)
	}
	if pwm.DutyCycle() != 30.0 {
		t.Errorf("PWM duty cycle = %f, want 30.0", pwm.DutyCycle())
	}
}

func TestMockActuatorSetSpeed_Clamping(t *testing.T) {
	pwm := NewMockPWMBus()
	motor := NewFeedingMotor("feed-motor", pwm)

	// Below range
	if err := motor.SetSpeed(-10); err != nil {
		t.Fatal(err)
	}
	if motor.Speed() != 0 {
		t.Errorf("Speed() = %f, want 0 (clamped)", motor.Speed())
	}

	// Above range
	if err := motor.SetSpeed(150); err != nil {
		t.Fatal(err)
	}
	if motor.Speed() != 100 {
		t.Errorf("Speed() = %f, want 100 (clamped)", motor.Speed())
	}
}

// ============================================================================
// ActuatorPWM — linear 0-100% test
// ============================================================================

func TestActuatorPWM_LinearRange(t *testing.T) {
	pwm := NewMockPWMBus()
	motor := NewFeedingMotor("feed-pwm", pwm)

	if err := motor.On(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		input    float64
		expected float64
	}{
		{0, 0},
		{10, 10},
		{25, 25},
		{50, 50},
		{75, 75},
		{90, 90},
		{100, 100},
	}

	for _, tt := range tests {
		if err := motor.SetSpeed(tt.input); err != nil {
			t.Fatalf("SetSpeed(%f) failed: %v", tt.input, err)
		}
		if pwm.DutyCycle() != tt.expected {
			t.Errorf("SetSpeed(%f): PWM = %f, want %f",
				tt.input, pwm.DutyCycle(), tt.expected)
		}
	}

	// Verify Off resets everything
	if err := motor.Off(); err != nil {
		t.Fatal(err)
	}
	if pwm.DutyCycle() != 0 {
		t.Errorf("after Off: PWM = %f, want 0", pwm.DutyCycle())
	}
	if motor.Speed() != 0 {
		t.Errorf("after Off: Speed() = %f, want 0", motor.Speed())
	}
}

func TestActuatorPWM_FractionalSpeed(t *testing.T) {
	pwm := NewMockPWMBus()
	motor := NewFeedingMotor("feed-pwm", pwm)

	if err := motor.On(); err != nil {
		t.Fatal(err)
	}

	// 33.3% should round to 33.3 (1 decimal)
	if err := motor.SetSpeed(33.3456789); err != nil {
		t.Fatal(err)
	}
	const epsilon = 0.1
	if math.Abs(pwm.DutyCycle()-33.3) > epsilon {
		t.Errorf("SetSpeed(33.3456789): PWM = %f, want 33.3", pwm.DutyCycle())
	}
}

// ============================================================================
// PWM Bus Error
// ============================================================================

func TestActuatorPWM_ErrorStatus(t *testing.T) {
	pwm := NewMockPWMBus()
	pwm.SetError(errors.New("pwm hardware fault"))
	motor := NewFeedingMotor("feed-pwm", pwm)

	// SetSpeed while OFF should succeed (no PWM call yet)
	if err := motor.SetSpeed(50); err != nil {
		t.Fatalf("SetSpeed while off should not trigger PWM: %v", err)
	}
	if motor.Status() != StatusOK {
		t.Errorf("status should be OK while off, got %v", motor.Status())
	}

	// On should trigger the PWM error
	if err := motor.On(); err == nil {
		t.Fatal("expected error from faulty PWM bus on On()")
	}
	if motor.Status() != StatusError {
		t.Errorf("status should be StatusError, got %v", motor.Status())
	}

	// Clear error, try again — should recover
	pwm.SetError(nil)
	if err := motor.On(); err != nil {
		t.Fatal(err)
	}
	if motor.Status() != StatusOK {
		t.Errorf("after clearing error, status should be OK, got %v", motor.Status())
	}
}

// ============================================================================
// GPIO Actuators — aerator & circulation pump
// ============================================================================

func TestActuatorGPIO_AeratorOnOff(t *testing.T) {
	gpio := NewMockGPIOBus()
	aerator := NewAerator("aerator-01", gpio)

	if aerator.IsOn() {
		t.Error("aerator should start off")
	}

	// On
	if err := aerator.On(); err != nil {
		t.Fatalf("On failed: %v", err)
	}
	if !gpio.State() {
		t.Error("GPIO should be HIGH after On()")
	}
	if !aerator.IsOn() {
		t.Error("aerator should be on")
	}

	// Off
	if err := aerator.Off(); err != nil {
		t.Fatalf("Off failed: %v", err)
	}
	if gpio.State() {
		t.Error("GPIO should be LOW after Off()")
	}
	if aerator.IsOn() {
		t.Error("aerator should be off")
	}
}

func TestActuatorGPIO_CirculationPumpOnOff(t *testing.T) {
	gpio := NewMockGPIOBus()
	pump := NewCirculationPump("pump-01", gpio)

	if pump.IsOn() {
		t.Error("pump should start off")
	}

	if err := pump.On(); err != nil {
		t.Fatalf("On failed: %v", err)
	}
	if !pump.IsOn() || !gpio.State() {
		t.Error("pump should be on, GPIO HIGH")
	}

	if err := pump.Off(); err != nil {
		t.Fatalf("Off failed: %v", err)
	}
	if pump.IsOn() || gpio.State() {
		t.Error("pump should be off, GPIO LOW")
	}
}

func TestActuatorGPIO_SetSpeedBinary(t *testing.T) {
	gpio := NewMockGPIOBus()
	aerator := NewAerator("aer-01", gpio)

	// SetSpeed > 0 → on
	if err := aerator.SetSpeed(1); err != nil {
		t.Fatal(err)
	}
	if !aerator.IsOn() {
		t.Error("SetSpeed(1) should enable aerator")
	}

	// SetSpeed == 0 → off
	if err := aerator.SetSpeed(0); err != nil {
		t.Fatal(err)
	}
	if aerator.IsOn() {
		t.Error("SetSpeed(0) should disable aerator")
	}
}

func TestActuatorGPIO_ErrorStatus(t *testing.T) {
	gpio := NewMockGPIOBus()
	gpio.SetError(errors.New("gpio pin stuck"))
	aerator := NewAerator("aer-01", gpio)

	if err := aerator.On(); err == nil {
		t.Fatal("expected error from faulty GPIO")
	}
	if aerator.Status() != StatusError {
		t.Errorf("status should be ERROR, got %v", aerator.Status())
	}

	gpio.SetError(nil)
	if err := aerator.On(); err != nil {
		t.Fatal(err)
	}
	if aerator.Status() != StatusOK {
		t.Errorf("after recovery, status should be OK, got %v", aerator.Status())
	}
}

// ============================================================================
// All sensor types — create & read
// ============================================================================

func TestAllSensorTypes_CreateAndRead(t *testing.T) {
	t.Run("pH", func(t *testing.T) {
		s := NewPHSensor("pH", newMockReader(7.2))
		v, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(v-7.2) > 0.01 {
			t.Errorf("pH = %f, want 7.2", v)
		}
	})

	t.Run("DO", func(t *testing.T) {
		s := NewDOSensor("DO", newMockReader(6.5))
		v, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(v-6.5) > 0.01 {
			t.Errorf("DO = %f, want 6.5", v)
		}
	})

	t.Run("Temperature", func(t *testing.T) {
		bus := NewMockOneWire(25.3)
		s := NewTempSensor("temp", bus)
		v, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(v-25.3) > 0.01 {
			t.Errorf("temp = %f, want 25.3", v)
		}
	})

	t.Run("NH3", func(t *testing.T) {
		s := NewNH3Sensor("NH3", newMockReader(0.15))
		v, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(v-0.15) > 0.001 {
			t.Errorf("NH3 = %f, want 0.15", v)
		}
	})

	t.Run("Turbidity", func(t *testing.T) {
		s := NewTurbiditySensor("turb", newMockReader(12.5))
		v, err := s.Read()
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(v-12.5) > 0.1 {
			t.Errorf("turbidity = %f, want 12.5", v)
		}
	})
}

// ============================================================================
// Temp sensor — calibration offset
// ============================================================================

func TestTempSensor_CalibrateWithOffset(t *testing.T) {
	bus := NewMockOneWire(26.0)
	s := NewTempSensor("temp-cal", bus)
	ts, ok := s.(*TempSensor)
	if !ok {
		t.Fatal("type assertion failed")
	}

	// DS18B20 reads 26.0, reference thermometer says 25.8
	ts.CalibrateWithOffset(26.0, 25.8)

	v, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	// After calibration, reading + offset should equal reference
	const epsilon = 0.01
	if math.Abs(v-25.8) > epsilon {
		t.Errorf("after offset calib: temp = %f, want 25.8", v)
	}
}

func TestTempSensor_BrokenBus(t *testing.T) {
	bus := NewMockOneWire(0)
	bus.SetError(errors.New("1-wire short"))
	s := NewTempSensor("temp-broken", bus)

	_, err := s.Read()
	if err == nil {
		t.Fatal("expected error from broken 1-Wire bus")
	}
	if s.Status() != StatusError {
		t.Errorf("broken bus → status should be ERROR, got %v", s.Status())
	}
}

// ============================================================================
// Mock ADC
// ============================================================================

func TestMockADC_ReadChannel(t *testing.T) {
	adc := NewMockADC()
	adc.SetChannel(0, 12345)
	adc.SetChannel(1, 32767)
	adc.SetChannel(2, 0)

	v, err := adc.ReadChannel(0)
	if err != nil {
		t.Fatal(err)
	}
	if v != 12345 {
		t.Errorf("ch0 = %d, want 12345", v)
	}

	v, err = adc.ReadChannel(1)
	if err != nil {
		t.Fatal(err)
	}
	if v != 32767 {
		t.Errorf("ch1 = %d, want 32767", v)
	}

	v, err = adc.ReadChannel(9) // unset
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("unset ch9 = %d, want 0", v)
	}
}

// ============================================================================
// Mock OneWire
// ============================================================================

func TestMockOneWire_ReadTemperature(t *testing.T) {
	bus := NewMockOneWire(23.5)
	v, err := bus.ReadTemperature()
	if err != nil {
		t.Fatal(err)
	}
	if v != 23.5 {
		t.Errorf("temp = %f, want 23.5", v)
	}

	bus.SetTemperature(30.0)
	v, err = bus.ReadTemperature()
	if err != nil {
		t.Fatal(err)
	}
	if v != 30.0 {
		t.Errorf("updated temp = %f, want 30.0", v)
	}

	bus.SetError(errors.New("crc mismatch"))
	_, err = bus.ReadTemperature()
	if err == nil {
		t.Fatal("expected error after SetError")
	}
}

// ============================================================================
// Linear regression
// ============================================================================

func TestLinearRegression_PerfectFit(t *testing.T) {
	x := []float64{1.0, 2.0, 3.0}
	y := []float64{2.0, 3.0, 4.0} // y = x + 1

	slope, intercept := linearRegression(x, y)
	const epsilon = 0.0001
	if math.Abs(slope-1.0) > epsilon {
		t.Errorf("slope = %f, want 1.0", slope)
	}
	if math.Abs(intercept-1.0) > epsilon {
		t.Errorf("intercept = %f, want 1.0", intercept)
	}
}

func TestLinearRegression_Identity(t *testing.T) {
	x := []float64{0, 1, 2, 3, 4, 5}
	y := []float64{0, 1, 2, 3, 4, 5}

	slope, intercept := linearRegression(x, y)
	const epsilon = 0.0001
	if math.Abs(slope-1.0) > epsilon {
		t.Errorf("slope = %f, want 1.0", slope)
	}
	if math.Abs(intercept) > epsilon {
		t.Errorf("intercept = %f, want 0", intercept)
	}
}

// ============================================================================
// Interface compliance check (compile-time)
// ============================================================================

var _ Sensor = (*pHSensor)(nil)
var _ Sensor = (*DOSensor)(nil)
var _ Sensor = (*TempSensor)(nil)
var _ Sensor = (*NH3Sensor)(nil)
var _ Sensor = (*TurbiditySensor)(nil)

var _ Actuator = (*FeedingMotor)(nil)
var _ Actuator = (*Aerator)(nil)
var _ Actuator = (*CirculationPump)(nil)

var _ ADCBus = (*MockADC)(nil)
var _ OneWireBus = (*MockOneWire)(nil)
var _ PWMBus = (*MockPWMBus)(nil)
var _ GPIOBus = (*MockGPIOBus)(nil)
