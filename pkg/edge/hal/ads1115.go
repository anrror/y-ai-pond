package hal

// ADCBus abstracts the ADS1115 16-bit I2C ADC (or any compatible ADC).
// The implementation injects either a real periph.io-backed driver or a
// mock for testing.
type ADCBus interface {
	// ReadChannel reads the raw 16-bit ADC value from the given channel
	// (0-3 for a single ADS1115; multiplexed channels when using a
	// TCA9548A I2C mux are mapped to higher indices transparently).
	ReadChannel(ch int) (int, error)
}

// MockADC is an in-memory ADCBus implementation for unit tests.
// Each channel holds a pre-programmed raw ADC value.
type MockADC struct {
	channels map[int]int
}

// NewMockADC creates a MockADC with no channels programmed.
func NewMockADC() *MockADC {
	return &MockADC{channels: make(map[int]int)}
}

// SetChannel programs the raw ADC value that will be returned when
// ReadChannel is called for the given channel.
func (m *MockADC) SetChannel(ch int, value int) {
	m.channels[ch] = value
}

// ReadChannel returns the pre-programmed value, or 0 if the channel
// has not been set.
func (m *MockADC) ReadChannel(ch int) (int, error) {
	return m.channels[ch], nil
}
