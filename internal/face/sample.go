package face

import (
	"image"

	"github.com/bughatti/sentinel/internal/camera"
)

// frameSampler returns a bilinear RGB sampler over a BGR24 camera frame.
func frameSampler(f *camera.Frame) rgbSampler {
	w, h := f.Width, f.Height
	at := func(x, y int) (float32, float32, float32) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return 0, 0, 0
		}
		i := (y*w + x) * 3
		if i+2 >= len(f.Data) {
			return 0, 0, 0
		}
		return float32(f.Data[i+2]), float32(f.Data[i+1]), float32(f.Data[i]) // R,G,B from B,G,R
	}
	return bilinear(at, w, h)
}

// imageSampler returns a bilinear RGB sampler over an image.Image.
func imageSampler(img image.Image) rgbSampler {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	at := func(x, y int) (float32, float32, float32) {
		if x < 0 || y < 0 || x >= w || y >= h {
			return 0, 0, 0
		}
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return float32(r >> 8), float32(g >> 8), float32(bl >> 8)
	}
	return bilinear(at, w, h)
}

func bilinear(at func(x, y int) (float32, float32, float32), w, h int) rgbSampler {
	return func(fx, fy float64) (float32, float32, float32) {
		if fx < 0 || fy < 0 || fx > float64(w-1) || fy > float64(h-1) {
			return 0, 0, 0
		}
		x0 := int(fx)
		y0 := int(fy)
		x1 := x0 + 1
		y1 := y0 + 1
		dx := float32(fx - float64(x0))
		dy := float32(fy - float64(y0))
		r00, g00, b00 := at(x0, y0)
		r10, g10, b10 := at(x1, y0)
		r01, g01, b01 := at(x0, y1)
		r11, g11, b11 := at(x1, y1)
		lerp := func(a, b, t float32) float32 { return a + (b-a)*t }
		r := lerp(lerp(r00, r10, dx), lerp(r01, r11, dx), dy)
		g := lerp(lerp(g00, g10, dx), lerp(g01, g11, dx), dy)
		bb := lerp(lerp(b00, b10, dx), lerp(b01, b11, dx), dy)
		return r, g, bb
	}
}

// fillSCRFDInput letterboxes the source (top-left aligned) into a 640x640 NCHW
// float32 tensor with insightface SCRFD normalisation ((v-127.5)/128, RGB).
// Returns the uniform scale applied (detections must be divided by it).
func fillSCRFDInput(sample rgbSampler, srcW, srcH int, out []float32) float32 {
	scale := float32(scrfdInput) / float32(srcW)
	if s2 := float32(scrfdInput) / float32(srcH); s2 < scale {
		scale = s2
	}
	newW := int(float32(srcW) * scale)
	newH := int(float32(srcH) * scale)
	plane := scrfdInput * scrfdInput
	for v := 0; v < scrfdInput; v++ {
		for u := 0; u < scrfdInput; u++ {
			idx := v*scrfdInput + u
			var r, g, b float32
			if u < newW && v < newH {
				r, g, b = sample(float64(u)/float64(scale), float64(v)/float64(scale))
			}
			out[idx] = (r - 127.5) / 128.0
			out[plane+idx] = (g - 127.5) / 128.0
			out[2*plane+idx] = (b - 127.5) / 128.0
		}
	}
	return scale
}
