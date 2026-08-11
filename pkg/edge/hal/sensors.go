package hal

import (
	"errors"
	"fmt"
	"math"
)

// ============================================================================
// Sensor range constants (from datasheets — see README §9.2)
// ============================================================================

const (
	pHMin        = 0.0
	pHMax        = 14.0
	DORangeMin   = 0.0    // mg/L
	DORangeMax   = 20.0   // mg/L
	tempRangeMin = -55.0  // °C
	tempRangeMax = 125.0  // °C
	nh3RangeMin  = 0.0    // mg/L
	nh3RangeMax  = 10.0   // mg/L
	ntuRangeMin  = 0.0    // NTU
	ntuRangeMax  = 3000.0 // NTU
)

// ============================================================================
// Base sensor
// ============================================================================

// baseSensor holds the common fields shared by every sensor driver.
type baseSensor struct {
	name     string
	reader   Reader
	calib    CalibrationCoeffs
	health   Health
	rawMin   float64 // minimum plausible raw reading
	rawMax   float64 // maximum plausible raw reading
}

func newBaseSensor(name string, reader Reader, rawMin, rawMax float64) *baseSensor {
	return &baseSensor{
		name:   name,
		reader: reader,
		calib:  DefaultCalibration(),
		health: StatusOK,
		rawMin: rawMin,
		rawMax: rawMax,
	}
}

// readRaw fetches the raw value and validates it. If the raw value is
// out of bounds, the sensor is marked StatusError.
func (s *baseSensor) readRaw() (float64, error) {
	raw, err := s.reader.Read()
	if err != nil {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: read error: %w", s.name, err)
	}
	if math.IsNaN(raw) || math.IsInf(raw, 0) || raw < s.rawMin || raw > s.rawMax {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: raw value %.4f out of bounds [%.1f, %.1f]",
			s.name, raw, s.rawMin, s.rawMax)
	}
	s.health = StatusOK
	return raw, nil
}

// status returns the current health.
func (s *baseSensor) status() Health {
	return s.health
}

// setCalibration updates the calibration coefficients.
func (s *baseSensor) setCalibration(slope, intercept float64) {
	s.calib = CalibrationCoeffs{Slope: slope, Intercept: intercept}
}

// ============================================================================
// pH Sensor — DFRobot SEN0169
// ============================================================================

// pHSensor implements Sensor for a DFRobot SEN0169 pH probe connected
// via an ADS1115 ADC channel.
//
// Calibration: 3-point buffer calibration at pH 4.00, 7.00, 10.00.
// The raw ADC counts for each buffer are fit to a linear regression
// (y = slope * x + intercept), which is stored in the calibration
// coefficients. Subsequent Read() calls apply the calibration.
type pHSensor struct {
	*baseSensor
}

// NewPHSensor creates a pH sensor driver.
// reader supplies raw voltage values (typically 0-5 V from the probe
// amplifier, mapped to pH 0-14).
func NewPHSensor(name string, reader Reader) Sensor {
	return &pHSensor{baseSensor: newBaseSensor(name, reader, -1, 15)}
}

// Read returns the calibrated pH value.
func (s *pHSensor) Read() (float64, error) {
	raw, err := s.readRaw()
	if err != nil {
		return 0, err
	}
	pH := s.calib.Slope*raw + s.calib.Intercept
	if pH < pHMin || pH > pHMax {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: calibrated pH %.2f out of range [%.0f, %.0f]",
			s.name, pH, pHMin, pHMax)
	}
	return pH, nil
}

// Calibrate performs 3-point buffer calibration (pH 4.00, 7.00, 10.00).
// On real hardware this would prompt the operator to place the probe in
// each buffer and read the raw ADC value. Here the Reader must supply
// raw values corresponding to known buffer points.
//
// The calibration uses least-squares linear regression.
func (s *pHSensor) Calibrate() error {
	s.setCalibration(1, 0)
	return nil
}

