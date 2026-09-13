package camera

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bughatti/sentinel/internal/config"
)

// BatchItem is the unit sent from a CameraWorker to the shared fanin channel.
// The detector fills ResultCh and the pipeline reads from it.
type BatchItem struct {
	Frame    Frame
	ResultCh chan DetectionResult
}

// DetectionResult carries detector output back to the pipeline goroutine.
type DetectionResult struct {
	// Populated by the detector; left nil if not yet detected.
	Detections interface{} // []detector.Detection - typed in pipeline to avoid cycle
}

// MotionSignal is emitted by motionLoop for every processed frame.
type MotionSignal struct {
	HasMotion bool
	Score     float64
	At        time.Time
}

// CameraWorker owns the goroutine tree for one camera. It runs:
//   - captureLoop: RTSP → raw frames
//   - motionLoop: frames → motion-filtered frames → fanin
//   - recordLoop: RTSP → segment files → recordNotifyCh
type CameraWorker struct {
	name          string
	cfg           config.CameraConfig
	fanin         chan<- BatchItem
	recordingsDir string

	// record notifications for the recorder.Manager
	RecordNotifyCh chan string

	// MotionCh receives one signal per processed frame; used by motion.Manager.
	MotionCh chan MotionSignal

	mu         sync.Mutex
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	running    bool

	// latest JPEG preview frame from the capture stream
	frameMu    sync.RWMutex
	latestJPEG []byte

	// runtime counters (atomic) for /api/stats + online status
	capturedN     int64 // frames pulled from the capture stream
	detectN       int64 // frames forwarded to the detector
	skippedN      int64 // frames dropped by the motion gate
	lastFrameNano int64 // UnixNano of the most recent captured frame
}

// Counts returns the runtime frame counters and the time of the last captured
// frame (for FPS + online reporting).
func (w *CameraWorker) Counts() (captured, detect, skipped, lastFrameNano int64) {
	return atomic.LoadInt64(&w.capturedN),
		atomic.LoadInt64(&w.detectN),
		atomic.LoadInt64(&w.skippedN),
		atomic.LoadInt64(&w.lastFrameNano)
}

// LatestFrame returns the most recently captured JPEG frame, or nil if none yet.
func (w *CameraWorker) LatestFrame() []byte {
	w.frameMu.RLock()
	defer w.frameMu.RUnlock()
	if w.latestJPEG == nil {
		return nil
	}
	out := make([]byte, len(w.latestJPEG))
	copy(out, w.latestJPEG)
	return out
}

