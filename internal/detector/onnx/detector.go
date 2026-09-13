//go:build cgo

package onnx

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/bughatti/sentinel/internal/camera"
	det "github.com/bughatti/sentinel/internal/detector"
)

// Detector is a YOLOv8/v9 (anchor-free, [1,84,8400] output) object detector
// backed by ONNX Runtime. It is thread-safe via an internal mutex; concurrent
// Detect calls are serialised.
type Detector struct {
	mu           sync.Mutex
	session      *ort.AdvancedSession
	inputTensor  *ort.Tensor[float32]
	outputTensor *ort.Tensor[float32]
	labels       []string
	confThresh   float32
	iouThresh    float32
	modelPath    string
	inputW       int
	inputH       int

	// batching
	batch      int // frames per inference (model's fixed batch dim)
	channels   int // 4 + numClasses
	numAnchors int // anchors per frame (scales with input size)

	// stats (atomic)
	framesN    int64 // total frames inferred
	inferNanos int64 // cumulative inference wall time
}

// Stats returns total frames processed and average inference time (ms).
func (d *Detector) Stats() (int64, float64) {
	frames := atomic.LoadInt64(&d.framesN)
	if frames == 0 {
		return 0, 0
	}
	return frames, float64(atomic.LoadInt64(&d.inferNanos)) / float64(frames) / 1e6
}

// New creates and initialises an ONNX detector. The ONNX Runtime shared
// library must be discoverable at runtime (via LD_LIBRARY_PATH or
// ort.SetSharedLibraryPath before calling New).
func New(modelPath, labelPath string, confThreshold float32, numThreads int, inputW, inputH int, useGPU bool, gpuDeviceID int, useTensorRT bool, batchSize int) (*Detector, error) {
	if inputW <= 0 {
		inputW = 640
	}
	if inputH <= 0 {
		inputH = 640
	}
	if batchSize <= 0 {
		batchSize = 1
	}

	ort.SetSharedLibraryPath("/usr/local/lib/libonnxruntime.so")

	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("onnx: init environment: %w", err)
		}
	}

	labels, err := loadLabels(labelPath)
	if err != nil {
		slog.Warn("onnx: could not load label file, using class indices", "path", labelPath, "err", err)
	}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("onnx: session options: %w", err)
	}
	defer opts.Destroy()

	if numThreads > 0 {
		if err := opts.SetIntraOpNumThreads(numThreads); err != nil {
			return nil, fmt.Errorf("onnx: set threads: %w", err)
		}
	}

	if useGPU {
		// TensorRT EP first (preferred) — it compiles the model to a TensorRT
		// engine (fp16 on the RTX 5080) and runs it; unsupported ops + engine
		// build fall through to the CUDA EP below. The engine is cached so only
		// the first startup pays the (minutes-long) build cost.
		if useTensorRT {
			_ = os.MkdirAll("/tmp/sentinel/trt-cache", 0o755)
			trtOpts, err := ort.NewTensorRTProviderOptions()
			if err != nil {
				return nil, fmt.Errorf("onnx: tensorrt provider options: %w", err)
			}
			if err := trtOpts.Update(map[string]string{
				"device_id":               fmt.Sprintf("%d", gpuDeviceID),
				"trt_fp16_enable":         "1",
				"trt_engine_cache_enable": "1",
				"trt_engine_cache_path":   "/tmp/sentinel/trt-cache",
				"trt_timing_cache_enable": "1",
			}); err != nil {
				trtOpts.Destroy()
				return nil, fmt.Errorf("onnx: tensorrt provider update: %w", err)
			}
			if err := opts.AppendExecutionProviderTensorRT(trtOpts); err != nil {
				slog.Warn("onnx: TensorRT EP unavailable, falling back to CUDA", "err", err)
			} else {
				slog.Info("onnx: TensorRT execution provider enabled (fp16, engine cache)", "device", gpuDeviceID)
			}
			trtOpts.Destroy()
		}

		cudaOpts, err := ort.NewCUDAProviderOptions()
		if err != nil {
			return nil, fmt.Errorf("onnx: cuda provider options: %w", err)
		}
		defer cudaOpts.Destroy()
		if err := cudaOpts.Update(map[string]string{
			"device_id": fmt.Sprintf("%d", gpuDeviceID),
		}); err != nil {
			return nil, fmt.Errorf("onnx: cuda provider update: %w", err)
		}
		if err := opts.AppendExecutionProviderCUDA(cudaOpts); err != nil {
			return nil, fmt.Errorf("onnx: append cuda provider: %w", err)
		}
		slog.Info("onnx: CUDA execution provider enabled (fallback)", "device", gpuDeviceID)
	}

	// Input tensor: float32[batch, 3, inputH, inputW]
	inputShape := ort.NewShape(int64(batchSize), 3, int64(inputH), int64(inputW))
	inputTensor, err := ort.NewEmptyTensor[float32](inputShape)
	if err != nil {
		return nil, fmt.Errorf("onnx: create input tensor: %w", err)
	}

	// Output tensor: float32[batch, 4+numClasses, numAnchors]. Anchor-free YOLO
	// (v8/v9/11) emits one column per anchor across strides 8/16/32, so the
	// count scales with input size — computing it here lets any input
	// resolution (640, 960, 1280, …) and model size work unchanged.
	numClasses := len(labels)
	if numClasses == 0 {
		numClasses = 80
	}
	channels := 4 + numClasses
	numAnchors := (inputW/8)*(inputH/8) + (inputW/16)*(inputH/16) + (inputW/32)*(inputH/32)
	outputShape := ort.NewShape(int64(batchSize), int64(channels), int64(numAnchors))
	outputTensor, err := ort.NewEmptyTensor[float32](outputShape)
	if err != nil {
		inputTensor.Destroy()
		return nil, fmt.Errorf("onnx: create output tensor: %w", err)
	}
	slog.Info("onnx: model io", "input", fmt.Sprintf("%dx%d", inputW, inputH), "batch", batchSize, "classes", numClasses, "anchors", numAnchors)

	session, err := ort.NewAdvancedSession(
		modelPath,
		[]string{"images"},
		[]string{"output0"},
		[]ort.ArbitraryTensor{inputTensor},
		[]ort.ArbitraryTensor{outputTensor},
		opts,
	)
	if err != nil {
		inputTensor.Destroy()
		outputTensor.Destroy()
		return nil, fmt.Errorf("onnx: create session from %s: %w", modelPath, err)
	}

	if confThreshold <= 0 {
		confThreshold = 0.5
	}

	return &Detector{
		session:      session,
		inputTensor:  inputTensor,
		outputTensor: outputTensor,
		labels:       labels,
		confThresh:   confThreshold,
		iouThresh:    0.45,
		modelPath:    modelPath,
		inputW:       inputW,
		inputH:       inputH,
		batch:        batchSize,
		channels:     channels,
		numAnchors:   numAnchors,
	}, nil
}

