package camera

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	backoffMin = 1 * time.Second
	backoffMax = 30 * time.Second
	// If a capture streamed for at least this long before failing, treat the next
	// failure as fresh and reset the reconnect backoff to backoffMin. Without this a
	// flaky camera (e.g. eufy RTSP that resets every ~30s) ratchets the backoff to
	// backoffMax and then sits offline that long between every reconnect.
	backoffResetAfter = 20 * time.Second
	// If no decoded frame arrives within this window, the capture is considered stalled
	// and ffmpeg is killed so Run() reconnects. ffmpeg's own -timeout doesn't reliably
	// fire when the RTSP TCP socket stays open but video stops (or the initial connect
	// hangs) — that state wedged Back_Drive + eufy offline until a manual restart. This
	// app-level watchdog is the belt-and-suspenders that makes cameras self-heal.
	frameStallTimeout = 15 * time.Second
)

// DetectCapture manages an ffmpeg subprocess that decodes an RTSP stream to
// raw BGR24 frames and pushes them onto frameCh. It restarts with exponential
// backoff on any failure. Cancel ctx to stop.
type DetectCapture struct {
	cameraID string
	url      string
	width    int
	height   int
	fps      int
	hwAccel  string
	frameCh  chan<- Frame
}

// NewDetectCapture creates a DetectCapture. frameCh must be non-nil.
func NewDetectCapture(cameraID, url string, width, height, fps int, hwAccel string, frameCh chan<- Frame) *DetectCapture {
	return &DetectCapture{
		cameraID: cameraID,
		url:      url,
		width:    width,
		height:   height,
		fps:      fps,
		hwAccel:  hwAccel,
		frameCh:  frameCh,
	}
}

// Run starts the capture loop. It blocks until ctx is cancelled.
func (dc *DetectCapture) Run(ctx context.Context) {
	backoff := backoffMin
	var seqNum uint64

	for {
		if ctx.Err() != nil {
			return
		}
		startedAt := time.Now()
		err := dc.runOnce(ctx, &seqNum)
		if ctx.Err() != nil {
			return
		}
		// If the stream ran healthily for a while before dropping, treat this as a
		// fresh failure and reset the backoff — otherwise a camera that flaps every
		// ~30s (eufy) ratchets to backoffMax and then sits offline that long each time.
		if time.Since(startedAt) >= backoffResetAfter {
			backoff = backoffMin
		}
		if err != nil {
			slog.Warn("detect capture restarting",
				"camera", dc.cameraID,
				"err", err,
				"backoff", backoff,
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < backoffMax {
			backoff *= 2
			if backoff > backoffMax {
				backoff = backoffMax
			}
		}
	}
}

func (dc *DetectCapture) runOnce(ctx context.Context, seqNum *uint64) error {
	frameSize := FrameSize(dc.width, dc.height)

	args := dc.buildArgs()
	cmd := exec.CommandContext(ctx, "ffmpeg", args...) //nolint:gosec
	stderr, _ := cmd.StderrPipe()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("detect capture %s: stdout pipe: %w", dc.cameraID, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("detect capture %s: start ffmpeg: %w", dc.cameraID, err)
	}

	// Drain stderr in background at debug level.
	go drainStderr(dc.cameraID, "detect", stderr)

	slog.Info("detect capture started",
		"camera", dc.cameraID,
		"url", redactURL(dc.url),
		"resolution", fmt.Sprintf("%dx%d@%dfps", dc.width, dc.height, dc.fps),
	)

	// Frame-starvation watchdog: kills ffmpeg (unblocking the read below) if no frame
	// arrives within frameStallTimeout, so Run() reconnects instead of hanging forever
	// in io.ReadFull on a stalled-but-open RTSP socket. The timer also starts before the
	// first frame, so a hung initial connect is caught too.
	gotFrame := make(chan struct{}, 1)
	watchdogDone := make(chan struct{})
	go func() {
		t := time.NewTimer(frameStallTimeout)
		defer t.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-gotFrame:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(frameStallTimeout)
			case <-t.C:
				slog.Warn("detect capture stalled — killing ffmpeg to force reconnect",
					"camera", dc.cameraID, "stall", frameStallTimeout)
				_ = cmd.Process.Kill()
				return
			}
		}
	}()
	defer close(watchdogDone)

	buf := make([]byte, frameSize)
	for {
		if ctx.Err() != nil {
			break
		}
		n, err := io.ReadFull(stdout, buf)
		if err != nil || n != frameSize {
			break
		}
		// Tell the watchdog a frame arrived (non-blocking).
		select {
		case gotFrame <- struct{}{}:
		default:
		}

		data := make([]byte, frameSize)
		copy(data, buf)

		f := Frame{
			CameraID:  dc.cameraID,
			Timestamp: time.Now(),
			Data:      data,
			Width:     dc.width,
			Height:    dc.height,
			SeqNum:    *seqNum,
		}
		*seqNum++

		select {
		case dc.frameCh <- f:
		case <-ctx.Done():
			break
		default:
			// Drop frame if pipeline is backed up — never block capture.
		}
	}

	_ = cmd.Wait()
	return nil
}

