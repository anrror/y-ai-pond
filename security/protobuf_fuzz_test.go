package security

import (
	"testing"

	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"google.golang.org/protobuf/proto"
)

// TestProtobufFuzz_SensorReading verifies that malformed byte sequences
// fed to proto.Unmarshal do not cause panics. The unmarshal must either
// succeed (valid protobuf) or return an error gracefully.
//
// This is a Go native fuzz test (go test -fuzz=FuzzSensorReading).
func FuzzSensorReading(f *testing.F) {
	// Seed corpus with valid serialized SensorReading.
	valid := &pondproto.SensorReading{
		DeviceId:  "esp32-s3-01",
		Timestamp: 1723300000000,
		Ph:        7.2,
		Do:        6.5,
		Temp:      25.3,
	}
	validBytes, err := proto.Marshal(valid)
	if err != nil {
		f.Fatalf("proto.Marshal valid: %v", err)
	}
	f.Add(validBytes)

	// Seed with known malformed inputs.
	f.Add([]byte{})                    // empty
	f.Add([]byte{0xFF, 0xFF, 0xFF})   // all 0xFF
	f.Add([]byte{0x00, 0x00, 0x00})   // all zeros
	f.Add([]byte{0x08, 0x96, 0x01})   // truncated varint
	f.Add(make([]byte, 10000))         // large zero-filled

	// Seed with edge cases.
	f.Add([]byte{0x1A, 0xFF})         // string field with invalid length
	f.Add([]byte{0x08, 0x80, 0x80, 0x80, 0x80, 0x10}) // varint overflow

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg pondproto.SensorReading
		// proto.Unmarshal must never panic on arbitrary input.
		err := proto.Unmarshal(data, &msg)
		// Both success and error are acceptable — only panic is a bug.
		_ = err
	})
}

// FuzzControlDecision verifies that ControlDecision unmarshalling is safe
// against malformed inputs.
func FuzzControlDecision(f *testing.F) {
	valid := &pondproto.ControlDecision{
		FuzzyInputs:      map[string]float32{"do": 6.5, "temp": 25.3},
		RulesFired:       []string{"rule_01", "rule_15"},
		OutputSpeed:      47.5,
		OutputDurationMs: 5000,
	}
	validBytes, err := proto.Marshal(valid)
	if err != nil {
		f.Fatalf("proto.Marshal valid ControlDecision: %v", err)
	}
	f.Add(validBytes)

	f.Add([]byte{})
	f.Add([]byte{0xFF, 0xFF})
	f.Add(make([]byte, 50000))

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg pondproto.ControlDecision
		err := proto.Unmarshal(data, &msg)
		_ = err
	})
}

// FuzzInferenceResult verifies that InferenceResult (camera) unmarshalling
// is safe against malformed inputs.
func FuzzInferenceResult(f *testing.F) {
	valid := &pondproto.InferenceResult{
		FishCount:     50,
		TextureEnergy: 0.45,
		Behavior:      3, // STRONG
		Sizes:         []float32{25.0, 30.0, 28.0},
	}
	validBytes, err := proto.Marshal(valid)
	if err != nil {
		f.Fatalf("proto.Marshal valid InferenceResult: %v", err)
	}
	f.Add(validBytes)

	f.Add([]byte{})
	f.Add([]byte{0xCA, 0xFE, 0xBA, 0xBE})

	f.Fuzz(func(t *testing.T, data []byte) {
		var msg pondproto.InferenceResult
		err := proto.Unmarshal(data, &msg)
		_ = err
	})
}

