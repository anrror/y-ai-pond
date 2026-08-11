package texture

import (
	"math"
	"math/rand"
	"testing"
)

func newFrame(w, h int, val uint8) Frame {
	gray := make([]uint8, w*h)
	for i := range gray {
		gray[i] = val
	}
	return Frame{Gray: gray, Width: w, Height: h}
}

func shiftFrame(w, h int, base uint8, shift int) Frame {
	f := newFrame(w, h, base)
	for i := range f.Gray {
		v := int(f.Gray[i]) + shift
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		f.Gray[i] = uint8(v)
	}
	return f
}

// rippleFrame returns a frame where a fraction ratio of pixels is shifted by
// +-amp gray levels, simulating water agitation. Deterministic per seed.
func rippleFrame(w, h int, base, amp uint8, ratio float64, seed int64) Frame {
	rng := rand.New(rand.NewSource(seed))
	f := newFrame(w, h, base)
	for i := range f.Gray {
		if rng.Float64() < ratio {
			d := int(amp)
			if rng.Intn(2) == 0 {
				d = -d
			}
			if v := int(f.Gray[i]) + d; v < 0 {
				f.Gray[i] = 0
			} else if v > 255 {
				f.Gray[i] = 255
			} else {
				f.Gray[i] = uint8(v)
			}
		}
	}
	return f
}

// TestTextureEnergyComputation verifies E = Sum((dI)^2) / area on simulated
// frames: static water yields ~0 energy, feeding agitation yields high energy.
func TestTextureEnergyComputation(t *testing.T) {
	const w, h = 64, 64
	const base = uint8(128)

	// Identical frames -> perfectly static surface -> E = 0.
	if e := TextureEnergy(newFrame(w, h, base), newFrame(w, h, base)); e != 0 {
		t.Errorf("static energy = %v, want 0", e)
	}

	// Uniform +60 gray-level shift -> E = (60/255)^2 exactly.
	eShift := TextureEnergy(newFrame(w, h, base), shiftFrame(w, h, base, 60))
	if want := (60.0 / 255.0) * (60.0 / 255.0); math.Abs(eShift-want) > 1e-9 {
		t.Errorf("uniform shift energy = %v, want %v", eShift, want)
	}

	// Camera-like noise (+-2 on ~5% of pixels) -> near zero.
	eNoise := TextureEnergy(
		rippleFrame(w, h, base, 2, 0.05, 1),
		rippleFrame(w, h, base, 2, 0.05, 2),
	)
	if eNoise >= 0.001 {
		t.Errorf("noise energy = %v, want < 0.001", eNoise)
	}

	// Active feeding surface (80% of pixels shifted by +-100) -> high energy.
	eActive := TextureEnergy(newFrame(w, h, base), rippleFrame(w, h, base, 100, 0.8, 42))
	if eActive < 0.05 || eActive > 0.2 {
		t.Errorf("active energy = %v, want in [0.05, 0.2]", eActive)
	}
	if eActive < eNoise*100 {
		t.Errorf("active energy %v must be far above noise %v", eActive, eNoise)
	}

	// Dimension mismatch and empty frames -> 0.
	if e := TextureEnergy(newFrame(w, h, base), newFrame(w, h+1, base)); e != 0 {
		t.Errorf("mismatched dims energy = %v, want 0", e)
	}
	if e := TextureEnergy(Frame{}, Frame{}); e != 0 {
		t.Errorf("empty frames energy = %v, want 0", e)
	}
}

func TestActiveRatio(t *testing.T) {
	const w, h = 32, 32
	if r := ActiveRatio(newFrame(w, h, 100), newFrame(w, h, 100), 15); r != 0 {
		t.Errorf("static active ratio = %v, want 0", r)
	}
	if r := ActiveRatio(newFrame(w, h, 100), shiftFrame(w, h, 100, 60), 15); r != 1 {
		t.Errorf("uniform shift active ratio = %v, want 1", r)
	}
	if r := ActiveRatio(newFrame(w, h, 100), shiftFrame(w, h, 100, 5), 15); r != 0 {
		t.Errorf("small shift active ratio = %v, want 0", r)
	}
}

