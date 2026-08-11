package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/anrror/y-ai-pond/pkg/edge/detector"
	"github.com/anrror/y-ai-pond/pkg/edge/fuzzypid"
	"github.com/anrror/y-ai-pond/pkg/edge/hal"
	"github.com/anrror/y-ai-pond/pkg/edge/safety"
	"github.com/anrror/y-ai-pond/pkg/edge/texture"
)

// ============================================================================
// Mock types (unexported, same package for white-box access)
// ============================================================================

type mockCamera struct {
	frames []CameraFrame
	pos    int
	err    error
	calls  int
}

func (m *mockCamera) NextFrame(_ context.Context) (CameraFrame, error) {
	m.calls++
	if m.err != nil {
		return CameraFrame{}, m.err
	}
	if len(m.frames) == 0 {
		return CameraFrame{}, nil
	}
	f := m.frames[m.pos%len(m.frames)]
	m.pos++
	return f, nil
}

type mockDetector struct {
	det   detector.Detection
	err   error
	calls int
}

func (m *mockDetector) Detect(_ []byte) (detector.Detection, error) {
	m.calls++
	return m.det, m.err
}

type mockSensors struct {
	snap  SensorSnapshot
	err   error
	calls int
}

func (m *mockSensors) Read(_ context.Context) (SensorSnapshot, error) {
	m.calls++
	return m.snap, m.err
}

type mockActuator struct {
	speed      float64
	forward    bool
	stopped    bool
	speedCalls int
	dirCalls   int
	stopCalls  int
}

func (m *mockActuator) SetSpeed(pct float64) error {
	m.speedCalls++
	m.speed = pct
	m.stopped = false
	return nil
}

func (m *mockActuator) SetDirection(forward bool) error {
	m.dirCalls++
	m.forward = forward
	return nil
}

func (m *mockActuator) Stop() error {
	m.stopCalls++
	m.stopped = true
	m.speed = 0
	return nil
}

func (m *mockActuator) Status() hal.Health {
	return hal.StatusOK
}

type mockReporter struct {
	err   error
	calls int
	last  StatusReport
}

func (m *mockReporter) Report(_ context.Context, r StatusReport) error {
	m.calls++
	m.last = r
	return m.err
}

// grayFrame creates a 64x64 grayscale frame filled with the given value.
func grayFrame(v uint8) texture.Frame {
	gray := make([]uint8, 64*64)
	for i := range gray {
		gray[i] = v
	}
	return texture.Frame{Gray: gray, Width: 64, Height: 64}
}

// drainDecisions flushes the decisions channel and calls reporter for each
// pending report so tick-driven tests can observe reporter calls.
func drainDecisions(ch chan StatusReport, rep Reporter) {
	for {
		select {
		case r := <-ch:
			if rep != nil {
				_ = rep.Report(context.Background(), r)
			}
		default:
			return
		}
	}
}

// ============================================================================
// TestControllerLoop — density increase drives PWM up over 100 ticks.
// ============================================================================

