package texture

import "sync"

// Behavior is the YOLOv8n behavior classification of the current frame.
type Behavior int

const (
	BehaviorUnknown    Behavior = iota // no behavior classification available
	BehaviorGathering                  // fish gathered near the surface
	BehaviorDispersing                 // fish spread out
	BehaviorScrambling                 // fish scrambling for feed
)

// FusionWeights are the YOLO11-PEGA three-feature fusion weights.
// They should sum to 1.0 to keep the fused score bounded in [0, 1].
type FusionWeights struct {
	Splash   float64 // splash_frequency
	BBoxArea float64 // mean_bbox_area (normalized)
	Texture  float64 // texture_energy
}

// DefaultWeights returns the default fusion weights
// (splash 0.4, bbox area 0.2, texture 0.4).
func DefaultWeights() FusionWeights {
	return FusionWeights{Splash: 0.4, BBoxArea: 0.2, Texture: 0.4}
}

// FusionInput aggregates the three YOLO11-PEGA features for one assessment.
// SplashFrequency and MeanBBoxArea are expected in [0, 1]; TextureEnergy is
// the output of TextureEnergy, already in [0, 1].
type FusionInput struct {
	SplashFrequency float64
	MeanBBoxArea    float64
	TextureEnergy   float64
	Behavior        Behavior
}

// FeedingIntensity is the fused feeding intensity assessment result.
type FeedingIntensity struct {
	Score           float64 // S_t in [0, 1]
	Level           string  // LevelNone, LevelWeak, LevelMedium or LevelStrong
	TextureEnergy   float64 // E from the frame-difference computation
	SplashFrequency float64 // fraction of recent frame pairs with splash activity
}

// Fuse computes S_t = clamp01(Sum(w_i * f_i) * behaviorFactor) in [0, 1]
// from the three features using the given weights.
func Fuse(in FusionInput, w FusionWeights) float64 {
	s := w.Splash*clamp01(in.SplashFrequency) +
		w.BBoxArea*clamp01(in.MeanBBoxArea) +
		w.Texture*clamp01(in.TextureEnergy)
	return clamp01(s * behaviorFactor(in.Behavior))
}

// Score computes S_t with the default fusion weights.
func (in FusionInput) Score() float64 {
	return Fuse(in, DefaultWeights())
}

// Assess fuses a FusionInput and classifies it into a FeedingIntensity.
func Assess(in FusionInput) FeedingIntensity {
	score := in.Score()
	return FeedingIntensity{
		Score:           score,
		Level:           Level(score),
		TextureEnergy:   in.TextureEnergy,
		SplashFrequency: in.SplashFrequency,
	}
}

// behaviorFactor boosts the score for active feeding behaviors and damps it
// for passive ones.
func behaviorFactor(b Behavior) float64 {
	switch b {
	case BehaviorScrambling:
		return 1.15
	case BehaviorGathering:
		return 1.0
	case BehaviorDispersing:
		return 0.85
	default:
		return 1.0
	}
}

// Analyzer tracks consecutive frames and produces streaming feeding
// intensity assessments. It is safe for concurrent use; feed it from a
// dedicated goroutine so texture extraction never blocks the YOLOv8n
// inference pipeline. A frame slice passed to Update must not be mutated
// afterwards.
type Analyzer struct {
	mu sync.Mutex

	prev    Frame
	hasPrev bool

	windowSize        int
	splashWindow      []bool
	splashIdx         int
	splashCount       int
	splashPixelThresh uint8
	splashRatioThresh float64

	energy     float64
	lastBBox   float64
	behavior   Behavior
	lastAssess FeedingIntensity
}

// Option configures an Analyzer.
type Option func(*Analyzer)

// WithWindowSize sets the number of frame pairs in the splash-frequency
// sliding window (default 30).
func WithWindowSize(n int) Option {
	return func(a *Analyzer) {
		if n > 0 {
			a.windowSize = n
		}
	}
}

// WithSplashPixelThreshold sets the per-pixel gray-level change that counts
// a pixel as active for splash detection (default 15).
func WithSplashPixelThreshold(t uint8) Option {
	return func(a *Analyzer) { a.splashPixelThresh = t }
}

// WithSplashRatioThreshold sets the active-pixel ratio required to count a
// frame pair as a splash event (default 0.15).
func WithSplashRatioThreshold(r float64) Option {
	return func(a *Analyzer) {
		if r >= 0 && r <= 1 {
			a.splashRatioThresh = r
		}
	}
}

// NewAnalyzer creates an Analyzer with default thresholds and window size.
func NewAnalyzer(opts ...Option) *Analyzer {
	a := &Analyzer{
		windowSize:        30,
		splashPixelThresh: 15,
		splashRatioThresh: 0.15,
	}
	for _, o := range opts {
		o(a)
	}
	a.splashWindow = make([]bool, a.windowSize)
	return a
}

// Update ingests a new frame and returns the latest assessment fused with
// the most recent YOLOv8n detections. The bool is false until a second frame
// is available (or after a frame-geometry change), since texture energy
// requires a frame pair.
func (a *Analyzer) Update(f Frame) (FeedingIntensity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.hasPrev {
		a.prev = f
		a.hasPrev = true
		return FeedingIntensity{}, false
	}
	if !a.compatible(f) {
		a.prev = f
		return FeedingIntensity{}, false
	}

	a.energy = TextureEnergy(a.prev, f)
	active := ActiveRatio(a.prev, f, a.splashPixelThresh)
	a.prev = f

	if a.splashWindow[a.splashIdx] {
		a.splashCount--
	}
	splash := active >= a.splashRatioThresh
	a.splashWindow[a.splashIdx] = splash
	if splash {
		a.splashCount++
	}
	a.splashIdx = (a.splashIdx + 1) % a.windowSize

	a.lastAssess = Assess(FusionInput{
		SplashFrequency: float64(a.splashCount) / float64(a.windowSize),
		MeanBBoxArea:    a.lastBBox,
		TextureEnergy:   a.energy,
		Behavior:        a.behavior,
	})
	return a.lastAssess, true
}

func (a *Analyzer) compatible(f Frame) bool {
	return f.Width > 0 && f.Height > 0 &&
		len(f.Gray) > 0 &&
		a.prev.Width == f.Width &&
		a.prev.Height == f.Height &&
		len(a.prev.Gray) == len(f.Gray)
}

// SetMeanBBoxArea stores the latest normalized mean fish bbox area [0, 1]
// from the YOLOv8n detector for use in the next assessment.
func (a *Analyzer) SetMeanBBoxArea(v float64) {
	a.mu.Lock()
	a.lastBBox = v
	a.mu.Unlock()
}

// SetBehavior stores the latest YOLOv8n behavior classification.
func (a *Analyzer) SetBehavior(b Behavior) {
	a.mu.Lock()
	a.behavior = b
	a.mu.Unlock()
}

// Energy returns the texture energy of the most recent frame pair.
func (a *Analyzer) Energy() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.energy
}

// SplashFrequency returns the current splash frequency over the window.
func (a *Analyzer) SplashFrequency() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return float64(a.splashCount) / float64(a.windowSize)
}

// Assess returns the most recent assessment (zero value until the first
// successful Update).
func (a *Analyzer) Assess() FeedingIntensity {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastAssess
}

// Reset clears all internal state.
func (a *Analyzer) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prev = Frame{}
	a.hasPrev = false
	a.splashIdx = 0
	a.splashCount = 0
	for i := range a.splashWindow {
		a.splashWindow[i] = false
	}
	a.energy = 0
	a.lastBBox = 0
	a.behavior = BehaviorUnknown
	a.lastAssess = FeedingIntensity{}
}
