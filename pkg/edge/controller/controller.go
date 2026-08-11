// Package controller implements the edge main control loop that composes
// existing packages (detector, texture, fuzzypid, safety, hal, mqtt) via
// interfaces using a module orchestrator pattern. The loop runs at ~100 Hz
// and drives the feeding actuator through the following pipeline:
//
//	camera frame -> YOLOv8n detection -> texture intensity ->
//	sensor readings -> Fuzzy-PID -> safety evaluate -> PWM output.
//
// Camera and MQTT reporter are optional; the controller falls back to
// sensor-only mode when the camera is unavailable and skips reporting
// when no reporter is configured.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anrror/y-ai-pond/pkg/edge/detector"
	"github.com/anrror/y-ai-pond/pkg/edge/fuzzypid"
	"github.com/anrror/y-ai-pond/pkg/edge/hal"
	"github.com/anrror/y-ai-pond/pkg/edge/safety"
	"github.com/anrror/y-ai-pond/pkg/edge/texture"
)

// Mode represents the current operating mode of the controller.
type Mode string

const (
	// ModeVision indicates the controller is receiving valid camera frames.
	ModeVision Mode = "vision"
	// ModeSensorOnly indicates the camera is unavailable; decisions rely on
	// sensor data and the fuzzy controller alone.
	ModeSensorOnly Mode = "sensor_only"
)

// CameraFrame bundles raw image bytes with a precomputed grayscale frame
// for texture analysis.
type CameraFrame struct {
	Raw  []byte
	Gray texture.Frame
}

// CameraSource provides the next frame from the camera. Implementations
// must be non-blocking and return immediately with the latest available
// frame.
type CameraSource interface {
	NextFrame(ctx context.Context) (CameraFrame, error)
}

// Detector runs YOLOv8n inference on a raw frame and returns a Detection.
type Detector interface {
	Detect(frame []byte) (detector.Detection, error)
}

// TextureAnalyzer computes feeding intensity [0,1] from two consecutive
// grayscale frames.
type TextureAnalyzer interface {
	Intensity(prev, curr texture.Frame) float64
}

// SensorSnapshot holds the most recent water-quality sensor readings.
type SensorSnapshot struct {
	DO   float64
	Temp float64
	NH3  float64
}

// SensorReader reads the current sensor snapshot from the hardware.
type SensorReader interface {
	Read(ctx context.Context) (SensorSnapshot, error)
}

// FuzzyPID wraps the fuzzy-PID controller step function.
type FuzzyPID interface {
	Step(in *fuzzypid.Input) (float64, bool)
	Reset()
}

// SafetyEvaluator evaluates hardware safety interlocks.
type SafetyEvaluator interface {
	Evaluate(reads safety.SensorReadings, states safety.ActuatorStates, emerg safety.EmergencyInput) safety.SafetyDecision
}

// ActuatorDriver controls the feeding motor (PWM speed + H-bridge direction).
type ActuatorDriver interface {
	SetSpeed(pct float64) error
	SetDirection(forward bool) error
	Stop() error
	Status() hal.Health
}

// Aerator controls the aeration pump.
type Aerator interface {
	On() error
	Off() error
}

// StatusReport is the JSON-serializable edge device telemetry snapshot.
type StatusReport struct {
	DeviceID         string  `json:"device_id"`
	Mode             Mode    `json:"mode"`
	Decision         bool    `json:"decision"`
	FishCount        int     `json:"fish_count"`
	Density          float64 `json:"density"`
	FeedingIntensity float64 `json:"feeding_intensity"`
	DO               float64 `json:"do"`
	Temp             float64 `json:"temp"`
	NH3              float64 `json:"nh3"`
	PWM              float64 `json:"pwm"`
	Direction        string  `json:"direction"`
	Safety           string  `json:"safety"`
	UptimeSec        int64   `json:"uptime_seconds"`
	Timestamp        string  `json:"timestamp"`
}

// Reporter publishes StatusReport snapshots to the cloud.
type Reporter interface {
	Report(ctx context.Context, r StatusReport) error
}

// Config holds tuneable controller parameters.
type Config struct {
	DeviceID      string
	StatusTopic   string
	DecisionTopic string
	Period        time.Duration
	Heartbeat     time.Duration
	MaxFishCount  float64
}