func TestControllerLoop(t *testing.T) {
	// Two alternating frames with enough difference to produce non-zero
	// texture intensity, keeping FI in M range so that high-density rules
	// (rule 23: VH+M->MAX) fire and PWM rises with density.
	frameA := CameraFrame{Raw: []byte{1}, Gray: grayFrame(120)}
	frameB := CameraFrame{Raw: []byte{2}, Gray: grayFrame(140)}
	cam := &mockCamera{frames: []CameraFrame{frameA, frameB}}
	det := &mockDetector{}
	sens := &mockSensors{snap: SensorSnapshot{DO: 6, Temp: 25, NH3: 0.1}}
	act := &mockActuator{}

	ctrl, err := New(DefaultConfig(), Deps{
		Camera:   cam,
		Detector: det,
		Analyzer: DefaultTextureAnalyzer{},
		Sensors:  sens,
		Fuzzy:    fuzzypid.NewDefault(),
		Safety:   safety.NewEvaluator(safety.DefaultConfig()),
		Actuator: act,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	var pwms []float64
	for i := 0; i < 100; i++ {
		// Increasing density and size: 1..100 fish, 10K..200K avg px area.
		det.det = detector.Detection{
			Count:     i + 1,
			AvgSizePx: float32(10000 + i*2000),
		}
		ctrl.tick(ctx)
		pwms = append(pwms, act.speed)
		drainDecisions(ctrl.decisions, nil)
	}

	// All PWM values must be in [0, 100].
	for i, v := range pwms {
		if v < 0 || v > 100 {
			t.Errorf("tick %d: pwm %.2f out of [0, 100]", i, v)
		}
	}

	// Density increased from 1 to 100 -> PWM should rise.
	if len(pwms) < 2 {
		t.Fatal("need at least 2 ticks")
	}
	if pwms[len(pwms)-1] <= pwms[0] {
		t.Errorf("expected PWM to rise with density: first=%.2f last=%.2f", pwms[0], pwms[len(pwms)-1])
	}

	if act.speedCalls == 0 {
		t.Error("expected actuator speed calls > 0")
	}
}

// ============================================================================
// TestOfflineFallback — reporter errors don't crash; local inference continues.
// ============================================================================

func TestOfflineFallback(t *testing.T) {
	frame := CameraFrame{Raw: []byte{1}, Gray: grayFrame(128)}
	cam := &mockCamera{frames: []CameraFrame{frame}}
	det := &mockDetector{det: detector.Detection{Count: 10, AvgSizePx: 50000}}
	sens := &mockSensors{snap: SensorSnapshot{DO: 6, Temp: 25, NH3: 0.1}}
	act := &mockActuator{}
	rep := &mockReporter{err: errors.New("mqtt disconnected")}

	ctrl, err := New(DefaultConfig(), Deps{
		Camera:   cam,
		Detector: det,
		Analyzer: DefaultTextureAnalyzer{},
		Sensors:  sens,
		Fuzzy:    fuzzypid.NewDefault(),
		Safety:   safety.NewEvaluator(safety.DefaultConfig()),
		Actuator: act,
		Reporter: rep,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		ctrl.tick(ctx)
		drainDecisions(ctrl.decisions, rep)
	}

	if det.calls != 10 {
		t.Errorf("detector calls = %d, want 10 (local inference continues)", det.calls)
	}
	if act.speedCalls == 0 {
		t.Error("expected actuator speed calls > 0")
	}
	if rep.calls == 0 {
		t.Error("expected reporter calls > 0 (should try to report)")
	}
}

// ============================================================================
// TestNoCameraFallback — camera error -> sensor-only mode, detector skipped.
// ============================================================================

func TestNoCameraFallback(t *testing.T) {
	cam := &mockCamera{err: errors.New("camera down")}
	det := &mockDetector{}
	sens := &mockSensors{snap: SensorSnapshot{DO: 6, Temp: 25, NH3: 0.1}}
	act := &mockActuator{}

	ctrl, err := New(DefaultConfig(), Deps{
		Camera:   cam,
		Detector: det,
		Analyzer: DefaultTextureAnalyzer{},
		Sensors:  sens,
		Fuzzy:    fuzzypid.NewDefault(),
		Safety:   safety.NewEvaluator(safety.DefaultConfig()),
		Actuator: act,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		ctrl.tick(ctx)
		drainDecisions(ctrl.decisions, nil)
	}

	if ctrl.mode != ModeSensorOnly {
		t.Errorf("mode = %s, want %s", ctrl.mode, ModeSensorOnly)
	}
	if det.calls != 0 {
		t.Errorf("detector calls = %d, want 0 (camera down)", det.calls)
	}
	if act.speedCalls == 0 {
		t.Error("expected actuator speed calls > 0 (sensor-based fuzzy drives PWM)")
	}
}

// ============================================================================
// TestNewRequiresDeps — missing required dependency returns error.
// ============================================================================

func TestNewRequiresDeps(t *testing.T) {
	base := Deps{
		Detector: &mockDetector{},
		Analyzer: DefaultTextureAnalyzer{},
		Sensors:  &mockSensors{},
		Fuzzy:    fuzzypid.NewDefault(),
		Safety:   safety.NewEvaluator(safety.DefaultConfig()),
		Actuator: &mockActuator{},
	}

	tests := []struct {
		name string
		deps Deps
		want string
	}{
		{
			name: "missing Detector",
			deps: func() Deps { d := base; d.Detector = nil; return d }(),
			want: "controller: Detector is required",
		},
		{
			name: "missing Analyzer",
			deps: func() Deps { d := base; d.Analyzer = nil; return d }(),
			want: "controller: Analyzer is required",
		},
		{
			name: "missing Sensors",
			deps: func() Deps { d := base; d.Sensors = nil; return d }(),
			want: "controller: Sensors is required",
		},
		{
			name: "missing Fuzzy",
			deps: func() Deps { d := base; d.Fuzzy = nil; return d }(),
			want: "controller: Fuzzy is required",
		},
		{
			name: "missing Safety",
			deps: func() Deps { d := base; d.Safety = nil; return d }(),
			want: "controller: Safety is required",
		},
		{
			name: "missing Actuator",
			deps: func() Deps { d := base; d.Actuator = nil; return d }(),
			want: "controller: Actuator is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(DefaultConfig(), tt.deps, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if err.Error() != tt.want {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