// TestIntensityLevelThresholds verifies the four-level classification
// boundaries: <0.1 none, 0.1-0.3 weak, 0.3-0.6 medium, >=0.6 strong.
func TestIntensityLevelThresholds(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{0.0, LevelNone},
		{LevelNoneMax - 0.001, LevelNone},
		{LevelNoneMax, LevelWeak},
		{0.2, LevelWeak},
		{LevelWeakMax - 0.001, LevelWeak},
		{LevelWeakMax, LevelMedium},
		{0.45, LevelMedium},
		{LevelMediumMax - 0.001, LevelMedium},
		{LevelMediumMax, LevelStrong},
		{0.9, LevelStrong},
		{1.0, LevelStrong},
		{1.5, LevelStrong},
		{-1.0, LevelNone},
	}
	for _, c := range cases {
		if got := Level(c.x); got != c.want {
			t.Errorf("Level(%v) = %q, want %q", c.x, got, c.want)
		}
	}
}

func TestFusionScore(t *testing.T) {
	// (0.5, 0.5, 0.5) with dispersing behavior:
	// (0.4*0.5 + 0.2*0.5 + 0.4*0.5) * 0.85 = 0.425.
	in := FusionInput{SplashFrequency: 0.5, MeanBBoxArea: 0.5, TextureEnergy: 0.5, Behavior: BehaviorDispersing}
	if want := 0.425; math.Abs(in.Score()-want) > 1e-9 {
		t.Errorf("dispersing Score() = %v, want %v", in.Score(), want)
	}

	// (0.5, 0.5, 0.5) gathering -> 0.5.
	in.Behavior = BehaviorGathering
	if want := 0.5; math.Abs(in.Score()-want) > 1e-9 {
		t.Errorf("gathering Score() = %v, want %v", in.Score(), want)
	}

	// Out-of-range features are clamped into [0, 1].
	in = FusionInput{SplashFrequency: 2, MeanBBoxArea: -1, TextureEnergy: 0.5}
	if s := in.Score(); s < 0 || s > 1 {
		t.Errorf("Score() = %v, want within [0, 1]", s)
	}
}

func TestAssessLevels(t *testing.T) {
	// Zero features -> score 0 -> none.
	fi := Assess(FusionInput{})
	if fi.Score != 0 || fi.Level != LevelNone {
		t.Errorf("zero input: score=%v level=%q, want 0/none", fi.Score, fi.Level)
	}

	// Full features with scrambling behavior -> clamped to 1 -> strong.
	fi = Assess(FusionInput{SplashFrequency: 1, MeanBBoxArea: 1, TextureEnergy: 1, Behavior: BehaviorScrambling})
	if fi.Score != 1 || fi.Level != LevelStrong {
		t.Errorf("full input: score=%v level=%q, want 1/strong", fi.Score, fi.Level)
	}

	// The result carries the raw features through.
	fi = Assess(FusionInput{SplashFrequency: 0.4, TextureEnergy: 0.55, Behavior: BehaviorGathering})
	if fi.TextureEnergy != 0.55 || fi.SplashFrequency != 0.4 {
		t.Errorf("assess feature passthrough mismatch: %+v", fi)
	}
}

func TestAnalyzerBasic(t *testing.T) {
	const w, h = 32, 32
	a := NewAnalyzer(WithWindowSize(5))

	if _, ok := a.Update(newFrame(w, h, 100)); ok {
		t.Error("first frame should not yield an assessment")
	}
	fi, ok := a.Update(newFrame(w, h, 100))
	if !ok {
		t.Fatal("second frame should yield an assessment")
	}
	if fi.TextureEnergy != 0 || fi.SplashFrequency != 0 {
		t.Errorf("static sequence: energy=%v splash=%v, want 0/0", fi.TextureEnergy, fi.SplashFrequency)
	}
	if fi.Level != LevelNone {
		t.Errorf("static sequence level = %q, want none", fi.Level)
	}
	if a.Energy() != 0 || a.SplashFrequency() != 0 {
		t.Error("accessor mismatch")
	}
}

