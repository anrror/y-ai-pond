package hal

// OneWireBus abstracts the 1-Wire bus used by DS18B20 temperature
// sensors. On real hardware this is backed by a periph.io 1-Wire
// driver; in tests a MockOneWire is used.
type OneWireBus interface {
	// ReadTemperature returns the temperature in degrees Celsius.
	ReadTemperature() (float64, error)
}

// MockOneWire is an in-memory OneWireBus for unit tests.
type MockOneWire struct {
	temp  float64
	err   error
}

// NewMockOneWire creates a MockOneWire that returns the given temperature.
func NewMockOneWire(temp float64) *MockOneWire {
	return &MockOneWire{temp: temp}
}

// SetError forces subsequent ReadTemperature calls to return the given error.
func (m *MockOneWire) SetError(err error) {
	m.err = err
}

// SetTemperature updates the temperature returned by ReadTemperature.
func (m *MockOneWire) SetTemperature(temp float64) {
	m.temp = temp
}

// ReadTemperature returns the programmed temperature or error.
func (m *MockOneWire) ReadTemperature() (float64, error) {
	return m.temp, m.err
}
