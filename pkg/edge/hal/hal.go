// Package hal provides the Hardware Abstraction Layer for edge device
// sensors and actuators. It defines pure-Go interfaces that decouple
// upper-level controllers from specific hardware implementations,
// enabling mock-based unit testing without real hardware.
//
// No cgo is used; hardware access (when available) goes through
// periph.io-compatible interfaces that are injected at startup.
package hal

import "fmt"

// Health encodes the operational status of a sensor or actuator.
type Health int

const (
	// StatusOK indicates the device is functioning normally.
	StatusOK Health = iota
	// StatusError indicates the device has failed, disconnected, or
	// returned readings outside the expected range.
	StatusError
)

// String returns a human-readable status label.
func (h Health) String() string {
	switch h {
	case StatusOK:
		return "OK"
	case StatusError:
		return "ERROR"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", h)
	}
}

// Sensor models any water-quality or environmental sensor.
//
// Read returns the current sensor value in the sensor's native unit
// (pH units, mg/L, °C, NTU, etc.) after applying calibration.
// Calibrate performs the sensor-specific calibration routine and
// updates the internal coefficients. The caller must supply
// reference values appropriate for the sensor type.
// Status returns the current health of the sensor.
type Sensor interface {
	Read() (float64, error)
	Calibrate() error
	Status() Health
}

// Actuator models any controllable output device (motor, pump, valve).
//
// On enables the actuator at its last-known speed.
// Off disables the actuator (speed = 0).
// SetSpeed sets the output level in percent [0, 100]. For binary
// (on/off) actuators, SetSpeed is equivalent to On when pct > 0 and
// Off when pct == 0.
// Status returns the current health of the actuator.
type Actuator interface {
	On() error
	Off() error
	SetSpeed(pct float64) error
	Status() Health
}

// Reader abstracts the raw hardware read path. Sensors use it to fetch
// a raw ADC count, voltage, or other uncalibrated value which is then
// converted by the sensor-specific driver into engineering units.
//
// In production this is backed by an I2C ADC or 1-Wire bus; in tests
// it is a mock that returns pre-programmed values.
type Reader interface {
	Read() (float64, error)
}

// CalibrationCoeffs holds the linear calibration parameters derived
// during 3-point (or N-point) buffer calibration.
//
//   engineering_value = slope * raw_value + intercept
type CalibrationCoeffs struct {
	Slope     float64
	Intercept float64
}

// IsZero reports whether the coefficients represent the uncalibrated
// identity mapping.
func (c CalibrationCoeffs) IsZero() bool {
	return c.Slope == 1 && c.Intercept == 0
}

// DefaultCalibration returns the identity calibration (raw == engineering).
func DefaultCalibration() CalibrationCoeffs {
	return CalibrationCoeffs{Slope: 1, Intercept: 0}
}