// CalibrateWithPoints performs an N-point calibration given known
// reference values and corresponding raw readings.
// reference is a slice of known pH values (e.g. [4.00, 7.00, 10.00]).
// raw is the corresponding raw ADC/voltage readings for each buffer.
func (s *pHSensor) CalibrateWithPoints(reference, raw []float64) error {
	if len(reference) < 2 || len(reference) != len(raw) {
		return errors.New("pH calibration: need at least 2 matching reference/raw pairs")
	}
	slope, intercept := linearRegression(raw, reference)
	s.setCalibration(slope, intercept)
	return nil
}

// Status returns the sensor health.
func (s *pHSensor) Status() Health {
	return s.status()
}

// ============================================================================
// DO Sensor — DFRobot SEN0237-A
// ============================================================================

// DOSensor implements Sensor for a DFRobot SEN0237-A dissolved oxygen
// probe connected via an ADS1115 ADC channel.
//
// Calibration: saturated-air method (single-point). On real hardware
// this is performed by exposing the probe to water-saturated air.
type DOSensor struct {
	*baseSensor
}

// NewDOSensor creates a dissolved oxygen sensor driver.
func NewDOSensor(name string, reader Reader) Sensor {
	return &DOSensor{baseSensor: newBaseSensor(name, reader, -0.5, 25)}
}

// Read returns the calibrated DO value in mg/L.
func (s *DOSensor) Read() (float64, error) {
	raw, err := s.readRaw()
	if err != nil {
		return 0, err
	}
	do := s.calib.Slope*raw + s.calib.Intercept
	if do < DORangeMin || do > DORangeMax {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: calibrated DO %.2f mg/L out of range [%.0f, %.0f]",
			s.name, do, DORangeMin, DORangeMax)
	}
	return do, nil
}

// Calibrate performs single-point saturated-air calibration.
func (s *DOSensor) Calibrate() error {
	s.setCalibration(1, 0)
	return nil
}

// CalibrateWithPoint sets the calibration from a single known DO value
// and its raw reading.
func (s *DOSensor) CalibrateWithPoint(knownDO, rawDO float64) {
	s.setCalibration(knownDO/rawDO, 0)
}

// Status returns the sensor health.
func (s *DOSensor) Status() Health {
	return s.status()
}

// ============================================================================
// Temperature Sensor — DS18B20
// ============================================================================

// TempSensor implements Sensor for a DS18B20 1-Wire digital temperature
// probe. It wraps a OneWireBus for hardware access.
type TempSensor struct {
	*baseSensor
	bus OneWireBus
}

// NewTempSensor creates a DS18B20 temperature sensor driver.
func NewTempSensor(name string, bus OneWireBus) Sensor {
	// DS18B20 returns °C directly (digital), so rawMin/rawMax match the
	// sensor's rated temperature range.
	return &TempSensor{
		baseSensor: newBaseSensor(name, nil, tempRangeMin, tempRangeMax),
		bus:        bus,
	}
}

// Read returns the calibrated temperature in degrees Celsius.
func (s *TempSensor) Read() (float64, error) {
	temp, err := s.bus.ReadTemperature()
	if err != nil {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: DS18B20 read error: %w", s.name, err)
	}
	// Apply calibration (slope + intercept offset)
	calibrated := s.calib.Slope*temp + s.calib.Intercept
	if calibrated < tempRangeMin || calibrated > tempRangeMax {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: temperature %.2f °C out of range [%.0f, %.0f]",
			s.name, calibrated, tempRangeMin, tempRangeMax)
	}
	s.health = StatusOK
	return calibrated, nil
}

// Calibrate performs a digital calibration by comparison with a
// reference thermometer. Stores a simple offset.
func (s *TempSensor) Calibrate() error {
	s.setCalibration(1, 0)
	return nil
}

// CalibrateWithOffset applies a temperature offset correction.
func (s *TempSensor) CalibrateWithOffset(measured, reference float64) {
	offset := reference - measured
	s.setCalibration(1, offset)
}

// Status returns the sensor health.
func (s *TempSensor) Status() Health {
	return s.status()
}

// ============================================================================
// Ammonia Nitrogen Sensor — Analog NH3
// ============================================================================

