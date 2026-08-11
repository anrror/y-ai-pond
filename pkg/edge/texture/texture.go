// Package texture implements water surface texture extraction and feeding
// intensity assessment for the edge controller. Texture energy
// E = Sum((dI)^2) / area is derived from consecutive grayscale frames using
// a simplified frame-difference scheme, requiring no GPU and no external
// computer-vision dependencies. The energy is fused with YOLOv8n behavior
// output (splash_frequency + mean_bbox_area + texture_energy, YOLO11-PEGA
// style) into a feeding intensity score S_t in [0, 1] with a four-level
// classification (none/weak/medium/strong).
//
// The package is CPU friendly and safe for concurrent use. Callers should
// feed frames from a dedicated goroutine so texture extraction never blocks
// the YOLOv8n inference pipeline.
package texture

// Frame is a single grayscale video frame (row-major, one byte per pixel).
type Frame struct {
	Gray   []uint8
	Width  int
	Height int
}

// TextureEnergy computes E = Sum((dI)^2) / area between two consecutive
// frames, where dI is the normalized per-pixel intensity difference
// |curr - prev| / 255. E is in [0, 1]: 0 for a perfectly static surface,
// rising with water agitation such as splashes and feeding ripples.
// Mismatched or empty frames yield 0.
func TextureEnergy(prev, curr Frame) float64 {
	n := len(prev.Gray)
	if n == 0 || len(curr.Gray) != n || prev.Width != curr.Width || prev.Height != curr.Height {
		return 0
	}
	var sumSq float64
	for i := 0; i < n; i++ {
		d := grayDiff(prev.Gray[i], curr.Gray[i])
		sumSq += d * d
	}
	return sumSq / float64(n)
}

// ActiveRatio returns the fraction of pixels whose intensity changed by more
// than pixelThreshold gray levels between two frames. It is used to detect
// splash events: a high active ratio indicates feeding agitation.
func ActiveRatio(prev, curr Frame, pixelThreshold uint8) float64 {
	n := len(prev.Gray)
	if n == 0 || len(curr.Gray) != n || prev.Width != curr.Width || prev.Height != curr.Height {
		return 0
	}
	active := 0
	for i := 0; i < n; i++ {
		if absDiff(prev.Gray[i], curr.Gray[i]) > pixelThreshold {
			active++
		}
	}
	return float64(active) / float64(n)
}

// grayDiff returns the absolute normalized intensity difference |a-b| / 255.
func grayDiff(a, b uint8) float64 {
	if a > b {
		return float64(a-b) / 255.0
	}
	return float64(b-a) / 255.0
}

// absDiff returns the absolute intensity difference |a-b|.
func absDiff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// Feeding intensity level thresholds.
const (
	LevelNoneMax   = 0.1
	LevelWeakMax   = 0.3
	LevelMediumMax = 0.6
)

// Feeding intensity level labels.
const (
	LevelNone   = "none"
	LevelWeak   = "weak"
	LevelMedium = "medium"
	LevelStrong = "strong"
)

// Level classifies an intensity value into the four-level scale:
//
//	x < 0.1 -> none
//	x < 0.3 -> weak
//	x < 0.6 -> medium
//	x >= 0.6 -> strong
//
// Values outside [0, 1] are treated as the nearest valid intensity.
func Level(x float64) string {
	if x < LevelNoneMax {
		return LevelNone
	}
	if x < LevelWeakMax {
		return LevelWeak
	}
	if x < LevelMediumMax {
		return LevelMedium
	}
	return LevelStrong
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
