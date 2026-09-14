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
- **A missing or unreadable model degrades to recording-only.** Cameras keep
  recording and the dashboard keeps working; only detection stops, with an
  error in the log naming the file.
- **MQTT coverage is mostly complete.** Events, availability, per-camera motion and stats can all be published, selected with `mqtt.publish`. Per-camera object-count topics are not published yet.
- **Test coverage is partial.** Configuration loading and validation, credential redaction in logs, MQTT topic construction, publishing and event payload mapping, the motion state machine, and API authentication are covered. The detection pipeline, storage and recorder are not. Contributions welcome.
- **Verified on NVIDIA GPUs, on CPU detection on x86-64, and on a Raspberry Pi 4.** On a 4 GB Pi 4 running 64-bit Raspberry Pi OS, the arm64 build ran the YOLOv8n model at 640×640 at about 0.5 seconds per detection on four threads, using under 100 MB of memory, and its output matched the x86-64 result. That is enough for roughly one camera at a low detection rate, not a multi-camera setup. The DeepStream backend is implemented but has had far less exercise than the ONNX Runtime path.

---

## Features

| Feature | Sentinel |
|---------|----------|
| Language | Go |
| Detection backend | ONNX Runtime / DeepStream |
| Storage | PostgreSQL + pgvector |
| Face recognition | Built-in (ArcFace + pgvector) |
| API | REST + MQTT |
| Home Assistant integration | MQTT event stream + REST API |
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

Everything below runs from `deploy/`, because that is where the compose file
looks for its configuration.

```bash
git clone https://github.com/bughatti/sentinel.git
cd sentinel/deploy

cp config.example.yaml config.yaml      # your cameras and database URL
cp go2rtc.example.yaml go2rtc.yaml      # restreaming; leave as-is if unsure
cp ../.env.example .env                 # set POSTGRES_PASSWORD, the stack will not start without it

# Set the SAME password inside config.yaml under database.url.
# The config file is literal YAML and does not read environment variables.
```

`mosquitto.conf` already ships in this directory, and `models/` is created by
the model step below. Both are mounted by the compose file, so leave them where
they are.

### 2. Add a detection model