// Detect runs the detector over frames in true GPU batches of d.batch (the
// model's fixed batch dimension), returning one []Detection per frame. All
// cameras in a cycle are inferred in a single GPU call, so a big model at high
// resolution keeps up where sequential inference couldn't. The ORT session is
// not concurrency-safe, so calls are serialised by the mutex.
func (d *Detector) Detect(ctx context.Context, frames []camera.Frame) ([][]det.Detection, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	results := make([][]det.Detection, len(frames))
	frameStride := 3 * d.inputW * d.inputH   // input floats per frame
	outStride := d.channels * d.numAnchors   // output floats per frame
	inData := d.inputTensor.GetData()
	outData := d.outputTensor.GetData()

	for start := 0; start < len(frames); start += d.batch {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		end := start + d.batch
		if end > len(frames) {
			end = len(frames)
		}
		n := end - start // real frames in this batch (rest of the batch slot is padding)

		valid := make([]bool, n)
		for i := 0; i < n; i++ {
			td, err := preprocess(&frames[start+i], d.inputW, d.inputH)
			if err != nil {
				slog.Warn("onnx: preprocess", "camera", frames[start+i].CameraID, "err", err)
				continue
			}
			if len(td) == frameStride && (i+1)*frameStride <= len(inData) {
				copy(inData[i*frameStride:(i+1)*frameStride], td)
				valid[i] = true
			}
		}

		runStart := time.Now()
		if err := d.session.Run(); err != nil {
			slog.Error("onnx: batched run", "err", err)
			continue
		}
		atomic.AddInt64(&d.inferNanos, time.Since(runStart).Nanoseconds())
		atomic.AddInt64(&d.framesN, int64(n))

		for i := 0; i < n; i++ {
			if !valid[i] {
				continue
			}
			fo := outData[i*outStride : (i+1)*outStride]
			results[start+i] = parseYOLO(
				fo, d.labels, d.confThresh, d.iouThresh,
				frames[start+i].Width, frames[start+i].Height, d.inputW, d.inputH,
			)
		}
	}
	return results, nil
}

// Warmup runs a single batched inference on the (zero-initialised) input tensor
// to pre-load weights and trigger the TensorRT engine build before real frames
// arrive.
func (d *Detector) Warmup(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.session.Run(); err != nil {
		return fmt.Errorf("onnx warmup: %w", err)
	}
	slog.Info("onnx detector warmed up", "model", d.modelPath, "batch", d.batch)
	return nil
}

// Close releases ONNX Runtime session and tensor resources.
func (d *Detector) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.session != nil {
		d.session.Destroy()
		d.session = nil
	}
	if d.inputTensor != nil {
		d.inputTensor.Destroy()
		d.inputTensor = nil
	}
	if d.outputTensor != nil {
		d.outputTensor.Destroy()
		d.outputTensor = nil
	}
	return nil
}

// Type returns "onnx".
func (d *Detector) Type() string { return "onnx" }

// loadLabels reads a COCO-style label file (one label per line).
func loadLabels(path string) ([]string, error) {
	if path == "" {
		return nil, fmt.Errorf("label path is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open labels: %w", err)
	}
	defer f.Close()

	var labels []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			labels = append(labels, line)
		}
	}
	return labels, sc.Err()
}
