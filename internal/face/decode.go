package face

import (
	"image"
	"sort"
)

// scrfdInput is the fixed SCRFD input size (multiple of 32). buffalo_l det_10g
// was exported for dynamic input; 640 is the standard inference size.
const scrfdInput = 640

// scrfd strides and their anchor-per-cell count (num_anchors=2 for det_10g).
var scrfdStrides = []int{8, 16, 32}

const scrfdNumAnchors = 2

// rawFace is a decoded detection in SCRFD input-space (640x640) coordinates.
type rawFace struct {
	box   [4]float32    // x1,y1,x2,y2
	kps   [5][2]float32 // 5 landmarks (x,y): left-eye, right-eye, nose, left-mouth, right-mouth
	score float32
}

// decodeSCRFD decodes the 9 SCRFD output tensors into faces (in 640-space),
// applying the score threshold and NMS. scores/bboxes/kpss are indexed by
// stride order (8, 16, 32); each scores[i] is [N,1] flattened, bboxes[i] [N,4],
// kpss[i] [N,10].
func decodeSCRFD(scores, bboxes, kpss [][]float32, scoreThresh, nmsThresh float32) []rawFace {
	var faces []rawFace
	for si, stride := range scrfdStrides {
		sc := scores[si]
		bb := bboxes[si]
		kp := kpss[si]
		feat := scrfdInput / stride // 80,40,20
		i := 0
		for y := 0; y < feat; y++ {
			for x := 0; x < feat; x++ {
				for a := 0; a < scrfdNumAnchors; a++ {
					if i >= len(sc) {
						break
					}
					s := sc[i]
					if s >= scoreThresh {
						cx := float32(x * stride)
						cy := float32(y * stride)
						fs := float32(stride)
						// distance2bbox: preds are [left,top,right,bottom] * stride
						x1 := cx - bb[i*4+0]*fs
						y1 := cy - bb[i*4+1]*fs
						x2 := cx + bb[i*4+2]*fs
						y2 := cy + bb[i*4+3]*fs
						var f rawFace
						f.box = [4]float32{x1, y1, x2, y2}
						f.score = s
						for k := 0; k < 5; k++ {
							f.kps[k][0] = cx + kp[i*10+k*2+0]*fs
							f.kps[k][1] = cy + kp[i*10+k*2+1]*fs
						}
						faces = append(faces, f)
					}
					i++
				}
			}
		}
	}
	return nmsFaces(faces, nmsThresh)
}

// nmsFaces applies greedy non-max suppression by score.
func nmsFaces(faces []rawFace, iouThresh float32) []rawFace {
	sort.Slice(faces, func(a, b int) bool { return faces[a].score > faces[b].score })
	var keep []rawFace
	suppressed := make([]bool, len(faces))
	for i := range faces {
		if suppressed[i] {
			continue
		}
		keep = append(keep, faces[i])
		for j := i + 1; j < len(faces); j++ {
			if suppressed[j] {
				continue
			}
			if faceIoU(faces[i].box, faces[j].box) > iouThresh {
				suppressed[j] = true
			}
		}
	}
	return keep
}

func faceIoU(a, b [4]float32) float32 {
	ix1 := maxf(a[0], b[0])
	iy1 := maxf(a[1], b[1])
	ix2 := minf(a[2], b[2])
	iy2 := minf(a[3], b[3])
	iw := ix2 - ix1
	ih := iy2 - iy1
	if iw <= 0 || ih <= 0 {
		return 0
	}
	inter := iw * ih
	areaA := (a[2] - a[0]) * (a[3] - a[1])
	areaB := (b[2] - b[0]) * (b[3] - b[1])
	return inter / (areaA + areaB - inter)
}

func maxf(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
func minf(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

// scaleRawFace converts a rawFace from SCRFD input-space (with letterbox scale)
// back to original image pixel coordinates. scale = det_size / max(origW,origH),
// applied uniformly with no padding offset (top-left aligned letterbox).
func scaleRawFace(f rawFace, scale float32) (image.Rectangle, [5][2]float32) {
	box := image.Rect(
		int(f.box[0]/scale), int(f.box[1]/scale),
		int(f.box[2]/scale), int(f.box[3]/scale),
	)
	var kps [5][2]float32
	for k := 0; k < 5; k++ {
		kps[k][0] = f.kps[k][0] / scale
		kps[k][1] = f.kps[k][1] / scale
	}
	return box, kps
}