func (dc *DetectCapture) buildArgs() []string {
	var args []string

	// Default: CPU decode + CPU scale.
	vf := fmt.Sprintf("scale=%d:%d", dc.width, dc.height)

	switch dc.hwAccel {
	case "cuda":
		// Full GPU path: NVDEC decode + scale_cuda resize on the GPU, then
		// download only the small resized frame and convert nv12->bgr24 on the
		// CPU. Keeps decode AND full-res scaling off the CPU — the big win for
		// high-res cameras (a 1080p frame is scaled on the GPU, so only a
		// 640x480 frame ever crosses to system memory).
		args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
		vf = fmt.Sprintf("scale_cuda=%d:%d,hwdownload,format=nv12,format=bgr24", dc.width, dc.height)
	case "":
		// no hwaccel
	default:
		args = append(args, "-hwaccel", dc.hwAccel)
	}

	args = append(args,
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		// Socket I/O timeout (µs): if the camera stops sending data for 10s (a stalled
		// eufy stream that hasn't yet reset the TCP connection), ffmpeg errors out and
		// exits so Run() reconnects, instead of blocking forever in io.ReadFull. Healthy
		// cameras stream continuously so this never fires for them.
		"-timeout", "10000000",
		"-i", dc.url,
		"-vf", vf,
		"-f", "rawvideo",
		"-pix_fmt", "bgr24",
		"-r", fmt.Sprintf("%d", dc.fps),
		"pipe:1",
	)
	return args
}

// RecordCapture manages an ffmpeg subprocess that copies the RTSP stream into
// rolling MP4 segment files. Segment paths are reported on segmentCh.
// Cancel ctx to stop.
type RecordCapture struct {
	cameraID    string
	url         string
	outputGlob  string // e.g. /recordings/front/%Y-%m-%d/%H/%M%S.mp4
	dirGoFmt    string // Go time.Format equivalent of the directory component
	segmentSecs int
	hwAccel     string
	segmentCh   chan<- string
}

// NewRecordCapture creates a RecordCapture. segmentCh receives the path of
// each completed segment file.
func NewRecordCapture(cameraID, url, outputPattern string, segmentSecs int, hwAccel string, segmentCh chan<- string) *RecordCapture {
	return &RecordCapture{
		cameraID:    cameraID,
		url:         url,
		outputGlob:  outputPattern,
		dirGoFmt:    strftimeDir(outputPattern),
		segmentSecs: segmentSecs,
		hwAccel:     hwAccel,
		segmentCh:   segmentCh,
	}
}

// Run starts the recording loop. It blocks until ctx is cancelled.
func (rc *RecordCapture) Run(ctx context.Context) {
	backoff := backoffMin
	for {
		if ctx.Err() != nil {
			return
		}
		err := rc.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("record capture restarting",
				"camera", rc.cameraID,
				"err", err,
				"backoff", backoff,
			)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < backoffMax {
			backoff *= 2
			if backoff > backoffMax {
				backoff = backoffMax
			}
		}
	}
}

func (rc *RecordCapture) runOnce(ctx context.Context) error {
	// Pre-create output directories for the current and next two hours so that
	// ffmpeg's strftime segment paths exist before it tries to open them.
	rc.ensureSegmentDirs(time.Now())
	go rc.segmentDirMaintainer(ctx)

	// Use a temp file for the segment list so ffmpeg writes full absolute paths.
	segList := fmt.Sprintf("/tmp/sentinel_segs_%s_%d.txt", rc.cameraID, time.Now().UnixNano())
	defer os.Remove(segList) //nolint:errcheck

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		// Socket I/O timeout (µs): exit if the camera stops sending for 10s so Run()
		// reconnects rather than hanging in cmd.Wait() (parity with the detect path).
		"-timeout", "10000000",
		"-i", rc.url,
		"-c", "copy",
		"-f", "segment",
		"-segment_time", fmt.Sprintf("%d", rc.segmentSecs),
		"-segment_format", "mp4",
		"-reset_timestamps", "1",
		"-strftime", "1",
		"-segment_list", segList,
		"-segment_list_size", "0",
		"-segment_list_flags", "cache+live",
		rc.outputGlob,
	}
	if rc.hwAccel != "" {
		args = append([]string{"-hwaccel", rc.hwAccel}, args...)
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...) //nolint:gosec
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("record capture %s: start ffmpeg: %w", rc.cameraID, err)
	}

	go drainStderr(rc.cameraID, "record", stderr)
	gotSegment := make(chan struct{}, 1)
	go rc.tailSegmentList(ctx, segList, gotSegment)

	// Segment-production watchdog: if no new segment completes within stall — a stalled-
	// but-open RTSP socket that ffmpeg's -timeout doesn't catch — kill ffmpeg so Run()
	// reconnects instead of hanging in cmd.Wait() and silently gapping the recording.
	stall := time.Duration(rc.segmentSecs) * time.Second * 3
	if stall < 30*time.Second {
		stall = 30 * time.Second
	}
	watchdogDone := make(chan struct{})
	go func() {
		t := time.NewTimer(stall)
		defer t.Stop()
		for {
			select {
			case <-watchdogDone:
				return
			case <-gotSegment:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(stall)
			case <-t.C:
				slog.Warn("record capture stalled — killing ffmpeg to force reconnect",
					"camera", rc.cameraID, "stall", stall)
				_ = cmd.Process.Kill()
				return
			}
		}
	}()
	defer close(watchdogDone)

	slog.Info("record capture started", "camera", rc.cameraID, "url", redactURL(rc.url))

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("record capture %s: ffmpeg exited: %w", rc.cameraID, err)
	}
	return nil
}

