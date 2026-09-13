# Sentinel NVR

[![Build](https://github.com/bughatti/sentinel/actions/workflows/ci.yml/badge.svg)](https://github.com/bughatti/sentinel/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Container](https://img.shields.io/badge/ghcr.io-bughatti%2Fsentinel-blue)](https://github.com/bughatti/sentinel/pkgs/container/sentinel)
[![Go Version](https://img.shields.io/badge/Go-1.23-00ADD8.svg)](https://golang.org)

**Sentinel NVR — A high-performance, GPU-accelerated Network Video Recorder built in Go. REST API, PostgreSQL storage, NVIDIA DeepStream support.**

Sentinel trades Python for Go and adds first-class PostgreSQL persistence, pluggable AI backends (ONNX Runtime, NVIDIA DeepStream), and a modular architecture designed to scale beyond a single host. Its REST API and MQTT event stream use stable, documented shapes, so existing home automation tooling can consume them, with the limits described under Project status.

## Project status

Honest state of things, so you can judge whether to run it:

- **In daily use on one deployment**, eight cameras with GPU detection, running continuously.
- **MQTT coverage is partial.** The REST API works, and `<prefix>/events` and `<prefix>/available` are published. Stats and per-camera motion or object-count topics are not published yet.
- **Test coverage is thin.** The credential redaction and configuration loading paths are covered; most of the pipeline is not. Contributions welcome.
- **Verified on NVIDIA GPUs and CPU decoding.** The DeepStream backend is implemented but has had far less exercise than the ONNX Runtime path.

---

## Features

| Feature | Sentinel |
|---------|----------|
| Language | Go |
| Detection backend | ONNX Runtime / DeepStream |
| Storage | PostgreSQL + pgvector |
| Face recognition | Built-in (ArcFace + pgvector) |
| API | REST + MQTT |
| Home Assistant integration | Drop-in (same topics + API) |
| Config hot-reload | Yes (fsnotify debounce) |
| Multi-camera batching | Yes (dynamic batch assembly) |
| Recording format | MP4 segments (HLS-compatible) |
| Zone detection | Polygon + inertia |
| Object tracking | IoU centroid tracker |
| Motion detection | Background subtraction (Go) |
| WebSocket events | Yes |
| Docker / GPU override | Yes |

---

## Quick Start

### 1. Clone and configure

```bash
git clone https://github.com/bughatti/sentinel.git
cd sentinel
cp deploy/config.example.yaml config.yaml
cp .env.example .env          # set POSTGRES_PASSWORD; the stack will not start without it
# Edit config.yaml — set database.url and add your cameras
```

### 2. Start with Docker Compose

```bash
docker compose -f deploy/docker-compose.yml up -d
```

The UI is available at `http://localhost:5000`.

### 3. GPU acceleration (optional)

```bash
docker compose \
  -f deploy/docker-compose.yml \
  -f deploy/docker-compose.gpu.yml \
  up -d
```

---

## Configuration

The configuration file is YAML. Every field has a sensible default; the only required sections are `database.url` and at least one camera.

```yaml
database:
  url: "postgresql://sentinel:sentinel@postgres:5432/sentinel?sslmode=disable"

cameras:
  front_door:
    ffmpeg:
      inputs:
        - path: "rtsp://admin:pass@192.168.1.100:554/stream1"
          roles: [detect, record]
    detect:
      width: 1280
      height: 720
      fps: 5
```

See [`deploy/config.example.yaml`](deploy/config.example.yaml) for the full annotated reference.

---

## API Reference

Sentinel exposes a REST API that existing home automation tooling can consume without changes.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/events` | List events with filters |
| GET | `/api/events/{id}` | Get single event |
| GET | `/api/events/{id}/snapshot.jpg` | Event snapshot image |
| GET | `/api/events/{id}/clip.mp4` | Event clip video |
| POST | `/api/events/{id}/false_positive` | Mark false positive |
| DELETE | `/api/events/{id}` | Delete event |
| GET | `/api/recordings` | List recording segments |
| GET | `/api/recordings/summary` | Per-day recording summary |
| GET | `/api/cameras` | List all cameras |
| GET | `/api/cameras/{name}/latest-frame` | Latest JPEG frame |
| GET | `/api/stats` | System stats |
| GET | `/ws` | WebSocket event stream |
| GET | `/vod/{date}/{hour}/{camera}/index.m3u8` | HLS playlist |

---

## Home Assistant Integration

Sentinel publishes two MQTT topics today: `<prefix>/events` (every event, as a before/after payload) and `<prefix>/available` (retained online/offline). The prefix is set by `topic_prefix` in your config.

Be aware of the gap: `<prefix>/stats`, `<prefix>/{camera}/motion` and the per-camera object-count topics are defined in the code but not yet published, so integrations that depend on them will see nothing there.

**In `configuration.yaml`:**


Point your home automation platform's MQTT integration at the same broker and topic prefix Sentinel publishes on.

If you use Home Assistant's Frigate integration, set `topic_prefix: frigate` in your config and point that integration at Sentinel's API URL. It subscribes to topics under that prefix, so the event stream works unchanged. That integration is named after another project, which is why the name appears here and nowhere else in Sentinel.

---

## Eufy Camera Setup

Eufy cameras use a proprietary cloud protocol. Sentinel accesses them via [go2rtc](https://github.com/AlexxIT/go2rtc):

1. Add your Eufy stream to `deploy/go2rtc.yaml`:

```yaml
streams:
  eufy_backyard:
    - "eufy://user@email.com:password@serialnumber"
```

2. Reference the go2rtc RTSP URL in your Sentinel config:

```yaml
cameras:
  eufy_backyard:
    ffmpeg:
      inputs:
        - path: "rtsp://go2rtc:8554/eufy_backyard"
          roles: [detect, record]
```

---

## Object Detection Models

Sentinel uses YOLOv9 in ONNX format. Download a pre-exported model:

```bash
# YOLOv9-s (small — recommended for CPU/low-power GPU)
wget https://github.com/ultralytics/assets/releases/download/v0.0.0/yolov9s.onnx \
  -O deploy/models/yolov9s.onnx

# COCO class names
wget https://raw.githubusercontent.com/pjreddie/darknet/master/data/coco.names \
  -O deploy/models/coco.names
```

Then in `config.yaml`:

```yaml
detector:
  type: onnx
  onnx:
    model_path: "/models/yolov9s.onnx"
    label_path: "/models/coco.names"
    threshold: 0.50
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Sentinel NVR                        │
│                                                         │
│  CameraManager                                          │
│  ├── CameraWorker (per camera)                          │
│  │   ├── captureLoop  → FFmpeg → Frame channel          │
│  │   ├── motionLoop   → Background subtraction          │
│  │   └── recordLoop   → FFmpeg segment writer           │
│  └── batchAssembler → BatchCh                           │
│                                                         │
│  detectionLoop                                          │
│  └── Detector (ONNX / CPU / DeepStream)                 │
│      └── PipelineManager                                │
│          └── Pipeline (per camera)                      │
│              ├── Tracker (IoU centroid)                 │
│              ├── Zone filtering                         │
│              ├── Event FSM (idle/active/cooldown)       │
│              └── EventBus.Publish()                     │
│                                                         │
│  EventBus (fanout)                                      │
│  ├── MQTT Publisher (<prefix>/events)                    │
│  ├── WebSocket Hub  (/ws)                               │
│  └── SnapshotSaver  (JPEG to disk)                      │
│                                                         │
│  RecorderManager (segment → Postgres)                   │
│  RetentionManager (hourly cleanup)                      │
│  API Server (chi router + embed UI)                     │
└─────────────────────────────────────────────────────────┘
```

---

## Contributing

Contributions are welcome. Please:

1. Fork the repository and create a feature branch.
2. Follow standard Go conventions (`gofmt`, `go vet`, `golangci-lint`).
3. Add tests for new functionality.
4. Open a pull request with a clear description.

### Development setup

```bash
# Prerequisites: Go 1.23+, ffmpeg, PostgreSQL (pgvector), Docker

# Install ONNX Runtime (Linux)
ORT_VERSION=1.18.1
curl -fsSL "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/onnxruntime-linux-x64-${ORT_VERSION}.tgz" \
  | sudo tar xzf - -C /usr/local --strip-components=1
sudo ldconfig

# Run tests
go test ./...

# Run with a local config
go run ./cmd/sentinel --config config.yaml
```

---

## License

MIT — see [LICENSE](LICENSE).

---

## Acknowledgements

- The open-source NVR community — inspiration for the MQTT schema and API design
- [go2rtc](https://github.com/AlexxIT/go2rtc) — stream gateway
- [YOLOv9](https://github.com/WongKinYiu/yolov9) — detection model
- [ONNX Runtime](https://github.com/microsoft/onnxruntime) — inference engine
