package camera

import (
	"image"
	"math"
)

// MotionDetector implements a running-average background subtraction model.
// Each call to Detect updates the background model and returns whether motion
// was detected along with a normalised motion score (0..1).
//
// Algorithm:
//  1. Convert BGRFrame to grayscale.
//  2. Compute per-pixel absolute difference from the background model.
//  3. Count pixels exceeding the threshold.
//  4. Score = (count / totalPixels). Motion if score > motionScoreThreshold.
//  5. Update background model: bg[i] = (1-alpha)*bg[i] + alpha*current[i].
type MotionDetector struct {
	// threshold is the per-pixel absolute difference required to count as
	// changed (0-255).
	threshold float64
	// alpha controls how quickly the background adapts. Small values (0.01)
	// mean the background updates slowly.
	alpha float64
	// scoreThreshold is the minimum fraction of pixels that must exceed the
	// per-pixel threshold before HasMotion is reported.
	scoreThreshold float64
	// lightningThresh suppresses motion when this fraction of pixels change at
	// once (global illumination event — light switch, WLED flash, etc.).
	lightningThresh float64

	// background holds the running-average grayscale background model.
	background []float64
	width      int
	height     int
	total      int
}

// NewMotionDetector creates a MotionDetector.
// threshold: per-pixel intensity difference (0-255).
// alpha: background learning rate (0-1].
// scoreThreshold: minimum changed-pixel fraction to report motion (0-1).
// lightningThresh: fraction above which the frame is treated as a global
// illumination change and motion is suppressed (0-1). 0 disables.
func NewMotionDetector(threshold, alpha, scoreThreshold, lightningThresh float64) *MotionDetector {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.01
	}
	if threshold <= 0 {
		threshold = 25
	}
	if scoreThreshold <= 0 {
		scoreThreshold = 0.015
	}
	return &MotionDetector{
		threshold:       threshold,
		alpha:           alpha,
		scoreThreshold:  scoreThreshold,
		lightningThresh: lightningThresh,
	}
}

// Detect analyses a frame and returns (hasMotion, score). The score is the
// fraction of pixels that differ from the background model by more than the
// threshold. The background model is updated on every call.
//
// If this is the first frame the background is initialised from it and
// (false, 0) is returned.
func (m *MotionDetector) Detect(frame *Frame) (bool, float64) {
	gray := bgrToGray(frame)

	// Initialise background on first frame.
	if m.background == nil {
		m.width = frame.Width
		m.height = frame.Height
		m.total = frame.Width * frame.Height
		m.background = make([]float64, m.total)
		for i, v := range gray.Pix {
			m.background[i] = float64(v)
		}
		return false, 0
	}

	// If frame size changed, reset background.
	if frame.Width != m.width || frame.Height != m.height {
		m.width = frame.Width
		m.height = frame.Height
		m.total = frame.Width * frame.Height
		m.background = make([]float64, m.total)
		for i, v := range gray.Pix {
			m.background[i] = float64(v)
		}
		return false, 0
	}

	// Compute motion.
	changed := 0
	for i, v := range gray.Pix {
		diff := math.Abs(float64(v) - m.background[i])
		if diff > m.threshold {
			changed++
		}
		// Update background model (running average).
		m.background[i] = (1-m.alpha)*m.background[i] + m.alpha*float64(v)
	}

	score := float64(changed) / float64(m.total)

	// Suppress global illumination events (light switch, WLED flash, etc.).
	// When an unusually large fraction of pixels change at once it is almost
	// certainly a lighting change, not real motion.
	if m.lightningThresh > 0 && score >= m.lightningThresh {
		return false, score
	}

	return score >= m.scoreThreshold, score
}

// Reset clears the background model. The next frame will re-initialise it.
func (m *MotionDetector) Reset() {
	m.background = nil
	m.width = 0
	m.height = 0
	m.total = 0
}

// bgrToGray converts a BGR24 frame to an image.Gray using the standard
// luminance weights (BT.601): Y = 0.114*B + 0.587*G + 0.299*R.
func bgrToGray(f *Frame) *image.Gray {
	gray := image.NewGray(image.Rect(0, 0, f.Width, f.Height))
	data := f.Data
	pixLen := f.Width * f.Height

	if len(data) < pixLen*3 {
		// Incomplete frame — return black image.
		return gray
	}

	for i := 0; i < pixLen; i++ {
		b := float64(data[i*3])
		g := float64(data[i*3+1])
		r := float64(data[i*3+2])
		y := 0.114*b + 0.587*g + 0.299*r
		if y > 255 {
			y = 255
		}
		gray.Pix[i] = uint8(y)
	}
	return gray
}
