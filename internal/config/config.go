// Package config handles loading, validation, and hot-reloading of Sentinel NVR configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SentinelConfig is the top-level configuration structure.
type SentinelConfig struct {
	Database        DatabaseConfig          `yaml:"database"`
	MQTT            MQTTConfig              `yaml:"mqtt"`
	Detector        DetectorConfig          `yaml:"detector"`
	Objects         ObjectsConfig           `yaml:"objects"`
	Motion          MotionConfig            `yaml:"motion"`
	Record          RecordConfig            `yaml:"record"`
	Snapshots       SnapshotsConfig         `yaml:"snapshots"`
	FaceRecognition FaceRecognitionConfig   `yaml:"face_recognition"`
	Storage         StorageConfig           `yaml:"storage"`
	API             APIConfig               `yaml:"api"`
	Cameras         map[string]CameraConfig `yaml:"cameras"`
	Go2RTC          Go2RTCConfig            `yaml:"go2rtc"`
	LogLevel        string                  `yaml:"log_level"`
}

// DatabaseConfig holds PostgreSQL connection parameters.
type DatabaseConfig struct {
	URL      string `yaml:"url"`
	MaxConns int32  `yaml:"max_conns"`
}

// MQTTConfig holds MQTT broker connection parameters.
type MQTTConfig struct {
	Host          string        `yaml:"host"`
	Port          int           `yaml:"port"`
	User          string        `yaml:"user"`
	Password      string        `yaml:"password"`
	TopicPrefix   string        `yaml:"topic_prefix"`
	ClientID      string        `yaml:"client_id"`
	Enabled       bool          `yaml:"enabled"`
	TLS           bool          `yaml:"tls"`
	ReconnectWait time.Duration `yaml:"reconnect_wait"`

	// Publish selects which topic families are sent to the broker. Valid
	// entries are "events", "availability", "motion" and "stats". Leaving it
	// unset keeps the long-standing behaviour of publishing events and
	// availability only, so upgrading changes nothing until you ask it to.
	Publish []string `yaml:"publish"`

	// StatsInterval is how often the stats topic is published. Ignored unless
	// "stats" is listed in Publish.
	StatsInterval time.Duration `yaml:"stats_interval"`
}

// DetectorConfig selects and configures the object detector backend.
type DetectorConfig struct {
	Type        string           `yaml:"type"` // "onnx", "deepstream", "cpu"
	ONNX        ONNXConfig       `yaml:"onnx"`
	DeepStream  DeepStreamConfig `yaml:"deepstream"`
	NumThreads  int              `yaml:"num_threads"`
	UseGPU      bool             `yaml:"use_gpu"`
	UseTensorRT bool             `yaml:"use_tensorrt"` // ONNX Runtime TensorRT EP (needs libnvinfer10); falls back to CUDA
	GPUDeviceID int              `yaml:"gpu_device_id"`
	BatchSize   int              `yaml:"batch_size"` // frames per GPU inference; must equal the model's ONNX batch dim
}

// ONNXConfig holds ONNX Runtime detector parameters.
type ONNXConfig struct {
	ModelPath   string  `yaml:"model_path"`
	LabelPath   string  `yaml:"label_path"`
	Threshold   float32 `yaml:"threshold"`
	InputWidth  int     `yaml:"input_width"`
	InputHeight int     `yaml:"input_height"`
}

// DeepStreamConfig holds NVIDIA DeepStream bridge parameters.
type DeepStreamConfig struct {
	GRPCAddr   string `yaml:"grpc_addr"`
	PipelineID string `yaml:"pipeline_id"`
}

// ObjectsConfig holds global object detection defaults.
type ObjectsConfig struct {
	Track   []string                `yaml:"track"`
	Filters map[string]ObjectFilter `yaml:"filters"`
}

// ObjectFilter defines per-label filtering criteria.
type ObjectFilter struct {
	MinArea   float64 `yaml:"min_area"`   // minimum bounding-box area as fraction of frame
	MaxArea   float64 `yaml:"max_area"`   // maximum bounding-box area as fraction of frame
	MinScore  float32 `yaml:"min_score"`  // minimum detection confidence
	MinWidth  int     `yaml:"min_width"`  // minimum pixel width
	MinHeight int     `yaml:"min_height"` // minimum pixel height
	Mask      [][]int `yaml:"mask"`       // polygon to exclude, list of [x, y] pairs
}