func TestAnalyzerFrameGeometryChange(t *testing.T) {
	a := NewAnalyzer(WithWindowSize(5))
	a.Update(newFrame(32, 32, 100))
	if _, ok := a.Update(newFrame(32, 32, 100)); !ok {
		t.Fatal("expected assessment before geometry change")
	}
	if _, ok := a.Update(newFrame(64, 64, 100)); ok {
		t.Error("expected no assessment on frame-geometry change")
	}
	if _, ok := a.Update(newFrame(64, 64, 100)); !ok {
		t.Error("expected assessment after geometry re-sync")
	}
}

// TestFeedingIntensityConvergence feeds a simulated frame sequence through
// the Analyzer and verifies the intensity converges from none through weak
// and medium to strong as feeding activity increases, and the score
// stabilizes once the splash window saturates.
func TestFeedingIntensityConvergence(t *testing.T) {
	const w, h = 64, 64
	const base = uint8(128)

	a := NewAnalyzer(WithWindowSize(10))
	var fi FeedingIntensity

	// Phase 1: static water surface -> none.
	for i := 0; i < 5; i++ {
		var ok bool
		fi, ok = a.Update(newFrame(w, h, base))
		if i == 0 && ok {
			t.Fatal("first frame should not yield an assessment")
		}
	}
	if fi.Score != 0 || fi.Level != LevelNone {
		t.Fatalf("phase 1: score=%v level=%q, want 0/none", fi.Score, fi.Level)
	}

	// Phase 2: mild ripples with gathering behavior -> weak, then medium as
	// the splash window fills up.
	a.SetMeanBBoxArea(0.5)
	a.SetBehavior(BehaviorGathering)

	first, ok := a.Update(rippleFrame(w, h, base, 60, 0.25, 100))
	if !ok || first.Level != LevelWeak {
		t.Fatalf("phase 2 first: ok=%v level=%q, want true/weak", ok, first.Level)
	}
	for i := 1; i < 5; i++ {
		fi, _ = a.Update(rippleFrame(w, h, base, 60, 0.25, int64(100+i)))
	}
	if fi.Level != LevelMedium {
		t.Errorf("phase 2 end: level=%q, want medium", fi.Level)
	}

	// Phase 3: violent feeding with scrambling behavior -> strong, and the
	// score converges once the window is saturated with splash events.
	a.SetMeanBBoxArea(0.9)
	a.SetBehavior(BehaviorScrambling)

	frameA := rippleFrame(w, h, base, 100, 0.8, 9001)
	frameB := rippleFrame(w, h, base, 100, 0.8, 9002)
	prev := fi
	for i := 0; i < 15; i++ {
		prev = fi
		if i%2 == 0 {
			fi, _ = a.Update(frameA)
		} else {
			fi, _ = a.Update(frameB)
		}
	}

	if fi.Level != LevelStrong {
		t.Errorf("phase 3 level = %q, want strong", fi.Level)
	}
	if fi.SplashFrequency != 1.0 {
		t.Errorf("phase 3 splash frequency = %v, want 1.0", fi.SplashFrequency)
	}
	wantScore := 1.15 * clamp01(0.4*1.0+0.2*0.9+0.4*TextureEnergy(frameA, frameB))
	if math.Abs(fi.Score-wantScore) > 1e-9 {
		t.Errorf("phase 3 score = %v, want %v", fi.Score, wantScore)
	}
	if fi.Score < 0.6 {
		t.Errorf("phase 3 score %v must be >= 0.6 for strong", fi.Score)
	}
	if fi.Score != prev.Score {
		t.Errorf("score not converged: last=%v prev=%v", fi.Score, prev.Score)
	}
	if fi.Score <= first.Score {
		t.Errorf("score must grow across phases: first=%v last=%v", first.Score, fi.Score)
	}
}

func BenchmarkTextureEnergy(b *testing.B) {
	const w, h = 640, 640
	prev := rippleFrame(w, h, 128, 100, 0.8, 1)
	curr := rippleFrame(w, h, 128, 100, 0.8, 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = TextureEnergy(prev, curr)
	}
}

func BenchmarkAnalyzerUpdate(b *testing.B) {
	const w, h = 640, 640
	a := NewAnalyzer()
	prev := rippleFrame(w, h, 128, 100, 0.8, 1)
	curr := rippleFrame(w, h, 128, 100, 0.8, 2)
	a.Update(prev)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Update(curr)
	}
}
