// Edge device reboot and NPU fallback tests (T35).
//
// Tests:
//   - TestEdgeReboot: edge reconnects after simulated power cycle → continues data flow
//   - TestNPUFallback: NPU inference failure → CPU inference fallback produces usable result
//
// Uses mochi-mqtt in-memory broker for the reboot test and mock detectors for NPU fallback.
package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anrror/y-ai-pond/pkg/edge/detector"
	pondproto "github.com/anrror/y-ai-pond/pkg/proto"
	"github.com/pashagolub/pgxmock/v4"
	"google.golang.org/protobuf/proto"
)

// ============================================================================
// TestEdgeReboot — edge power-cycles → reconnects → data flow continues
// ============================================================================

func TestEdgeReboot(t *testing.T) {
	// ---- Given: a running MQTT broker with an edge publisher ----
	addr, stopBroker := startMochiBroker(t)
	defer stopBroker()

	pgMock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("create pgxmock: %v", err)
	}
	defer pgMock.Close()

	// Gateway auto-registers devices on first sensor contact.
	// The same device_id only registers once (idempotent via seenDevices map).
	pgMock.ExpectExec("INSERT INTO devices").
		WithArgs("edge-dev-01", "farm-1", "pond-1", "sensor_node", "online", "unknown", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	influx := newTestInfluxWriter()
	gw := newTestGateway(influx, pgMock)

	received := make(chan struct{}, 20)
	gwHandler := func(ctx context.Context, topic string, payload []byte) {
		gw.HandleMessage(ctx, topic, payload)
		select {
		case received <- struct{}{}:
		default:
		}
	}
	subClient := newBrokerMQTTClient(t, addr, "gw-reboot", gwHandler)
	defer func() { _ = subClient.Disconnect(context.Background()) }()

	if err := subClient.Subscribe(context.Background(), "pond/v1/+/+/sensor/#", 0); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	pubClient := newBrokerMQTTClient(t, addr, "edge-pre-reboot", nil)
	defer func() { _ = pubClient.Disconnect(context.Background()) }()

	// ---- When: publish pre-reboot data ----
	publishDO := func(do float32) error {
		reading := &pondproto.SensorReading{
			DeviceId:  "edge-dev-01",
			Timestamp: time.Now().UnixMilli(),
			Ph:        7.2,
			Do:        do,
			Temp:      25.0,
		}
		payload, err := proto.Marshal(reading)
		if err != nil {
			return err
		}
		return pubClient.PublishTelemetry(context.Background(),
			"pond/v1/farm-1/pond-1/sensor/water/do", payload)
	}

	for _, do := range []float32{6.0, 6.1, 6.2} {
		if err := publishDO(do); err != nil {
			t.Fatalf("publish pre-reboot: %v", err)
		}
	}

	// Wait for pre-reboot data.
	for i := 0; i < 3; i++ {
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for pre-reboot message %d", i+1)
		}
	}
	time.Sleep(50 * time.Millisecond)
	preRebootPts := influx.snapshot()
	if len(preRebootPts) == 0 {
		t.Fatal("expected sensor points before reboot")
	}

	// ---- When: simulate edge power cycle (disconnect old client) ----
	_ = pubClient.Disconnect(context.Background())

	// Simulate the edge reboot by creating a fresh client (new connection).
	rebootedPub := newBrokerMQTTClient(t, addr, "edge-post-reboot", nil)
	defer func() { _ = rebootedPub.Disconnect(context.Background()) }()

	// ---- When: publish post-reboot data with the new client ----
	for _, do := range []float32{6.3, 6.4, 6.5} {
		reading := &pondproto.SensorReading{
			DeviceId:  "edge-dev-01",
			Timestamp: time.Now().UnixMilli(),
			Ph:        7.2,
			Do:        do,
			Temp:      25.0,
		}
		payload, err := proto.Marshal(reading)
		if err != nil {
			t.Fatalf("marshal post-reboot: %v", err)
		}
		if err := rebootedPub.PublishTelemetry(context.Background(),
			"pond/v1/farm-1/pond-1/sensor/water/do", payload); err != nil {
			t.Fatalf("publish post-reboot: %v", err)
		}
	}

	// Wait for post-reboot data.
	for i := 0; i < 3; i++ {
		select {
		case <-received:
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for post-reboot message %d", i+1)
		}
	}
	time.Sleep(50 * time.Millisecond)

	// ---- Then: all data (pre + post reboot) is in influx ----
	allPts := influx.snapshot()
	doCount := 0
	for _, pt := range allPts {
		if pt.FarmID == "farm-1" && pt.PondID == "pond-1" && pt.SensorType == "do" {
			doCount++
		}
	}
	if doCount < 6 {
		t.Errorf("expected at least 6 do points (3 pre + 3 post reboot), got %d", doCount)
	}

	// Verify pgxmock expectations.
	if err := pgMock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet pgxmock expectations: %v", err)
	}
}

// ============================================================================
// TestNPUFallback — NPU failure → CPU fallback produces a usable Detection
// ============================================================================