// MotionConfig controls the motion detection algorithm.
type MotionConfig struct {
	Enabled         bool    `yaml:"enabled"`
	Threshold       float64 `yaml:"threshold"`        // pixel difference threshold 0-255
	Alpha           float64 `yaml:"alpha"`            // background model learning rate 0-1
	ContourArea     float64 `yaml:"contour_area"`     // minimum contiguous motion area
	FrameAlpha      float64 `yaml:"frame_alpha"`      // frame smoothing alpha
	LightningThresh float64 `yaml:"lightning_thresh"` // score above which global flash is assumed
	// DetectWithoutMotion, when true, sends EVERY captured frame to the object
	// detector even while motion detection is enabled (motion still produces
	// motion events + segment annotations). Default false keeps the motion gate,
	// which saves GPU but can silently miss slow/low-contrast objects that never
	// trip motion. Set true to trade GPU for guaranteed detection coverage.
	DetectWithoutMotion bool `yaml:"detect_without_motion"`
}

// RecordConfig controls continuous and event-triggered recording.
type RecordConfig struct {
	Enabled         bool              `yaml:"enabled"`
	Retain          RetentionConfig   `yaml:"retain"`
	Events          EventRecordConfig `yaml:"events"`
	SegmentDuration int               `yaml:"segment_duration"` // seconds per segment file
	OutputPattern   string            `yaml:"output_pattern"`   // strftime-style path pattern
}

// RetentionConfig controls how long recordings are kept.
type RetentionConfig struct {
	Days float64 `yaml:"days"`
	Mode string  `yaml:"mode"` // "all", "motion", "active_objects"
}

// EventRecordConfig controls pre/post recording for detected events.
type EventRecordConfig struct {
	PreCapture  int             `yaml:"pre_capture"`  // seconds before event
	PostCapture int             `yaml:"post_capture"` // seconds after event
	Retain      RetentionConfig `yaml:"retain"`
	Required    []string        `yaml:"required"` // labels that must be present to keep
}

// SnapshotsConfig controls snapshot saving for events.
type SnapshotsConfig struct {
	Enabled    bool    `yaml:"enabled"`
	Clean      bool    `yaml:"clean"`     // save without bounding boxes
	Timestamp  bool    `yaml:"timestamp"` // draw timestamp on snapshot
	BBoxes     bool    `yaml:"bboxes"`    // draw bounding boxes
	Crop       bool    `yaml:"crop"`      // save cropped-to-object snapshot
	Required   bool    `yaml:"required"`  // only save when object in required zone
	Quality    int     `yaml:"quality"`   // JPEG quality 1-100
	Height     int     `yaml:"height"`    // output height in pixels (0 = native)
	RetainDays float64 `yaml:"retain_days"`
}

// FaceRecognitionConfig enables face embedding and identity lookup.
type FaceRecognitionConfig struct {
	Enabled          bool    `yaml:"enabled"`
	DetectorModel    string  `yaml:"detector_model"`    // SCRFD ONNX (buffalo_l detection, 5 landmarks)
	RecognitionModel string  `yaml:"recognition_model"` // ArcFace ONNX (buffalo_l w600k_r50, 512-d)
	ModelPath        string  `yaml:"model_path"`        // legacy/unused
	Threshold        float64 `yaml:"threshold"`         // cosine-similarity match threshold (0..1)
	MinFaceSize      int     `yaml:"min_face_size"`     // skip faces smaller than this (px)
}

// StorageConfig defines where media files are written.
type StorageConfig struct {
	RecordingsDir string `yaml:"recordings_dir"`
	SnapshotsDir  string `yaml:"snapshots_dir"`
	ClipsDir      string `yaml:"clips_dir"`
	ExportsDir    string `yaml:"exports_dir"`
	TmpDir        string `yaml:"tmp_dir"`
}

// APIConfig controls the HTTP API and embedded UI.
type APIConfig struct {
	Listen      string       `yaml:"listen"` // host:port
	AuthEnabled bool         `yaml:"auth_enabled"`
	APIKey      string       `yaml:"api_key"`
	CORSOrigins []string     `yaml:"cors_origins"`
	TLS         APITLSConfig `yaml:"tls"`
}

