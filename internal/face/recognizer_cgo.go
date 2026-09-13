//go:build cgo

package face

import (
	"fmt"
	"image"
	"log/slog"
	"sync"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/bughatti/sentinel/internal/camera"
	"github.com/bughatti/sentinel/internal/config"
)

const (
	faceScoreThresh = 0.5
	faceNMSThresh   = 0.4
)

type onnxRecognizer struct {
	mu sync.Mutex

	// SCRFD detector
	scrfd    *ort.AdvancedSession
	scrfdIn  *ort.Tensor[float32]
	scrfdOut []*ort.Tensor[float32] // 9 outputs, graph order

	// ArcFace embedder
	arc    *ort.AdvancedSession
	arcIn  *ort.Tensor[float32]
	arcOut *ort.Tensor[float32]

	minFace int
}

// New builds a Recognizer. If face recognition is disabled or the model files
// are unset, it returns the no-op recognizer.
func New(cfg *config.Config) (Recognizer, error) {
	fc := cfg.FaceRecognition
	if !fc.Enabled {
		return Disabled(), nil
	}
	if fc.DetectorModel == "" || fc.RecognitionModel == "" {
		slog.Warn("face: enabled but detector_model/recognition_model not set — disabling")
		return Disabled(), nil
	}

	ort.SetSharedLibraryPath("/usr/local/lib/libonnxruntime.so")
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("face: init ort: %w", err)
		}
	}

	mkOpts := func() (*ort.SessionOptions, error) {
		opts, err := ort.NewSessionOptions()
		if err != nil {
			return nil, err
		}
		if cfg.Detector.UseGPU {
			cudaOpts, err := ort.NewCUDAProviderOptions()
			if err != nil {
				opts.Destroy()
				return nil, err
			}
			defer cudaOpts.Destroy()
			_ = cudaOpts.Update(map[string]string{"device_id": fmt.Sprintf("%d", cfg.Detector.GPUDeviceID)})
			if err := opts.AppendExecutionProviderCUDA(cudaOpts); err != nil {
				slog.Warn("face: CUDA provider unavailable, using CPU", "err", err)
			}
		}
		return opts, nil
	}

	r := &onnxRecognizer{minFace: fc.MinFaceSize}
	if r.minFace <= 0 {
		r.minFace = 20
	}

	// ── SCRFD ──────────────────────────────────────────────────────────────
	sOpts, err := mkOpts()
	if err != nil {
		return nil, fmt.Errorf("face: scrfd opts: %w", err)
	}
	defer sOpts.Destroy()

	scrfdIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, scrfdInput, scrfdInput))
	if err != nil {
		return nil, fmt.Errorf("face: scrfd input tensor: %w", err)
	}
	// Outputs in graph order: scores(8,16,32), bboxes(8,16,32), kps(8,16,32).
	outNames := []string{"448", "471", "494", "451", "474", "497", "454", "477", "500"}
	outDims := [][2]int64{{12800, 1}, {3200, 1}, {800, 1}, {12800, 4}, {3200, 4}, {800, 4}, {12800, 10}, {3200, 10}, {800, 10}}
	scrfdOut := make([]*ort.Tensor[float32], 9)
	outArb := make([]ort.ArbitraryTensor, 9)
	for i := range outNames {
		t, err := ort.NewEmptyTensor[float32](ort.NewShape(outDims[i][0], outDims[i][1]))
		if err != nil {
			return nil, fmt.Errorf("face: scrfd out tensor %d: %w", i, err)
		}
		scrfdOut[i] = t
		outArb[i] = t
	}
	scrfd, err := ort.NewAdvancedSession(fc.DetectorModel,
		[]string{"input.1"}, outNames,
		[]ort.ArbitraryTensor{scrfdIn}, outArb, sOpts)
	if err != nil {
		return nil, fmt.Errorf("face: load scrfd %s: %w", fc.DetectorModel, err)
	}
	r.scrfd = scrfd
	r.scrfdIn = scrfdIn
	r.scrfdOut = scrfdOut

	// ── ArcFace ────────────────────────────────────────────────────────────
	aOpts, err := mkOpts()
	if err != nil {
		return nil, fmt.Errorf("face: arcface opts: %w", err)
	}
	defer aOpts.Destroy()

	arcIn, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 3, arcfaceSize, arcfaceSize))
	if err != nil {
		return nil, fmt.Errorf("face: arcface input tensor: %w", err)
	}
	arcOut, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 512))
	if err != nil {
		return nil, fmt.Errorf("face: arcface output tensor: %w", err)
	}
	arc, err := ort.NewAdvancedSession(fc.RecognitionModel,
		[]string{"input.1"}, []string{"683"},
		[]ort.ArbitraryTensor{arcIn}, []ort.ArbitraryTensor{arcOut}, aOpts)
	if err != nil {
		return nil, fmt.Errorf("face: load arcface %s: %w", fc.RecognitionModel, err)
	}
	r.arc = arc
	r.arcIn = arcIn
	r.arcOut = arcOut

	slog.Info("face: recognizer loaded", "detector", fc.DetectorModel, "recognition", fc.RecognitionModel, "gpu", cfg.Detector.UseGPU)
	return r, nil
}