Without a model Sentinel still starts and still records, but runs in
**recording-only mode** with no object detection, and logs an error naming the
file it could not load. Footage is never sacrificed for a configuration
mistake. To get detection, see
[Object Detection Models](#object-detection-models) below, then come back.

### 3. Start it

Sentinel publishes two images, so nobody downloads GPU libraries their
hardware cannot use. Pick the row that matches your machine. Every command runs
from `deploy/`.

| Your hardware | Image tag | Download | Detection runs on |
|---|---|---|---|
| No NVIDIA GPU: CPU only, AMD, Intel, Raspberry Pi 4 or 5 on 64-bit OS, other arm64 | `latest-cpu` | about 220 MB | CPU |
| NVIDIA GPU | `latest` | about 4.6 GB | GPU, with CUDA and TensorRT |

**Any machine, CPU detection** (amd64 or arm64):

```bash
docker compose up -d
```

**NVIDIA GPU.** Needs the NVIDIA Container Toolkit on the host:

```bash
docker compose -f docker-compose.yml -f docker-compose.gpu.yml up -d
```

**AMD or Intel GPU.** Detection stays on the CPU, but video decoding moves to
the GPU, which is usually the bigger share of CPU load with several cameras.
Add `hwaccel: vaapi` under each camera's `ffmpeg` section, then:

```bash
docker compose -f docker-compose.yml -f docker-compose.vaapi.yml up -d
```

Each command pulls a published image rather than building one. The dashboard
is at `http://localhost:5000`, and the first start takes 40 to 60 seconds while
the detector warms up and cameras connect. On an NVIDIA GPU with TensorRT
enabled, the very first start can take several minutes longer while the engine
is built; later starts reuse it.

The same `config.yaml` works on both images. With `use_gpu: true` on the CPU
image, or on a machine whose GPU is unavailable, detection falls back to the CPU
and logs a warning instead of switching detection off.

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

## Running on a Raspberry Pi

A Pi can run the whole stack for a single camera: the dashboard, events,
recordings, and person, car and animal detection. Use the `latest-cpu` image
and the plain `docker compose up -d` command.

**What has been tested.** On a 4 GB Raspberry Pi 4 with 64-bit Raspberry Pi OS,
object detection took about 0.5 seconds per frame on four threads, used under
100 MB of memory, and matched results from an x86-64 machine. Face recognition
and the full stack running for days have not been tested on a Pi yet.

**Setup that makes it work:**

- **Use a 64-bit OS.** Run `uname -m` on the Pi. It must print `aarch64`; the
  image will not start on 32-bit Raspberry Pi OS.
- **Use 4 GB of memory or more.** A Pi 5 has a noticeably faster CPU and more
  headroom than a Pi 4.
- **Detect on the camera's low-resolution stream, record the full one.** Most
  cameras offer a second stream at around 640×360. Motion checks decode the
  detect stream continuously on the CPU, which is cheap at low resolution and
  far too heavy at 4K. Recording copies the video without re-encoding it, so
  the full-resolution stream costs almost nothing to record.
- **Record to a USB SSD, not the SD card.** Continuous recording wears out SD
  cards. Put the recording folders and the database volume on the SSD.
- **Keep the detection rate low.** A `detect.fps` of 5 or less is plenty for one
  camera. Detection only runs when motion is seen, so a quiet camera costs
  little.

A single camera set up that way:

```yaml
cameras:
  driveway:
    ffmpeg:
      inputs:
        - path: "rtsp://user:pass@192.168.1.100:554/stream2"   # low-res substream
          roles: [detect]
        - path: "rtsp://user:pass@192.168.1.100:554/stream1"   # full-res main stream
          roles: [record]
    detect:
      width: 640
      height: 360
      fps: 5
```

Substream paths vary by camera brand, so check your camera's documentation for
the right one.

**Face recognition on a Pi is untested.** Its models are much heavier than the
object model, so expect a delay of a second or more before a name appears, and
leave it off if detection falls behind.

**Tried Sentinel on a Pi or another arm64 board?** Please
[open an issue](https://github.com/bughatti/sentinel/issues) with the board,
memory, OS, camera count and what worked, including face recognition timings
if you enabled it. Those reports are how the untested parts above get filled in.

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

You choose what Sentinel publishes with the `publish` list under `mqtt` in your config. The prefix every topic hangs off is set by `topic_prefix`.

| Entry | Topic | What arrives |
|---|---|---|
| `events` | `<prefix>/events` | Every event, as a before/after payload |
| `availability` | `<prefix>/available` | Retained `online` / `offline` |
| `motion` | `<prefix>/<camera>/motion` | Retained `ON` / `OFF`, sent on state changes only |
| `stats` | `<prefix>/stats` | System stats, on the `stats_interval` timer |

Leaving `publish` out of your config keeps the long-standing default of `events` and `availability`, so upgrading changes nothing until you ask it to. An empty list publishes nothing. A misspelled entry is ignored and logged with the list of valid names, rather than failing silently.

Two things worth knowing. Motion is edge-triggered, so you get a message when motion starts and another when it stops, not one per frame, and because it is retained a subscriber that connects later still learns the current state. Publishing never blocks recording: messages are queued, and if the broker stalls they are dropped with a warning rather than holding up the camera pipeline.

The per-camera object-count topics are defined in the code but still not published, so integrations that depend on those will see nothing there.

**In `configuration.yaml`:**


Point your home automation platform's MQTT integration at the same broker and topic prefix Sentinel publishes on.

The MQTT topic prefix is configurable. Several home automation integrations discover cameras by subscribing to a fixed prefix, so if you already run one, set `topic_prefix` to whatever prefix it expects and point it at Sentinel's API URL. The event stream then works unchanged, with no adapter in between.

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

Sentinel needs an ONNX detection model and a matching label file. They are not
bundled, because the model is large and its licence is not ours to redistribute.

There is no official pre-exported ONNX build of YOLOv8, so export one yourself.
It takes a minute and only has to be done once:

```bash
pip install ultralytics
yolo export model=yolov8n.pt format=onnx opset=12    # downloads the weights, writes yolov8n.onnx

mkdir -p models
mv yolov8n.onnx models/

# 80 COCO class names, one per line, in the order the model was trained on
wget https://raw.githubusercontent.com/pjreddie/darknet/master/data/coco.names \
  -O models/coco.txt
```

That produces exactly what the shipped `config.example.yaml` already expects, so
no configuration change is needed:

```yaml
detector:
  type: onnx
  use_gpu: true
  onnx:
    model_path: "/models/yolov8n.onnx"
    label_path: "/models/coco.txt"
    threshold: 0.50
```

`deploy/models/` is mounted read-only into the container at `/models`. Larger
models work the same way: export `yolov8s`, `yolov8m` or similar and point
`model_path` at it. Accuracy improves, throughput drops.

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