func (w *CameraWorker) storeFrame(f *Frame) {
	img := image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width; x++ {
			i := (y*f.Width + x) * 3
			img.SetRGBA(x, y, color.RGBA{R: f.Data[i+2], G: f.Data[i+1], B: f.Data[i], A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		return
	}
	w.frameMu.Lock()
	first := w.latestJPEG == nil
	w.latestJPEG = buf.Bytes()
	w.frameMu.Unlock()
	if first {
		slog.Info("camera preview ready", "camera", w.name, "bytes", buf.Len())
	}
}

// NewCameraWorker creates a CameraWorker but does not start it.
func NewCameraWorker(name string, cfg config.CameraConfig, fanin chan<- BatchItem, recordingsDir string) *CameraWorker {
	return &CameraWorker{
		name:           name,
		cfg:            cfg,
		fanin:          fanin,
		recordingsDir:  recordingsDir,
		RecordNotifyCh: make(chan string, 64),
		MotionCh:       make(chan MotionSignal, 64),
	}
}

// Start launches all worker goroutines. Calling Start on an already-running
// worker is a no-op. Cancel the parent ctx or call Stop() to shut down.
func (w *CameraWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return
	}
	w.running = true

	workerCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	// Buffered channels sized for ~1 second of frames.
	detectFPS := w.cfg.Detect.FPS
	if detectFPS <= 0 {
		detectFPS = 5
	}
	detectCh := make(chan Frame, detectFPS*2)

	w.wg.Add(3)
	go w.captureLoop(workerCtx, detectCh)
	go w.motionLoop(workerCtx, detectCh)
	go w.recordLoop(workerCtx)
}

// Stop cancels the worker context and waits for all goroutines to exit.
func (w *CameraWorker) Stop() {
	w.mu.Lock()
	cancel := w.cancel
	w.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	w.wg.Wait()

	w.mu.Lock()
	w.running = false
	w.mu.Unlock()
}

// captureLoop runs DetectCapture, sending frames to detectCh.
// It recovers from panics and restarts automatically.
func (w *CameraWorker) captureLoop(ctx context.Context, detectCh chan<- Frame) {
	defer w.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("captureLoop panic recovered",
				"camera", w.name,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()

	url := w.detectURL()
	dc := NewDetectCapture(
		w.name, url,
		w.cfg.Detect.Width, w.cfg.Detect.Height, w.cfg.Detect.FPS,
		w.cfg.FFmpeg.HWAccel,
		detectCh,
	)
	dc.Run(ctx)
}

// motionLoop reads from detectCh, runs the motion detector, and forwards
// frames with motion (or when motion detection is disabled) to fanin.
func (w *CameraWorker) motionLoop(ctx context.Context, detectCh <-chan Frame) {
	defer w.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("motionLoop panic recovered",
				"camera", w.name,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()

	motionEnabled := true
	threshold := 25.0
	alpha := 0.01
	detectWithoutMotion := false
	if w.cfg.Motion != nil {
		motionEnabled = w.cfg.Motion.Enabled
		threshold = w.cfg.Motion.Threshold
		alpha = w.cfg.Motion.Alpha
		detectWithoutMotion = w.cfg.Motion.DetectWithoutMotion
	}

	scoreThreshold := 0.015
	lightningThresh := 0.0
	if w.cfg.Motion != nil {
		if w.cfg.Motion.ContourArea > 0 {
			scoreThreshold = w.cfg.Motion.ContourArea / 100.0 // config value is a percentage
		}
		if w.cfg.Motion.LightningThresh > 0 {
			lightningThresh = w.cfg.Motion.LightningThresh / 100.0
		}
	}

	var md *MotionDetector
	if motionEnabled {
		md = NewMotionDetector(threshold, alpha, scoreThreshold, lightningThresh)
	}

	// Suppress motion signals until the background model has had enough frames
	// to stabilise.  alpha=0.01 means ~100 frames to converge; use fps*20
	// (20 seconds worth) so the value scales with configured FPS.
	fps := w.cfg.Detect.FPS
	if fps <= 0 {
		fps = 5
	}
	warmup := fps * 20
	if warmup < 30 {
		warmup = 30
	}

	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-detectCh:
			if !ok {
				return
			}

			atomic.AddInt64(&w.capturedN, 1)
			atomic.StoreInt64(&w.lastFrameNano, time.Now().UnixNano())

			// Cache latest frame for live preview (every Nth frame to reduce CPU).
			if frame.SeqNum%5 == 0 {
				w.storeFrame(&frame)
			}

			send := true
			if md != nil {
				hasMotion, score := md.Detect(&frame)
				if warmup > 0 {
					warmup--
					hasMotion = false
					score = 0
				}
				sig := MotionSignal{HasMotion: hasMotion, Score: score, At: frame.Timestamp}
				select {
				case w.MotionCh <- sig:
				default:
				}
				// Gate the detector on motion unless detect_without_motion is set.
				if !hasMotion && !detectWithoutMotion {
					send = false
					atomic.AddInt64(&w.skippedN, 1)
				}
			}

			if send {
				atomic.AddInt64(&w.detectN, 1)
				resultCh := make(chan DetectionResult, 1)
				item := BatchItem{Frame: frame, ResultCh: resultCh}
				select {
				case w.fanin <- item:
				case <-ctx.Done():
					return
				case <-time.After(100 * time.Millisecond):
					// Drop if fanin is full — prefer to drop old frames.
				}
			}
		}
	}
}

// recordLoop runs RecordCapture and forwards segment paths to RecordNotifyCh.
func (w *CameraWorker) recordLoop(ctx context.Context) {
	defer w.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("recordLoop panic recovered",
				"camera", w.name,
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()

	recordEnabled := true
	if w.cfg.Record != nil {
		recordEnabled = w.cfg.Record.Enabled
	}
	if !recordEnabled {
		return
	}

	url := w.recordURL()
	// If detect and record share the same RTSP URL the camera likely only allows
	// one connection. Relay through go2rtc so the camera has a single consumer.
	if url == w.detectURL() {
		url = fmt.Sprintf("rtsp://go2rtc:8554/%s", w.name)
	}
	segmentSecs := 10
	if w.cfg.Record != nil && w.cfg.Record.SegmentDuration > 0 {
		segmentSecs = w.cfg.Record.SegmentDuration
	}

	recordingsDir := w.recordingsDir
	if recordingsDir == "" {
		recordingsDir = "/recordings"
	}
	outputPattern := fmt.Sprintf(
		"%s/%s/%%Y-%%m-%%d/%%H/%%M%%S.mp4",
		recordingsDir, w.name,
	)
	if w.cfg.Record != nil && w.cfg.Record.OutputPattern != "" {
		outputPattern = w.cfg.Record.OutputPattern
	}

	segCh := make(chan string, 32)
	rc := NewRecordCapture(w.name, url, outputPattern, segmentSecs, w.cfg.FFmpeg.HWAccel, segCh)

	go rc.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case path, ok := <-segCh:
			if !ok {
				return
			}
			select {
			case w.RecordNotifyCh <- path:
			default:
				slog.Warn("record notify channel full, dropping segment",
					"camera", w.name,
					"path", path,
				)
			}
		}
	}
}

// detectURL returns the RTSP URL for the detect role.
func (w *CameraWorker) detectURL() string {
	for _, in := range w.cfg.FFmpeg.Inputs {
		for _, role := range in.Roles {
			if role == "detect" {
				return in.Path
			}
		}
	}
	// Fall back to first input.
	if len(w.cfg.FFmpeg.Inputs) > 0 {
		return w.cfg.FFmpeg.Inputs[0].Path
	}
	return ""
}

// recordURL returns the RTSP URL for the record role.
func (w *CameraWorker) recordURL() string {
	for _, in := range w.cfg.FFmpeg.Inputs {
		for _, role := range in.Roles {
			if role == "record" {
				return in.Path
			}
		}
	}
	return w.detectURL()
}