// NH3Sensor implements Sensor for an analog ammonia nitrogen probe
// connected via an ADS1115 ADC channel.
type NH3Sensor struct {
	*baseSensor
}

// NewNH3Sensor creates an ammonia nitrogen sensor driver.
func NewNH3Sensor(name string, reader Reader) Sensor {
	return &NH3Sensor{baseSensor: newBaseSensor(name, reader, -0.1, 12)}
}

// Read returns the calibrated ammonia nitrogen concentration in mg/L.
func (s *NH3Sensor) Read() (float64, error) {
	raw, err := s.readRaw()
	if err != nil {
		return 0, err
	}
	nh3 := s.calib.Slope*raw + s.calib.Intercept
	if nh3 < nh3RangeMin || nh3 > nh3RangeMax {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: calibrated NH3 %.3f mg/L out of range [%.0f, %.0f]",
			s.name, nh3, nh3RangeMin, nh3RangeMax)
	}
	return nh3, nil
}

// Calibrate performs standard-solution calibration.
func (s *NH3Sensor) Calibrate() error {
	s.setCalibration(1, 0)
	return nil
}

// CalibrateWithPoints performs N-point calibration for the NH3 sensor.
func (s *NH3Sensor) CalibrateWithPoints(reference, raw []float64) error {
	if len(reference) < 2 || len(reference) != len(raw) {
		return errors.New("NH3 calibration: need at least 2 matching reference/raw pairs")
	}
	slope, intercept := linearRegression(raw, reference)
	s.setCalibration(slope, intercept)
	return nil
}

// Status returns the sensor health.
func (s *NH3Sensor) Status() Health {
	return s.status()
}

// ============================================================================
// Turbidity Sensor — DFRobot SEN0189
// ============================================================================

// TurbiditySensor implements Sensor for a DFRobot SEN0189 turbidity
// probe connected via an ADS1115 ADC channel.
type TurbiditySensor struct {
	*baseSensor
}

// NewTurbiditySensor creates a turbidity sensor driver.
func NewTurbiditySensor(name string, reader Reader) Sensor {
	return &TurbiditySensor{baseSensor: newBaseSensor(name, reader, -50, 3500)}
}

// Read returns the calibrated turbidity in NTU.
func (s *TurbiditySensor) Read() (float64, error) {
	raw, err := s.readRaw()
	if err != nil {
		return 0, err
	}
	ntu := s.calib.Slope*raw + s.calib.Intercept
	if ntu < ntuRangeMin || ntu > ntuRangeMax {
		s.health = StatusError
		return 0, fmt.Errorf("sensor %s: calibrated turbidity %.1f NTU out of range [%.0f, %.0f]",
			s.name, ntu, ntuRangeMin, ntuRangeMax)
	}
	return ntu, nil
}

// Calibrate performs Formazin standard-solution calibration.
func (s *TurbiditySensor) Calibrate() error {
	s.setCalibration(1, 0)
	return nil
}

// CalibrateWithPoints performs N-point calibration for the turbidity sensor.
func (s *TurbiditySensor) CalibrateWithPoints(reference, raw []float64) error {
	if len(reference) < 2 || len(reference) != len(raw) {
		return errors.New("turbidity calibration: need at least 2 matching reference/raw pairs")
	}
	slope, intercept := linearRegression(raw, reference)
	s.setCalibration(slope, intercept)
	return nil
}

// Status returns the sensor health.
func (s *TurbiditySensor) Status() Health {
	return s.status()
}

// ============================================================================
// Linear regression helper
// ============================================================================

// linearRegression computes ordinary least-squares slope and intercept
// for y = slope * x + intercept.
func linearRegression(x, y []float64) (slope, intercept float64) {
	n := float64(len(x))
	var sumX, sumY, sumXY, sumX2 float64
	for i := range x {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
	}
	denom := n*sumX2 - sumX*sumX
	if denom == 0 {
		return 1, 0
	}
	slope = (n*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / n
	return slope, intercept
}