// APITLSConfig optionally enables HTTPS on the API server.
type APITLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// CameraConfig holds per-camera settings.
type CameraConfig struct {
	Name      string                `yaml:"name"`
	Enabled   bool                  `yaml:"enabled"`
	FFmpeg    FFmpegConfig          `yaml:"ffmpeg"`
	Detect    DetectConfig          `yaml:"detect"`
	Motion    *MotionConfig         `yaml:"motion,omitempty"`
	Record    *RecordConfig         `yaml:"record,omitempty"`
	Snapshots *SnapshotsConfig      `yaml:"snapshots,omitempty"`
	Objects   *ObjectsConfig        `yaml:"objects,omitempty"`
	Zones     map[string]ZoneConfig `yaml:"zones"`
	OnvifHost string                `yaml:"onvif_host"`
	OnvifPort int                   `yaml:"onvif_port"`
	OnvifUser string                `yaml:"onvif_user"`
	OnvifPass string                `yaml:"onvif_pass"`
	Timestamp bool                  `yaml:"timestamp"`
}

// FFmpegConfig holds camera-level FFmpeg stream settings.
type FFmpegConfig struct {
	Inputs        []FFmpegInput `yaml:"inputs"`
	GlobalArgs    []string      `yaml:"global_args"`
	HWAccel       string        `yaml:"hwaccel"` // e.g. "cuda", "vaapi", "videotoolbox"
	HWAccelDevice string        `yaml:"hwaccel_device"`
}

// FFmpegInput describes one input stream and its roles.
type FFmpegInput struct {
	Path   string   `yaml:"path"`
	Roles  []string `yaml:"roles"` // "detect", "record", "clips", "audio"
	Global []string `yaml:"global_args"`
}

// DetectConfig controls the resolution and rate used for detection.
type DetectConfig struct {
	Enabled    bool             `yaml:"enabled"`
	Width      int              `yaml:"width"`
	Height     int              `yaml:"height"`
	FPS        int              `yaml:"fps"`
	MaxDisapp  int              `yaml:"max_disappeared"` // frames before track is dropped
	Stationary StationaryConfig `yaml:"stationary"`
}

// StationaryConfig controls how long a stationary object is re-detected.
type StationaryConfig struct {
	Interval  int `yaml:"interval"`  // re-detect every N frames
	Threshold int `yaml:"threshold"` // frames before declaring stationary
	MaxFrames struct {
		Default int            `yaml:"default"`
		Objects map[string]int `yaml:"objects"`
	} `yaml:"max_frames"`
}

// ZoneConfig defines a named region inside a camera frame.
type ZoneConfig struct {
	Coordinates string                  `yaml:"coordinates"` // "x1,y1,x2,y2,..." comma list
	Objects     []string                `yaml:"objects"`
	Filters     map[string]ObjectFilter `yaml:"filters"`
	Inertia     int                     `yaml:"inertia"` // frames object must be in zone to trigger
}

// Go2RTCConfig points to a running go2rtc instance for proxied streams.
type Go2RTCConfig struct {
	URL string `yaml:"url"` // e.g. "http://localhost:1984"
}

// Config is an alias kept for cleaner imports.
type Config = SentinelConfig