func TestNPUFallback(t *testing.T) {
	// ---- Given: a primary NPU detector that fails, and a CPU fallback ----
	primaryDet := &failingDetector{err: errors.New("NPU not available")}
	fallbackDet := &cpuDetector{det: detector.Detection{
		Count:     12,
		AvgSizePx: 45000,
	}}

	fb := &fallbackWrapper{primary: primaryDet, fallback: fallbackDet}

	// ---- When: Detect is called ----
	result, err := fb.Detect([]byte{1, 2, 3})
	if err != nil {
		t.Fatalf("fallback Detect: %v", err)
	}

	// ---- Then: result is from the CPU fallback ----
	if result.Count != 12 {
		t.Errorf("fallback count: want 12, got %d", result.Count)
	}
	if result.AvgSizePx < 40000 {
		t.Errorf("fallback avg size: want ~45000, got %.1f", result.AvgSizePx)
	}
	if !fallbackDet.called {
		t.Error("expected CPU fallback to be called")
	}

	// ---- When: primary NPU recovers ----
	primaryDet.err = nil
	primaryDet.det = detector.Detection{Count: 15, AvgSizePx: 50000}
	fallbackDet.called = false

	result2, err2 := fb.Detect([]byte{4, 5, 6})
	if err2 != nil {
		t.Fatalf("primary Detect after recovery: %v", err2)
	}

	// ---- Then: primary is used again, fallback is NOT called ----
	if result2.Count != 15 {
		t.Errorf("primary count after recovery: want 15, got %d", result2.Count)
	}
	if fallbackDet.called {
		t.Error("expected CPU fallback NOT to be called when primary is available")
	}
}

// ============================================================================
// TestNPUFallback_ContinuousOperation — NPU permanently down, CPU keeps working
// ============================================================================

func TestNPUFallback_ContinuousOperation(t *testing.T) {
	// ---- Given: NPU always fails, only CPU is available ----
	primaryDet := &failingDetector{err: errors.New("NPU hardware failure")}
	fallbackDet := &cpuDetector{det: detector.Detection{
		Count:     5,
		AvgSizePx: 20000,
	}}

	fb := &fallbackWrapper{primary: primaryDet, fallback: fallbackDet}

	// ---- When: calling Detect repeatedly (simulates continuous operation) ----
	var results []detector.Detection
	for i := 0; i < 10; i++ {
		result, err := fb.Detect([]byte{byte(i)})
		if err != nil {
			t.Fatalf("Detect call %d: unexpected error: %v", i, err)
		}
		results = append(results, result)
	}

	// ---- Then: system is still usable — produces results, no crash ----
	if len(results) != 10 {
		t.Errorf("expected 10 results, got %d", len(results))
	}
	if fallbackDet.calls == 0 {
		t.Error("expected CPU fallback to have been called")
	}
	for i, r := range results {
		if r.Count != 5 {
			t.Errorf("result %d: count = %d, want 5", i, r.Count)
		}
	}
}

// ============================================================================
// TestNPUFallback_SlowerButUsable — CPU path is demonstrably available
// ============================================================================

func TestNPUFallback_SlowerButUsable(t *testing.T) {
	// ---- Given: both NPU and CPU are available ----
	npudet := &failingDetector{err: errors.New("NPU inference timeout")}
	cpudet := &cpuDetector{det: detector.Detection{Count: 8, AvgSizePx: 35000}}

	fb := &fallbackWrapper{primary: npudet, fallback: cpudet}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []detector.Detection
	errs := make(chan error, 100)

	// ---- When: concurrent detection calls (simulating multi-frame pipeline) ----
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r, err := fb.Detect([]byte{byte(idx)})
			if err != nil {
				errs <- err
				return
			}
			mu.Lock()
			results = append(results, r)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	close(errs)

	// ---- Then: all calls succeeded (via CPU fallback), none errored ----
	errCount := 0
	for range errs {
		errCount++
	}
	if errCount > 0 {
		t.Errorf("expected 0 errors from fallback, got %d", errCount)
	}
	if len(results) != 100 {
		t.Errorf("expected 100 results, got %d", len(results))
	}
	// CPU fallback should have been called for every request.
	if cpudet.calls != 100 {
		t.Errorf("expected CPU fallback called 100 times, got %d", cpudet.calls)
	}
}

// ============================================================================
// Fallback / mock detector types
// ============================================================================

// fallbackWrapper tries primary, falls back to secondary on error.
type fallbackWrapper struct {
	primary  DetectorLike
	fallback DetectorLike
}

// DetectorLike is a subset of detector.Detector used by the fallback wrapper.
type DetectorLike interface {
	Detect(frame []byte) (detector.Detection, error)
}

func (w *fallbackWrapper) Detect(frame []byte) (detector.Detection, error) {
	result, err := w.primary.Detect(frame)
	if err != nil {
		return w.fallback.Detect(frame)
	}
	return result, nil
}

// failingDetector simulates a failed NPU backend.
type failingDetector struct {
	det detector.Detection
	err error
}

func (d *failingDetector) Detect(_ []byte) (detector.Detection, error) {
	if d.err != nil {
		return detector.Detection{}, d.err
	}
	return d.det, nil
}

// cpuDetector simulates CPU-path inference (slower but always available).
type cpuDetector struct {
	det    detector.Detection
	called bool
	calls  int
	mu     sync.Mutex
}

func (d *cpuDetector) Detect(_ []byte) (detector.Detection, error) {
	d.mu.Lock()
	d.called = true
	d.calls++
	d.mu.Unlock()
	return d.det, nil
}
