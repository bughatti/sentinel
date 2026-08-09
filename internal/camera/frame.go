// Package camera handles per-camera goroutine trees: RTSP capture, motion
// detection, and coordination with the detector pipeline.
package camera

import "time"

// Frame carries one video frame from an RTSP capture subprocess to the
// detection pipeline. Data contains raw BGR24 bytes at the detect resolution.
type Frame struct {
	// CameraID is the name key from the config cameras map.
	CameraID string

	// Timestamp is the wall-clock time when the frame was captured.
	Timestamp time.Time

	// Data holds raw BGR24 pixel bytes: len == Width * Height * 3.
	Data []byte

	// Width and Height are the pixel dimensions of Data.
	Width  int
	Height int

	// SeqNum is a monotonically increasing frame counter per camera, used
	// for stationary-object interval logic.
	SeqNum uint64
}

// Clone returns a deep copy of the frame with an independent Data slice.
// Use this when a frame must outlive the capture buffer.
func (f *Frame) Clone() *Frame {
	data := make([]byte, len(f.Data))
	copy(data, f.Data)
	return &Frame{
		CameraID:  f.CameraID,
		Timestamp: f.Timestamp,
		Data:      data,
		Width:     f.Width,
		Height:    f.Height,
		SeqNum:    f.SeqNum,
	}
}

// FrameSize returns the expected byte length for the given dimensions.
func FrameSize(width, height int) int {
	return width * height * 3
}
