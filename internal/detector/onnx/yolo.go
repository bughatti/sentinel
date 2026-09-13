//go:build cgo

package onnx

import (
	"image"
	"sort"

	"github.com/bughatti/sentinel/internal/detector"
)

// yoloDet is an intermediate detection used during NMS.
type yoloDet struct {
	cx, cy, w, h float32
	score        float32
	classIdx     int
}

// parseYOLO decodes a YOLOv8/v9 output tensor of shape [1, 84, 8400] into
// Detections. The 84 channels are: [cx, cy, w, h, class0_conf, ..., class79_conf].
// Boxes with max class confidence below confThresh are discarded. NMS is applied
// with iouThresh. (Layout is shared by YOLOv8 and YOLOv9; the prod model is
// yolov8n.onnx.)
func parseYOLO(
	output []float32, // flat [84 * 8400] values, row-major (84 rows, 8400 cols)
	labels []string,
	confThresh float32,
	iouThresh float32,
	origW, origH int,
	inputW, inputH int,
) []detector.Detection {
	numClasses := len(labels)
	if numClasses == 0 {
		numClasses = 80
	}
	// Anchor count derived from the actual output length so any input
	// resolution / model works: output is [4+numClasses, numAnchors] flattened.
	numAnchors := len(output) / (4 + numClasses)
	if numAnchors == 0 {
		return nil
	}

	var raws []yoloDet
	// Tensor layout: output[c*8400 + a] = value for channel c, anchor a.
	for a := 0; a < numAnchors; a++ {
		cx := output[0*numAnchors+a]
		cy := output[1*numAnchors+a]
		w := output[2*numAnchors+a]
		h := output[3*numAnchors+a]

		if w <= 0 || h <= 0 {
			continue
		}

		bestScore := float32(0)
		bestClass := 0
		for c := 0; c < numClasses; c++ {
			if idx := (4+c)*numAnchors + a; idx < len(output) {
				if s := output[idx]; s > bestScore {
					bestScore = s
					bestClass = c
				}
			}
		}
		if bestScore < confThresh {
			continue
		}
		raws = append(raws, yoloDet{cx, cy, w, h, bestScore, bestClass})
	}

	if len(raws) == 0 {
		return nil
	}

	// Convert cx,cy,w,h (model-space 0..modelInputW/H) to pixel rect.
	toRect := func(d yoloDet) image.Rectangle {
		x1 := int(d.cx - d.w/2)
		y1 := int(d.cy - d.h/2)
		x2 := int(d.cx + d.w/2)
		y2 := int(d.cy + d.h/2)
		return image.Rect(x1, y1, x2, y2)
	}

	// Group by class and run NMS per class.
	byClass := make(map[int][]yoloDet)
	for _, r := range raws {
		byClass[r.classIdx] = append(byClass[r.classIdx], r)
	}

	var out []detector.Detection
	for classIdx, group := range byClass {
		// Sort descending by score.
		sort.Slice(group, func(i, j int) bool {
			return group[i].score > group[j].score
		})
		kept := nms(group, iouThresh, toRect)
		label := "unknown"
		if classIdx < len(labels) {
			label = labels[classIdx]
		}
		for _, k := range kept {
			scaledBox := scaleBox(toRect(k), origW, origH, inputW, inputH)
			out = append(out, detector.Detection{
				Label: label,
				Score: k.score,
				Box:   scaledBox,
			})
		}
	}
	return out
}

// nms applies greedy non-maximum suppression.
func nms(dets []yoloDet, iouThresh float32, toRect func(yoloDet) image.Rectangle) []yoloDet {
	suppressed := make([]bool, len(dets))
	var kept []yoloDet
	for i := 0; i < len(dets); i++ {
		if suppressed[i] {
			continue
		}
		kept = append(kept, dets[i])
		ri := toRect(dets[i])
		for j := i + 1; j < len(dets); j++ {
			if suppressed[j] {
				continue
			}
			if iou(ri, toRect(dets[j])) > iouThresh {
				suppressed[j] = true
			}
		}
	}
	return kept
}

// iou computes the intersection-over-union of two pixel rectangles.
func iou(a, b image.Rectangle) float32 {
	inter := a.Intersect(b)
	if inter.Empty() {
		return 0
	}
	areaA := float32(a.Dx() * a.Dy())
	areaB := float32(b.Dx() * b.Dy())
	areaI := float32(inter.Dx() * inter.Dy())
	union := areaA + areaB - areaI
	if union <= 0 {
		return 0
	}
	return areaI / union
}
