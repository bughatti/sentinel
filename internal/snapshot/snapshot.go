// Package snapshot saves JPEG snapshot images for detected events.
package snapshot

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/sentinel-nvr/sentinel/internal/camera"
	"github.com/sentinel-nvr/sentinel/internal/events"
	"github.com/sentinel-nvr/sentinel/internal/storage"
)

// Saver saves JPEG snapshots to disk and updates the events table.
type Saver struct {
	storage *storage.LocalStorage
	store   *events.Store
	quality int
	drawBox bool
}

// NewSaver creates a Saver. quality is JPEG quality [1,100]; drawBox enables
// bounding-box overlays on the saved image.
func NewSaver(s *storage.LocalStorage, store *events.Store, quality int, drawBox bool) *Saver {
	if quality <= 0 || quality > 100 {
		quality = 70
	}
	return &Saver{
		storage: s,
		store:   store,
		quality: quality,
		drawBox: drawBox,
	}
}

// Save encodes the frame as JPEG and writes it to the snapshot directory.
// It then updates the event's has_snapshot flag in the database.
func (sv *Saver) Save(ctx context.Context, e *events.Event, frame *camera.Frame) error {
	img, err := bgrToNRGBA(frame)
	if err != nil {
		return fmt.Errorf("snapshot: convert frame: %w", err)
	}

	if sv.drawBox {
		drawBoundingBox(img, e, frame.Width, frame.Height)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: sv.quality}); err != nil {
		return fmt.Errorf("snapshot: encode jpeg: %w", err)
	}

	path := sv.storage.SnapshotPath(e.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("snapshot: mkdir: %w", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("snapshot: write %s: %w", path, err)
	}

	e.HasSnapshot = true
	if err := sv.store.UpdateEvent(ctx, e); err != nil {
		slog.Warn("snapshot: update event has_snapshot", "event", e.ID, "err", err)
	}

	slog.Debug("snapshot saved", "event", e.ID, "path", path, "size_kb", buf.Len()/1024)
	return nil
}

// bgrToNRGBA converts a BGR24 camera.Frame to an *image.NRGBA.
func bgrToNRGBA(frame *camera.Frame) (*image.NRGBA, error) {
	pixLen := frame.Width * frame.Height
	if len(frame.Data) < pixLen*3 {
		return nil, fmt.Errorf("frame data too short: %d < %d", len(frame.Data), pixLen*3)
	}
	img := image.NewNRGBA(image.Rect(0, 0, frame.Width, frame.Height))
	for i := 0; i < pixLen; i++ {
		b := frame.Data[i*3]
		g := frame.Data[i*3+1]
		r := frame.Data[i*3+2]
		img.Pix[i*4] = r
		img.Pix[i*4+1] = g
		img.Pix[i*4+2] = b
		img.Pix[i*4+3] = 0xFF
	}
	return img, nil
}

// drawBoundingBox draws a coloured rectangle on img corresponding to the
// event's normalised bounding box.
func drawBoundingBox(img *image.NRGBA, e *events.Event, w, h int) {
	x1 := int(e.Box.X1 * float32(w))
	y1 := int(e.Box.Y1 * float32(h))
	x2 := int(e.Box.X2 * float32(w))
	y2 := int(e.Box.Y2 * float32(h))

	// Clamp to image bounds.
	if x1 < 0 {
		x1 = 0
	}
	if y1 < 0 {
		y1 = 0
	}
	if x2 >= w {
		x2 = w - 1
	}
	if y2 >= h {
		y2 = h - 1
	}

	c := boxColor(e.Label)
	drawHLine(img, x1, x2, y1, c)
	drawHLine(img, x1, x2, y2, c)
	drawVLine(img, y1, y2, x1, c)
	drawVLine(img, y1, y2, x2, c)
}

func drawHLine(img *image.NRGBA, x1, x2, y int, c color.NRGBA) {
	for x := x1; x <= x2; x++ {
		img.SetNRGBA(x, y, c)
	}
}

func drawVLine(img *image.NRGBA, y1, y2, x int, c color.NRGBA) {
	for y := y1; y <= y2; y++ {
		img.SetNRGBA(x, y, c)
	}
}

// boxColor returns a colour for common object labels.
func boxColor(label string) color.NRGBA {
	switch label {
	case "person":
		return color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	case "car":
		return color.NRGBA{R: 0, G: 255, B: 0, A: 255}
	case "dog", "cat":
		return color.NRGBA{R: 0, G: 0, B: 255, A: 255}
	default:
		return color.NRGBA{R: 255, G: 255, B: 0, A: 255}
	}
}