// ensureSegmentDirs pre-creates the output directories for the given time and
// the next two hours so that ffmpeg's strftime paths always exist.
func (rc *RecordCapture) ensureSegmentDirs(now time.Time) {
	goFmt := strftimeDir(rc.outputGlob)
	for h := 0; h < 3; h++ {
		dir := now.Add(time.Duration(h) * time.Hour).Format(goFmt)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Warn("record: mkdir failed", "camera", rc.cameraID, "dir", dir, "err", err)
		}
	}
}

// segmentDirMaintainer runs until ctx is cancelled, creating upcoming hour
// directories every 30 minutes so recordings never fail at an hour boundary.
func (rc *RecordCapture) segmentDirMaintainer(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			rc.ensureSegmentDirs(t)
		}
	}
}

// strftimeDir converts an ffmpeg strftime output glob to a Go time.Format
// string for the directory component only.
func strftimeDir(outputGlob string) string {
	dir := filepath.Dir(outputGlob)
	r := strings.NewReplacer(
		"%Y", "2006",
		"%m", "01",
		"%d", "02",
		"%H", "15",
		"%M", "04",
		"%S", "05",
	)
	return r.Replace(dir)
}

// tailSegmentList polls the ffmpeg segment list file and forwards each new
// absolute path to segmentCh. ffmpeg appends to this file as segments complete.
func (rc *RecordCapture) tailSegmentList(ctx context.Context, listPath string, gotSegment chan<- struct{}) {
	// Wait for the file to appear.
	var f *os.File
	for {
		var err error
		f, err = os.Open(listPath)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	defer f.Close() //nolint:errcheck

	seen := make(map[string]struct{})
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return
			}
			buf := make([]byte, 65536)
			n, _ := f.Read(buf)
			now := time.Now()
			lines := strings.Split(string(buf[:n]), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// ffmpeg writes only the basename — reconstruct the full path.
				// Check current and previous hour in case of boundary crossing.
				if !filepath.IsAbs(line) {
					found := ""
					for _, t := range []time.Time{now, now.Add(-time.Hour)} {
						candidate := filepath.Join(t.Format(rc.dirGoFmt), line)
						if _, err := os.Stat(candidate); err == nil {
							found = candidate
							break
						}
					}
					if found == "" {
						continue
					}
					line = found
				}
				if _, ok := seen[line]; ok {
					continue
				}
				seen[line] = struct{}{}
				// Tell the record watchdog a fresh segment completed (non-blocking).
				select {
				case gotSegment <- struct{}{}:
				default:
				}
				select {
				case rc.segmentCh <- line:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────────────

func drainStderr(camera, role string, r io.Reader) {
	buf := make([]byte, 4096)
	var acc strings.Builder
	for {
		n, err := r.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
			for {
				s := acc.String()
				idx := strings.IndexByte(s, '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimSpace(s[:idx])
				acc.Reset()
				acc.WriteString(s[idx+1:])
				if line != "" {
					slog.Debug("ffmpeg", "camera", camera, "role", role, "msg", line)
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// redactURL removes the password from rtsp://user:pass@host URLs for logging.
func redactURL(u string) string {
	if i := strings.Index(u, "@"); i >= 0 {
		if j := strings.LastIndex(u[:i], "//"); j >= 0 {
			prefix := u[:j+2]
			rest := u[i:]
			if k := strings.IndexByte(u[j+2:i], ':'); k >= 0 {
				user := u[j+2 : j+2+k]
				return prefix + user + ":***" + rest
			}
		}
	}
	return u
}