// DefaultConfig returns sensible production defaults.
func DefaultConfig() Config {
	return Config{
		DeviceID:      "edge-01",
		StatusTopic:   "pond/v1/edge/status",
		DecisionTopic: "pond/v1/edge/control/feeding/decision",
		Period:        10 * time.Millisecond,
		Heartbeat:     30 * time.Second,
		MaxFishCount:  100,
	}
}

// ensureDefaults fills zero-valued Config fields with DefaultConfig values.
func ensureDefaults(cfg *Config) {
	def := DefaultConfig()
	if cfg.DeviceID == "" {
		cfg.DeviceID = def.DeviceID
	}
	if cfg.StatusTopic == "" {
		cfg.StatusTopic = def.StatusTopic
	}
	if cfg.DecisionTopic == "" {
		cfg.DecisionTopic = def.DecisionTopic
	}
	if cfg.Period == 0 {
		cfg.Period = def.Period
	}
	if cfg.Heartbeat == 0 {
		cfg.Heartbeat = def.Heartbeat
	}
	if cfg.MaxFishCount == 0 {
		cfg.MaxFishCount = def.MaxFishCount
	}
}

// Deps holds the injected module implementations.
type Deps struct {
	Camera   CameraSource
	Detector Detector
	Analyzer TextureAnalyzer
	Sensors  SensorReader
	Fuzzy    FuzzyPID
	Safety   SafetyEvaluator
	Actuator ActuatorDriver
	Aerator  Aerator
	Reporter Reporter
}

// Controller orchestrates the edge control loop.
type Controller struct {
	cfg       Config
	deps      Deps
	log       *slog.Logger
	startTime time.Time

	prevGray texture.Frame
	lastSnap SensorSnapshot
	mode     Mode

	lastPWM   float64
	lastDir   bool
	aeratorOn bool

	decisions chan StatusReport
	stateMu   sync.Mutex
	state     StatusReport
}

// New creates a Controller after validating required dependencies.
// Camera, Aerator, and Reporter are optional; all others are required.
func New(cfg Config, deps Deps, log *slog.Logger) (*Controller, error) {
	if deps.Detector == nil {
		return nil, fmt.Errorf("controller: Detector is required")
	}
	if deps.Analyzer == nil {
		return nil, fmt.Errorf("controller: Analyzer is required")
	}
	if deps.Sensors == nil {
		return nil, fmt.Errorf("controller: Sensors is required")
	}
	if deps.Fuzzy == nil {
		return nil, fmt.Errorf("controller: Fuzzy is required")
	}
	if deps.Safety == nil {
		return nil, fmt.Errorf("controller: Safety is required")
	}
	if deps.Actuator == nil {
		return nil, fmt.Errorf("controller: Actuator is required")
	}
	if log == nil {
		log = slog.Default()
	}

	ensureDefaults(&cfg)

	return &Controller{
		cfg:       cfg,
		deps:      deps,
		log:       log,
		startTime: time.Now(),
		mode:      ModeSensorOnly,
		decisions: make(chan StatusReport, 64),
	}, nil
}

// Run starts the main control loop. It blocks until ctx is cancelled, then
// performs an orderly shutdown (stop actuator, drain report goroutine).
func (c *Controller) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.reportLoop(ctx)
	}()

	ticker := time.NewTicker(c.cfg.Period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if err := c.deps.Actuator.Stop(); err != nil {
				c.log.Warn("controller: actuator stop on shutdown", "error", err)
			}
			wg.Wait()
			return nil
		case <-ticker.C:
			c.tick(ctx)
		}
	}
}