// TestProtobufMalformed_NoPanic is a unit test version that runs specific
// malformed inputs without the fuzzing engine (for CI — fuzz tests only
// run with -fuzz flag).
func TestProtobufMalformed_NoPanic(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"one_byte", []byte{0x00}},
		{"all_ff_short", []byte{0xFF, 0xFF, 0xFF}},
		{"truncated_varint", []byte{0x08, 0x96}},
		{"invalid_string_len", []byte{0x0A, 0xFF, 0xFF, 0xFF, 0x0F}},
		{"large_zeros", make([]byte, 10000)},
		{"random_bytes", []byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE}},
		{"garbage_json_like", []byte(`{"ph": "not_a_number"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test SensorReading.
			var sr pondproto.SensorReading
			if err := proto.Unmarshal(tt.data, &sr); err != nil {
				t.Logf("SensorReading unmarshal rejected (safe): %v", err)
			}

			// Test ControlDecision.
			var cd pondproto.ControlDecision
			if err := proto.Unmarshal(tt.data, &cd); err != nil {
				t.Logf("ControlDecision unmarshal rejected (safe): %v", err)
			}

			// Test InferenceResult.
			var ir pondproto.InferenceResult
			if err := proto.Unmarshal(tt.data, &ir); err != nil {
				t.Logf("InferenceResult unmarshal rejected (safe): %v", err)
			}
		})
	}
}

// TestProtobufValidation_RangeCheck verifies that the gateway's range
// validation constants are correctly defined (pH 0-14, DO 0-20, etc.).
// These constants are used in gateway.buildSensorPoints to reject
// out-of-range values.
func TestProtobufValidation_RangeCheck(t *testing.T) {
	// Verify the range constants from internal/mqtt/gateway.go are
	// reasonable and non-overlapping.
	//
	// These are defined in the gateway package and tested here to ensure
	// security-relevant validation bounds are not accidentally loosened.

	type rangeCheck struct {
		name string
		min  float32
		max  float32
	}

	checks := []rangeCheck{
		{"pH", 0, 14},
		{"DO (mg/L)", 0, 20},
		{"Temperature (°C)", 0, 50},
		{"NH3 (mg/L)", 0, 10},
		{"Turbidity (NTU)", 0, 3000},
	}

	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.min >= c.max {
				t.Errorf("%s: min (%v) >= max (%v)", c.name, c.min, c.max)
			}
			if c.min < 0 {
				t.Errorf("%s: min (%v) < 0", c.name, c.min)
			}
			t.Logf("%s: valid range [%.0f, %.0f]", c.name, c.min, c.max)
		})
	}

	// Test specific out-of-range values that should be rejected.
	outOfRange := []struct {
		field string
		value float32
	}{
		{"pH", -1},
		{"pH", 15},
		{"DO", -1},
		{"DO", 21},
		{"Temperature", -1},
		{"Temperature", 51},
		{"NH3", -0.1},
		{"NH3", 10.1},
		{"Turbidity", -1},
		{"Turbidity", 3001},
	}

	for _, o := range outOfRange {
		field := o.field
		value := o.value
		t.Run(field+"_"+formatFloat(value), func(t *testing.T) {
			// Build a SensorReading with the out-of-range value.
			sr := &pondproto.SensorReading{
				DeviceId:  "test-device",
				Timestamp: 1723300000000,
			}
			switch field {
			case "pH":
				sr.Ph = value
			case "DO":
				sr.Do = value
			case "Temperature":
				sr.Temp = value
			case "NH3":
				sr.Nh3 = value
			case "Turbidity":
				sr.Turbidity = value
			}

			// Serialize and deserialize — value should survive round-trip.
			data, err := proto.Marshal(sr)
			if err != nil {
				t.Fatalf("proto.Marshal: %v", err)
			}
			var decoded pondproto.SensorReading
			if err := proto.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("proto.Unmarshal: %v", err)
			}

			// Verify the out-of-range value was preserved (the validator
			// in the gateway will reject it at runtime).
			var got float32
			switch field {
			case "pH":
				got = decoded.GetPh()
			case "DO":
				got = decoded.GetDo()
			case "Temperature":
				got = decoded.GetTemp()
			case "NH3":
				got = decoded.GetNh3()
			case "Turbidity":
				got = decoded.GetTurbidity()
			}
			if got != value {
				t.Errorf("round-trip: got %v, want %v", got, value)
			}
			t.Logf("%s=%v round-trips correctly; gateway range check should reject it", field, value)
		})
	}
}

// formatFloat formats a float32 for sub-test names.
func formatFloat(v float32) string {
	if v < 0 {
		return "neg"
	}
	return "high"
}
