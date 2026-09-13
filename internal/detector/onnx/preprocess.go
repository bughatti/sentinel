//go:build cgo

package onnx

import (
	"fmt"
	"image"
	"image/color"

	"github.com/bughatti/sentinel/internal/camera"
	"golang.org/x/image/draw"
)

// preprocess converts a camera.Frame (BGR24, arbitrary resolution) into a
// flat float32 slice in NCHW RGB format normalised to [0, 1], sized for a
// single model input of [1, 3, inputH, inputW].
func preprocess(frame *camera.Frame, inputW, inputH int) ([]float32, error) {
	if len(frame.Data) < frame.Width*frame.Height*3 {
		return nil, fmt.Errorf("onnx preprocess: frame data too short (%d bytes for %dx%d)",
			len(frame.Data), frame.Width, frame.Height)
	}

	// Fast path: the frame is already at the model input size (ffmpeg's
	// scale_cuda resized it on the GPU), so skip the NRGBA build + CPU bilinear
	// resize entirely and convert BGR24 → NCHW RGB [0,1] in one pass. This is
	// what keeps a high-res model fed without the CPU starving the GPU.
	if frame.Width == inputW && frame.Height == inputH {
		total := inputW * inputH
		tensor := make([]float32, 3*total)
		d := frame.Data
		for i := 0; i < total; i++ {
			tensor[i] = float32(d[i*3+2]) / 255.0       // R (from BGR)
			tensor[total+i] = float32(d[i*3+1]) / 255.0 // G
			tensor[2*total+i] = float32(d[i*3]) / 255.0 // B
		}
		return tensor, nil
	}

	// Build an image.NRGBA from the BGR24 data.
	src := image.NewNRGBA(image.Rect(0, 0, frame.Width, frame.Height))
	pixLen := frame.Width * frame.Height
	for i := 0; i < pixLen; i++ {
		b := frame.Data[i*3]
		g := frame.Data[i*3+1]
		r := frame.Data[i*3+2]
		src.Pix[i*4] = r
		src.Pix[i*4+1] = g
		src.Pix[i*4+2] = b
		src.Pix[i*4+3] = 0xFF
	}

	// Resize to model input size using bilinear interpolation.
	dst := image.NewNRGBA(image.Rect(0, 0, inputW, inputH))
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)

	// Convert to NCHW float32 RGB [0, 1].
	// Layout: [C, H, W] flattened → R-plane, G-plane, B-plane.
	total := inputH * inputW
	tensor := make([]float32, 3*total)
	for y := 0; y < inputH; y++ {
		for x := 0; x < inputW; x++ {
			c := dst.NRGBAAt(x, y)
			idx := y*inputW + x
			tensor[idx] = float32(c.R) / 255.0
			tensor[total+idx] = float32(c.G) / 255.0
			tensor[2*total+idx] = float32(c.B) / 255.0
		}
	}
	return tensor, nil
}

// scaleBox scales a bounding box from model-input coordinates back to the
// original frame coordinates.
func scaleBox(box image.Rectangle, origW, origH, inputW, inputH int) image.Rectangle {
	if inputW == 0 || inputH == 0 {
		return box
	}
	scaleX := float64(origW) / float64(inputW)
	scaleY := float64(origH) / float64(inputH)
	return image.Rect(
		int(float64(box.Min.X)*scaleX),
		int(float64(box.Min.Y)*scaleY),
		int(float64(box.Max.X)*scaleX),
		int(float64(box.Max.Y)*scaleY),
	)
}

// letterboxImage resizes src to fit within (targetW, targetH) while
// preserving aspect ratio, padding with grey. Returns the padded image and
// (padX, padY) pixel offsets so boxes can be un-padded. Used by callers that
// need letter-boxed input for accurate box coordinates.
func letterboxImage(src *image.NRGBA, targetW, targetH int) (*image.NRGBA, int, int) {
	srcW := src.Bounds().Dx()
	srcH := src.Bounds().Dy()

	scale := float64(targetW) / float64(srcW)
	if sh := float64(targetH) / float64(srcH); sh < scale {
		scale = sh
	}

	newW := int(float64(srcW) * scale)
	newH := int(float64(srcH) * scale)
	padX := (targetW - newW) / 2
	padY := (targetH - newH) / 2

	// Fill grey background.
	dst := image.NewNRGBA(image.Rect(0, 0, targetW, targetH))
	grey := color.NRGBA{R: 114, G: 114, B: 114, A: 255}
	for i := 0; i < len(dst.Pix); i += 4 {
		dst.Pix[i] = grey.R
		dst.Pix[i+1] = grey.G
		dst.Pix[i+2] = grey.B
		dst.Pix[i+3] = grey.A
	}

	resized := image.NewNRGBA(image.Rect(0, 0, newW, newH))
	draw.BiLinear.Scale(resized, resized.Bounds(), src, src.Bounds(), draw.Src, nil)

	// Blit resized into dst at (padX, padY).
	for y := 0; y < newH; y++ {
		for x := 0; x < newW; x++ {
			dst.Set(x+padX, y+padY, resized.At(x, y))
		}
	}
	return dst, padX, padY
}