func (r *onnxRecognizer) Enabled() bool { return true }

// runSCRFD fills the input from the sampler and returns decoded faces in source
// pixel coordinates (with landmarks). Caller holds r.mu.
func (r *onnxRecognizer) runSCRFD(sample rgbSampler, srcW, srcH int) ([]image.Rectangle, [][5][2]float32, []float32, error) {
	scale := fillSCRFDInput(sample, srcW, srcH, r.scrfdIn.GetData())
	if err := r.scrfd.Run(); err != nil {
		return nil, nil, nil, fmt.Errorf("face: scrfd run: %w", err)
	}
	scores := [][]float32{r.scrfdOut[0].GetData(), r.scrfdOut[1].GetData(), r.scrfdOut[2].GetData()}
	bboxes := [][]float32{r.scrfdOut[3].GetData(), r.scrfdOut[4].GetData(), r.scrfdOut[5].GetData()}
	kpss := [][]float32{r.scrfdOut[6].GetData(), r.scrfdOut[7].GetData(), r.scrfdOut[8].GetData()}
	raw := decodeSCRFD(scores, bboxes, kpss, faceScoreThresh, faceNMSThresh)

	boxes := make([]image.Rectangle, 0, len(raw))
	kpsList := make([][5][2]float32, 0, len(raw))
	scoreList := make([]float32, 0, len(raw))
	for _, f := range raw {
		box, kps := scaleRawFace(f, scale)
		boxes = append(boxes, box)
		kpsList = append(kpsList, kps)
		scoreList = append(scoreList, f.score)
	}
	return boxes, kpsList, scoreList, nil
}

// embed runs ArcFace on the aligned face and returns a normalised 512-d vector.
// Caller holds r.mu.
func (r *onnxRecognizer) embed(sample rgbSampler, kps [5][2]float32) []float32 {
	buildArcfaceInput(sample, kps, r.arcIn.GetData())
	if err := r.arc.Run(); err != nil {
		slog.Warn("face: arcface run", "err", err)
		return nil
	}
	src := r.arcOut.GetData()
	emb := make([]float32, len(src))
	copy(emb, src)
	return l2normalize(emb)
}

func (r *onnxRecognizer) DetectAndEmbed(frame *camera.Frame) ([]DetectedFace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	sample := frameSampler(frame)
	boxes, kpsList, scores, err := r.runSCRFD(sample, frame.Width, frame.Height)
	if err != nil {
		return nil, err
	}
	var out []DetectedFace
	for i := range boxes {
		if boxes[i].Dx() < r.minFace || boxes[i].Dy() < r.minFace {
			continue
		}
		emb := r.embed(sample, kpsList[i])
		if emb == nil {
			continue
		}
		out = append(out, DetectedFace{Box: boxes[i], Score: scores[i], Embedding: emb})
	}
	return out, nil
}

func (r *onnxRecognizer) EmbedLargestFace(img image.Image) ([]float32, image.Rectangle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b := img.Bounds()
	sample := imageSampler(img)
	boxes, kpsList, _, err := r.runSCRFD(sample, b.Dx(), b.Dy())
	if err != nil {
		return nil, image.Rectangle{}, err
	}
	if len(boxes) == 0 {
		return nil, image.Rectangle{}, fmt.Errorf("face: no face found in image")
	}
	best := 0
	bestArea := 0
	for i := range boxes {
		if a := boxes[i].Dx() * boxes[i].Dy(); a > bestArea {
			bestArea, best = a, i
		}
	}
	emb := r.embed(sample, kpsList[best])
	if emb == nil {
		return nil, image.Rectangle{}, fmt.Errorf("face: embedding failed")
	}
	return emb, boxes[best], nil
}

func (r *onnxRecognizer) Close() error {
	if r.scrfd != nil {
		r.scrfd.Destroy()
	}
	if r.scrfdIn != nil {
		r.scrfdIn.Destroy()
	}
	for _, t := range r.scrfdOut {
		if t != nil {
			t.Destroy()
		}
	}
	if r.arc != nil {
		r.arc.Destroy()
	}
	if r.arcIn != nil {
		r.arcIn.Destroy()
	}
	if r.arcOut != nil {
		r.arcOut.Destroy()
	}
	return nil
}
