package proto_test

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
)

// populatedSensorReading returns a SensorReading with all fields set.
func populatedSensorReading() *pondproto.SensorReading {
	return &pondproto.SensorReading{
		DeviceId:   "esp32-s3-pond-01",
		Timestamp:  1723300000000,
		Ph:         7.35,
		Do:         6.8,
		Temp:       26.5,
		Nh3:        0.12,
		Turbidity:  15.7,
		WaterLevel: 145.0,
	}
}

func TestSensorReadingSerializeDeserialize(t *testing.T) {
	// Given: a fully populated SensorReading
	original := populatedSensorReading()

	// When: serialized via proto.Marshal then deserialized via proto.Unmarshal
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	deserialized := &pondproto.SensorReading{}
	if err := proto.Unmarshal(data, deserialized); err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}

	// Then: the round-tripped message equals the original
	if !proto.Equal(original, deserialized) {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  deserialized: %+v", original, deserialized)
	}
}

func TestBinaryVsJSON(t *testing.T) {
	// Given: a fully populated SensorReading with realistic sensor values
	msg := populatedSensorReading()

	// When: serialized via proto.Marshal (binary) and protojson.Marshal (JSON)
	binaryData, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	jsonData, err := protojson.Marshal(msg)
	if err != nil {
		t.Fatalf("protojson.Marshal failed: %v", err)
	}

	binarySize := len(binaryData)
	jsonSize := len(jsonData)

	// Then: binary size must be less than 50% of JSON size
	if binarySize*2 >= jsonSize {
		t.Errorf("binary size (%d bytes) is not < 50%% of JSON size (%d bytes). ratio: %.1f%%",
			binarySize, jsonSize, float64(binarySize)/float64(jsonSize)*100)
	}

	// Sanity check: binary should be non-zero
	if binarySize == 0 {
		t.Error("binary size is 0, expected non-empty serialization")
	}

	t.Logf("binary: %d bytes, JSON: %d bytes (%.1f%%)", binarySize, jsonSize,
		float64(binarySize)/float64(jsonSize)*100)
}

func TestSensorReadingZeroValues(t *testing.T) {
	// Given: a SensorReading with all zero/empty values (protobuf defaults)
	msg := &pondproto.SensorReading{}

	// When: serialized and deserialized
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("proto.Marshal of empty message failed: %v", err)
	}

	deserialized := &pondproto.SensorReading{}
	if err := proto.Unmarshal(data, deserialized); err != nil {
		t.Fatalf("proto.Unmarshal of empty message failed: %v", err)
	}

	// Then: the deserialized message should equal the original (empty)
	if !proto.Equal(msg, deserialized) {
		t.Errorf("empty message round-trip mismatch: %+v vs %+v", msg, deserialized)
	}
}

func TestInferenceResultEnumRoundTrip(t *testing.T) {
	// Given: an InferenceResult with BEHAVIOR_FEEDING behavior
	original := &pondproto.InferenceResult{
		FishCount:     42,
		Sizes:         []float32{120.5, 135.2, 110.8},
		Behavior:      pondproto.Behavior_BEHAVIOR_FEEDING,
		TextureEnergy: 0.65,
	}

	// When: serialized and deserialized
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	deserialized := &pondproto.InferenceResult{}
	if err := proto.Unmarshal(data, deserialized); err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}

	// Then: all fields match including the enum
	if !proto.Equal(original, deserialized) {
		t.Errorf("InferenceResult round-trip mismatch: %+v vs %+v", original, deserialized)
	}

	if deserialized.Behavior != pondproto.Behavior_BEHAVIOR_FEEDING {
		t.Errorf("behavior enum: expected BEHAVIOR_FEEDING, got %v", deserialized.Behavior)
	}
}

func TestControlDecisionMapField(t *testing.T) {
	// Given: a ControlDecision with fuzzy_inputs map and rules_fired
	original := &pondproto.ControlDecision{
		FuzzyInputs: map[string]float32{
			"fish_density": 0.85,
			"do":           0.60,
			"temp":         0.45,
		},
		RulesFired:       []string{"R12: HIGH_density + NORMAL_DO → INCREASE", "R07: MEDIUM_temp → MAINTAIN"},
		OutputSpeed:      850.0,
		OutputDurationMs: 15000,
	}

	// When: serialized and deserialized
	data, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	deserialized := &pondproto.ControlDecision{}
	if err := proto.Unmarshal(data, deserialized); err != nil {
		t.Fatalf("proto.Unmarshal failed: %v", err)
	}

	// Then: all fields match including map and repeated fields
	if !proto.Equal(original, deserialized) {
		t.Errorf("ControlDecision round-trip mismatch: %+v vs %+v", original, deserialized)
	}

	if len(deserialized.FuzzyInputs) != 3 {
		t.Errorf("fuzzy_inputs map: expected 3 entries, got %d", len(deserialized.FuzzyInputs))
	}
}