// Load reads a YAML config file from path, applies defaults, and validates it.
func Load(path string) (*Config, error) {
	path = filepath.Clean(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	cfg := defaults()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	// Inherit camera-level settings from globals where the camera hasn't
	// specified its own.
	for name, cam := range cfg.Cameras {
		if cam.Motion == nil {
			m := cfg.Motion
			cam.Motion = &m
		}
		if cam.Record == nil {
			r := cfg.Record
			cam.Record = &r
		}
		if cam.Snapshots == nil {
			s := cfg.Snapshots
			cam.Snapshots = &s
		}
		if cam.Objects == nil {
			o := cfg.Objects
			cam.Objects = &o
		}
		if cam.Name == "" {
			cam.Name = name
		}
		cfg.Cameras[name] = cam
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaults returns a Config pre-populated with sensible defaults.
func defaults() *Config {
	return &Config{
		LogLevel: "info",
		Database: DatabaseConfig{
			MaxConns: 10,
		},
		MQTT: MQTTConfig{
			Port:          1883,
			TopicPrefix:   "sentinel",
			ClientID:      "sentinel-nvr",
			ReconnectWait: 5 * time.Second,
			StatsInterval: 60 * time.Second,
			// Publish is deliberately left nil: nil means "not configured"
			// and resolves to the default set, while an explicitly empty
			// list in YAML means "publish nothing".
		},
		Detector: DetectorConfig{
			Type:        "cpu",
			NumThreads:  2,
			UseGPU:      false,
			GPUDeviceID: 0,
			ONNX: ONNXConfig{
				Threshold:   0.5,
				InputWidth:  640,
				InputHeight: 640,
			},
		},
		Objects: ObjectsConfig{
			Track:   []string{"person", "car", "dog", "cat"},
			Filters: make(map[string]ObjectFilter),
		},
		Motion: MotionConfig{
			Enabled:         true,
			Threshold:       25,
			Alpha:           0.01,
			ContourArea:     1.5, // minimum % of pixels that must change to count as motion
			FrameAlpha:      0.01,
			LightningThresh: 15, // suppress motion when >15% of pixels change at once
		},
		Record: RecordConfig{
			Enabled:         true,
			SegmentDuration: 10,
			OutputPattern:   "",
			Retain: RetentionConfig{
				Days: 3,
				Mode: "all",
			},
			Events: EventRecordConfig{
				PreCapture:  5,
				PostCapture: 5,
				Retain: RetentionConfig{
					Days: 10,
					Mode: "active_objects",
				},
			},
		},
		Snapshots: SnapshotsConfig{
			Enabled:    true,
			BBoxes:     true,
			Quality:    70,
			RetainDays: 10,
		},
		FaceRecognition: FaceRecognitionConfig{
			Threshold:   0.6,
			MinFaceSize: 20,
		},
		Storage: StorageConfig{
			RecordingsDir: "/media/recordings",
			SnapshotsDir:  "/media/snapshots",
			ClipsDir:      "/media/clips",
			ExportsDir:    "/media/exports",
			TmpDir:        "/tmp/sentinel",
		},
		API: APIConfig{
			Listen: "0.0.0.0:5000",
			// No cross-origin browser access unless listed; the dashboard is
			// same-origin and needs none. See corsMiddleware.
			CORSOrigins: nil,
		},
		Go2RTC: Go2RTCConfig{
			URL: "http://localhost:1984",
		},
		Cameras: make(map[string]CameraConfig),
	}
}

// Validate checks that required fields are present and values are in range.
func (c *Config) Validate() error {
	var errs []string

	if c.Database.URL == "" {
		errs = append(errs, "database.url is required")
	}

	if len(c.Cameras) == 0 {
		errs = append(errs, "at least one camera must be defined")
	}

	for name, cam := range c.Cameras {
		if len(cam.FFmpeg.Inputs) == 0 {
			errs = append(errs, fmt.Sprintf("camera %q: ffmpeg.inputs is required", name))
			continue
		}
		hasDetect := false
		for _, in := range cam.FFmpeg.Inputs {
			if in.Path == "" {
				errs = append(errs, fmt.Sprintf("camera %q: ffmpeg input path must not be empty", name))
			}
			for _, role := range in.Roles {
				if role == "detect" {
					hasDetect = true
				}
			}
		}
		if !hasDetect {
			errs = append(errs, fmt.Sprintf("camera %q: at least one ffmpeg input must have role 'detect'", name))
		}
		if cam.Detect.Width <= 0 {
			errs = append(errs, fmt.Sprintf("camera %q: detect.width must be > 0", name))
		}
		if cam.Detect.Height <= 0 {
			errs = append(errs, fmt.Sprintf("camera %q: detect.height must be > 0", name))
		}
		if cam.Detect.FPS <= 0 {
			errs = append(errs, fmt.Sprintf("camera %q: detect.fps must be > 0", name))
		}
	}

	if c.API.AuthEnabled && c.API.APIKey == "" {
		errs = append(errs, "api.api_key is required when api.auth_enabled is true")
	}

	validLogLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLogLevels[strings.ToLower(c.LogLevel)] {
		errs = append(errs, fmt.Sprintf("log_level %q is invalid; must be debug, info, warn, or error", c.LogLevel))
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation errors:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}