// tick runs one control cycle: camera -> detect -> texture -> sensors ->
// fuzzy -> safety -> actuator -> report.
func (c *Controller) tick(ctx context.Context) {
	var det detector.Detection
	var intensity float64

	// 1. Camera + detection + texture.
	if c.deps.Camera != nil {
		frame, err := c.deps.Camera.NextFrame(ctx)
		if err == nil && len(frame.Raw) > 0 {
			d, detErr := c.deps.Detector.Detect(frame.Raw)
			if detErr == nil {
				det = d
			}
			if c.prevGray.Width > 0 {
				intensity = c.deps.Analyzer.Intensity(c.prevGray, frame.Gray)
			}
			c.prevGray = frame.Gray
			c.mode = ModeVision
		} else {
			c.mode = ModeSensorOnly
		}
	} else {
		c.mode = ModeSensorOnly
	}

	// 2. Sensors.
	snap, err := c.deps.Sensors.Read(ctx)
	if err == nil {
		c.lastSnap = snap
	}

	// 3. Fuzzy-PID.
	in := &fuzzypid.Input{
		Density:          clamp01(float64(det.Count) / c.cfg.MaxFishCount),
		Size:             clamp01(float64(det.AvgSizePx) / 409600.0),
		FeedingIntensity: clamp01(intensity),
		DO:               c.lastSnap.DO,
		Temp:             c.lastSnap.Temp,
		NH3:              c.lastSnap.NH3,
	}
	pwm, _ := c.deps.Fuzzy.Step(in)

	// 4. Safety evaluation.
	dec := c.deps.Safety.Evaluate(
		safety.SensorReadings{DO: c.lastSnap.DO, Temp: c.lastSnap.Temp},
		safety.ActuatorStates{FeedingRunning: pwm > 0},
		safety.EmergencyInput{},
	)
	if dec.AllActuatorsOff || dec.FeedingForcedStop {
		pwm = 0
	}
	if dec.AeratorForced && c.deps.Aerator != nil && !c.aeratorOn {
		if err := c.deps.Aerator.On(); err != nil {
			c.log.Warn("controller: aerator on", "error", err)
		}
		c.aeratorOn = true
	} else if !dec.AeratorForced && c.deps.Aerator != nil && c.aeratorOn {
		if err := c.deps.Aerator.Off(); err != nil {
			c.log.Warn("controller: aerator off", "error", err)
		}
		c.aeratorOn = false
	}

	// 5. Actuator.
	if pwm <= 0 {
		if err := c.deps.Actuator.Stop(); err != nil {
			c.log.Warn("controller: actuator stop", "error", err)
		}
	} else {
		if err := c.deps.Actuator.SetSpeed(pwm); err != nil {
			c.log.Warn("controller: actuator set speed", "error", err)
		}
		if err := c.deps.Actuator.SetDirection(true); err != nil {
			c.log.Warn("controller: actuator set direction", "error", err)
		}
	}
	c.lastPWM = pwm
	c.lastDir = true

	// 6. Report.
	dir := "forward"
	if !c.lastDir {
		dir = "reverse"
	}
	safetyLabel := "ok"
	if dec.AllActuatorsOff {
		safetyLabel = "emergency_stop"
	} else if dec.FeedingForcedStop {
		safetyLabel = "feeding_stopped"
	}

	r := StatusReport{
		DeviceID:         c.cfg.DeviceID,
		Mode:             c.mode,
		Decision:         true,
		FishCount:        det.Count,
		Density:          clamp01(float64(det.Count) / c.cfg.MaxFishCount),
		FeedingIntensity: clamp01(intensity),
		DO:               c.lastSnap.DO,
		Temp:             c.lastSnap.Temp,
		NH3:              c.lastSnap.NH3,
		PWM:              pwm,
		Direction:        dir,
		Safety:           safetyLabel,
		UptimeSec:        int64(time.Since(c.startTime).Seconds()),
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
	}

	c.stateMu.Lock()
	prev := c.state
	changed := prev.PWM != r.PWM || prev.Mode != r.Mode || prev.Safety != r.Safety
	c.state = r
	c.stateMu.Unlock()

	if changed {
		select {
		case c.decisions <- r:
		default:
		}
	}
}

// reportLoop publishes status reports on heartbeat intervals and immediate
// decision changes.
func (c *Controller) reportLoop(ctx context.Context) {
	if c.deps.Reporter == nil {
		return
	}
	heartbeat := time.NewTicker(c.cfg.Heartbeat)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case r := <-c.decisions:
			c.publish(ctx, r)
		case <-heartbeat.C:
			c.stateMu.Lock()
			r := c.state
			c.stateMu.Unlock()
			r.Decision = false
			c.publish(ctx, r)
		}
	}
}

// publish sends a status report to the MQTT reporter with a 2-second timeout.
func (c *Controller) publish(ctx context.Context, r StatusReport) {
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.deps.Reporter.Report(pubCtx, r); err != nil {
		c.log.Warn("controller: report failed", "error", err)
	}
}

// clamp01 clamps v into [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
