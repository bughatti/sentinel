# Sentinel NVR — Architecture & Engineering Notes

> Custom Go network video recorder, built as a **drop-in the reference implementation replacement** (integration-friendly REST + MQTT so existing Home Assistant integrations work unmodified). Object detection via ONNX Runtime YOLOv8n on GPU, PostgreSQL+pgvector storage, go2rtc for streams.
> Module `github.com/bughatti/sentinel` · Go 1.23 (NAS host has 1.26) · MIT.
> _Notes compiled 2026-07-26 from a full source read; file:line references are to this repository._

---

## ⚑ 2026-07-26 UPDATE — many "known issues" below are now FIXED (this section supersedes them)

The bulk of §14/§16 "known issues" were **resolved in a big 2026-07-26 session**. Current state:
- **FIXED:** parked-car flood (movement-gated events), snapshots (were never saved), event clips (never implemented), **event/snapshot/clip retention** (nothing pruned events before — caused the 233k bloat), recordings-tab playback (new `VodPlayer.svelte` + playlist-from-disk), WebSocket live updates (`{type,payload}`), `/api/config` + `/api/stats` + camera `online` (were stubs), motion-gate toggle, object area/mask filters, hot-reload (log_level live), build version.
- **FACE RECOGNITION now IMPLEMENTED** (was scaffold-only): `internal/face/` — SCRFD detect + 5-pt align + ArcFace 512-d embed (ONNX) → pgvector cosine match → `sub_label`; enroll API + Faces UI. Models on NAS `/media/sentinel/models/{scrfd_10g,arcface_w600k_r50}.onnx`.
- **TensorRT ENABLED** (`detector.use_tensorrt`): ORT TensorRT EP fp16 → inference 44→2.7ms (16×) on the RTX 5080. **NVDEC + `scale_cuda`** GPU decode+resize (`ffmpeg.hwaccel: cuda`, 7/8 cams — reolink's 896×512 sub-stream can't NVDEC so it's CPU-decode). Detector is now **batch- + input-size-agnostic** with **true batched inference** + a preprocess **fast-path**.
- **DeepStream** full GStreamer pipeline: assessed NOT worth building (TensorRT delivers the win).
- **★ PENDING:** max-detail model (YOLO11/bigger) — attempted, yolo11 produced near-zero class scores in our inference (cause unknown), reverted to **yolov8n@640**. Model is still YOLOv8n (code comments say "YOLOv9" — harmless).

Everything below the line is the original full architecture reference (still accurate for structure; treat its "known issues" as historical unless re-confirmed).

---

## 0. TL;DR / most important facts

- **Runs in Docker Compose on the NAS** (containers `sentinel`, `sentinel-postgres`, `sentinel-go2rtc`, `sentinel-mosquitto`). NOT a host binary — older notes were wrong.
- **8 cameras**: 5 Dahua/Reolink RTSP (sub-stream=detect, main=record) + 3 eufy (single stream, relayed through go2rtc).
- **Data flow:** RTSP → ffmpeg decode to BGR24 → **motion gate** → batch → **ONNX YOLOv8n (GPU)** → **IoU tracker** → per-camera **pipeline** (movement-gated events) → snapshot + clip → EventBus → MQTT + WebSocket.
- **Two separate recording concepts:** (1) *continuous* 10-second segments recorded 24/7 (the "Recordings" tab, retained 3 days) and (2) *event clips* cut from those segments on event end (the "Events" tab, kept forever).
- **Big surprises found in the code** (see §14 Known Issues): face recognition is **scaffold-only, not implemented**; **events/snapshots/clips never auto-expire** (only continuous recordings do — this is why the DB bloated with 233k car events); **motion gates object detection**; **no TensorRT** (CUDA only); live UI real-time updates are broken (**WS payload shape mismatch**); the model is **YOLOv8n** though the code is named "YOLOv9".

---

## 1. Deployment & runtime

### Containers (as deployed by `deploy/docker-compose.yml`)
| Container | Image | Purpose |
|---|---|---|
| `sentinel` | built from `deploy/Dockerfile` | the NVR (API :5000, detection, recording) |
| `sentinel-postgres` | `pgvector/pgvector:pg16` | events/recordings DB + pgvector (loopback `127.0.0.1:5432`) |
| `sentinel-go2rtc` | `alexxit/go2rtc:latest` | camera stream restream/fan-out (`network_mode: host`, API :1984, RTSP :8554) |
| `sentinel-mosquitto` | `eclipse-mosquitto:2` | MQTT broker :1883 (the reference implementation-compat topics), anonymous allowed |

Host media at **`/media/sentinel/{recordings,snapshots,clips,exports,models}`** → container `/recordings`, `/snapshots`, `/clips`, `/exports`, `/models`. `config.yaml` mounted at `/etc/sentinel/config.yaml`. `TZ=America/Denver`. GPU via nvidia runtime (`count: all`). Healthcheck `curl /healthz` (30s, start-period 60s).

> **Repo divergence to know:** the *running* compose (NAS repo root `docker-compose.yml`) maps `/media/sentinel/* → /recordings` etc. — matching `config.yaml`'s storage dirs. The template `deploy/docker-compose.yml` in the repo instead maps to `/media/recordings` and would NOT match the config — don't deploy from the template as-is. The laptop repo has `deploy/` but **not** the authoritative root `docker-compose.yml`; the NAS repo is the source of truth for the compose + `config.yaml`.

### Build & deploy (hard-won 2026-07-26)
- **Go source:** edit → `go build ./...` → `docker compose build sentinel && docker compose up -d sentinel`. **Startup takes ~40–60s** (GPU detector warmup + 8 cameras reconnecting); `curl localhost:5000/healthz` returns `000` until ready then `200` — poll it, don't assume it crashed. "detector batch channel full — dropping batch" spam during warmup is normal/transient (0 in steady state).
- **Frontend:** source is **on the laptop** at `web/` (Vite + Svelte 5 runes + Tailwind). Vite `outDir: '../internal/api/webdist'`, `emptyOutDir:true` → `npm run build` writes straight into `internal/api/webdist`, which the Go binary serves via `//go:embed webdist`. Deploy: build on laptop → `rm -rf` NAS `internal/api/webdist` → `pscp -r` laptop webdist → NAS → `docker compose build sentinel && up -d`. (Clear first — an old build once left a stray nested `webdist/webdist`.) App uses **hash routing** (`#/recordings`) so the plain file server needs no SPA fallback.

### Dockerfile (multi-stage, CGO + CUDA)
- **Builder** `golang:1.23-bookworm`: downloads **ONNX Runtime v1.20.1 GPU** into `/opt/ort`; builds with `CGO_ENABLED=1`, `CGO_CFLAGS/LDFLAGS` pointing at ORT; `-ldflags "…app.Version=dev"` (**version hardcoded to `dev`** — all images report `dev`). `mkdir internal/api/webdist` so the embed target exists.
- **Runtime** `nvidia/cuda:12.6.3-cudnn-runtime-ubuntu24.04`: installs `ffmpeg`; copies `libonnxruntime*.so*` → `/usr/local/lib` + `ldconfig` (the detector hardcodes `ort.SetSharedLibraryPath("/usr/local/lib/libonnxruntime.so")`). Binary → `/usr/local/bin/sentinel`.

### Startup sequence (`cmd/sentinel/main.go`, cobra)
`sentinel --config … [--log-level]`; subcommands `version`, `validate`. `run()` → resolve config path (flag → `/etc/sentinel/config.yaml` → `./config.yaml`; **note `SENTINEL_CONFIG` env is NOT read** — compose passes `--config`) → `config.Load` → slog JSON logger → config watcher (**hot-reload fires but only logs — `UpdateConfig` is a TODO, effectively a no-op**) → `app.New(cfg)` → `a.Start(ctx)` → block on SIGTERM/SIGINT → `a.Stop` (30s timeout).

### App wiring (`internal/app/app.go`, DI, no globals)
`New`: Storage(EnsureDirs) → DB(`db.New` runs migrations) → `events.NewStore` → EventBus → detector(`buildDetector`) → camera.Manager → **snapshot.Saver (built before pipelines so it can be injected)** → pipeline.Manager(one per camera, gets Saver+storage) → motion.Manager → recorder.Manager → MQTT(only if enabled&&host) → api.Server.
`Start` goroutines: detector.Warmup → cameras.Start → per-camera motion.Run → RetentionManager(hourly) → detectionLoop → recordingDispatchLoop → mqttFanout → API server.
`Stop`: API → pipeline.FlushAll → cameras.Stop → recorder.Wait → MQTT Disconnect(LWT offline) → detector.Close → db.Close.

---

## 2. End-to-end data flow

```
RTSP camera
  ├─ detect input (sub-stream, low-res)  ──ffmpeg──▶ raw BGR24 frames @ detect W/H/fps
  │      └─▶ motionLoop (background-subtraction)
  │             ├─ motion? NO  → frame DROPPED (not detected)      ← motion GATES detection
  │             └─ motion? YES → fanin ─▶ batchAssembler ─▶ BatchCh
  │                                             └─▶ detector.Detect() [ONNX YOLOv8n GPU]
  │                                                    └─▶ pipeline.Process(frame, dets)
  │                                                           ├─ filter (whitelist + min_score/size)
  │                                                           ├─ IoU tracker → TrackedDetection
  │                                                           ├─ zones (centroid-in-polygon + inertia)
  │                                                           └─ event state machine (movement-gated)
  │                                                                  ├─ InsertEvent + saveSnapshot (JPEG)
  │                                                                  ├─ UpdateEvent (each frame)
  │                                                                  └─ endEvent → saveClip (ffmpeg concat)
  │                                                                         └─▶ EventBus
  │                                                                               ├─▶ MQTT  <prefix>/events
  │                                                                               └─▶ WebSocket /ws
  └─ record input (main-stream, high-res) ──ffmpeg -c copy -f segment──▶ 10s .mp4 segments
         └─▶ tailSegmentList (2s poll) → recorder → recordings table + RetentionManager(3d)
```

Single-stream cameras (eufy) have one input tagged both detect+record; the record path relays them via `rtsp://go2rtc:8554/<cameraName>` so the camera has only one direct consumer.

---

## 3. Capture (`internal/camera/`, ~1236 LOC — biggest package)

- **Goroutines per camera** (`worker.go`): `captureLoop` (detect ffmpeg → BGR24 frames), `motionLoop` (motion gate + preview), `recordLoop` (segment ffmpeg).
- **Detect capture** (`rtsp.go` `DetectCapture`): `ffmpeg [-hwaccel] -rtsp_transport tcp -i <detectURL> -vf scale=W:H -f rawvideo -pix_fmt bgr24 -r fps pipe:1`. Reads fixed `W*H*3` byte frames. **Drops frames if the channel is full — capture never blocks.** Exponential-backoff restart 1s→30s on error.
- **Roles**: `detectURL()` = first input with role `detect`; `recordURL()` = first with role `record` (falls back to detect). Prod: Dahua/Reolink use `subtype=1`(detect)/`subtype=0`(record); eufy use one `live0` for both.
- **Channels & buffers**: `detectCh`(fps*2) → `motionLoop` → `w.fanin`(→`m.fanin`,256) → `batchAssembler` → `BatchCh`(32) → detectionLoop; `MotionCh`(64); `RecordNotifyCh`(64→128). **Frames/batches are dropped when buffers fill** at three points (capture, 100ms fanin timeout, full BatchCh) — under load detection silently thins; no counter kept.
- **Live preview**: every 5th frame JPEG-encoded (q70) → served by `/api/cameras/{name}/latest-frame`.
- **Batch assembly** (`manager.go`): flush every **100ms** or when batch len ≥ #cameras.

---

## 4. Detection (`internal/detector/`, ~600 LOC)

- **Backends**: `onnx` (real, build-tag `cgo`), `cpu` (no-op fallback — records nothing, returns zero detections), selected by `buildDetector`. **Non-CGO builds silently downgrade `type: onnx` → CPU no-op** (recording-only, zero detections).
- **Model**: `/models/yolov8n.onnx` (input `images`, output `output0`), labels `/models/coco.txt`. COCO index order is load-bearing (person=0, car=2, cat=15, dog=16). Code/struct is *named* `parseYOLOv9` but the `[1,84,8400]` layout decodes YOLOv8n correctly.
- **GPU**: `use_gpu:true` → `AppendExecutionProviderCUDA` on device `gpu_device_id`. **CUDA execution provider only — no TensorRT code path** despite image/notes mentioning it.
- **Input/preprocess** (`preprocess.go`): 640×640, BGR→RGB, **plain bilinear stretch resize (NOT letterbox** — `letterboxImage` exists but is dead code). Boxes stay correct via independent X/Y un-scale (`scaleBox`); small/thin objects lose accuracy from the squish.
- **Detect** (`detector.go`): `Detect(ctx, []Frame) [][]Detection`. **Processes frames sequentially under a mutex** (ORT session not concurrency-safe) — the "batch" is logical, not a real GPU batch (input tensor is `[1,3,640,640]`).
- **Thresholds (layered):** detector conf `threshold: 0.45` (config; drops raw anchors) → NMS IoU **0.45 hardcoded (per-class greedy)** → pipeline per-label `min_score` (person 0.55, car 0.50) + `min_width/min_height` → tracker match IoU **0.3 hardcoded**.
- Detector returns all 80 classes above threshold; **the whitelist is applied in the pipeline** (`objects.track` = person/car/dog/cat).

---

## 5. Tracking (`internal/pipeline/tracker.go`)

Greedy, label-gated IoU tracker (called "centroid" but matches purely on IoU).
- Per frame `Update()`: match each existing track to the highest-IoU same-label detection **above 0.3**; unmatched detections → new tracks with a **process-global atomic ID**; a track survives up to `maxDisappeared` (default 5) missed frames.
- Only matched + newly-created tracks are **emitted** each frame (an aging-but-surviving track isn't seen by the pipeline until it re-matches).
- **Gotchas**: greedy (not Hungarian) — two same-label objects can swap IDs; cross-label re-ID impossible; track IDs are global across all cameras.

---

## 6. Zones (`internal/pipeline/zone.go`)

- Defined by normalized-[0,1] polygon `coordinates` string, `objects` whitelist, `inertia` (frames inside before trigger).
- **Membership tested on the box CENTER only** (`ContainsBox` → ray-cast point-in-polygon), not edge overlap.
- `CurrentZones` = live membership; `EnteredZones` = accumulated once `zonesHit >= inertia` (monotonic, never clears). Only runs for confirmed, non-stationary events.
- **Gotchas**: per-zone `filters` config is parsed but **never applied**; **no zones defined in prod config** (subsystem dormant but functional).

---

## 7. Motion (`internal/camera/motion.go` + `internal/motion/manager.go`)

- **Algorithm**: grayscale running-average background subtraction; `score = changed_pixels / total`; motion if `score >= contour_area/100` (config `contour_area` is a **percent**, prod 2.0 → 0.02). **Lightning suppression**: if `score >= lightning_thresh/100` (prod 15 → 0.15) treat as global flash → no motion (kills WLED/light-switch false triggers). Warmup suppresses motion for the first ~fps*20 frames.
- **Motion GATES object detection**: with `motion.enabled:true`, a frame with no motion is **not forwarded to YOLO** (`worker.go`). Mistuned `contour_area`/`alpha` can silently blind detection; disabling motion sends every frame to the detector (more GPU, no missed frames). Recording is independent of motion.
- `motion.Manager` also writes standalone `label:"motion"` events and maintains motion intervals for segment annotation (`WasActiveInRange`, used by retain modes).

---

## 8. Event pipeline (`internal/pipeline/pipeline.go`, ~883 LOC) — the movement-gated lifecycle

`eventState` fields: `confirmed`, `birthCX/birthCY` (normalized center at first appearance), `initialBox`, `stationaryFrames`, `stationary`, `zonesHit`, `topScore/topBox`.

- **New track**: `holdUntilMoves(label) = label != "person"`.
  - **person** → confirmed immediately: InsertEvent + saveSnapshot + publish ("event started").
  - **car/dog/cat** → **held**: no DB row, no snapshot, no publish (just a debug log).
- **Movement confirmation** (the parked-car fix): a held object is confirmed only when its center **translates ≥ `movementThreshold = 0.08`** (fraction of frame) from birth — measured by displacement, NOT frame-to-frame IoU (box jitter wobbles in place but doesn't translate the center). On confirm: InsertEvent + saveSnapshot + publish. A never-moving object (parked car) is **discarded on end** (no DB row ever).
- **Stationary suppression**: if `IoU(box, initialBox) > 0.85` for `stationary.threshold` frames (default 50 ≈ 10s@5fps), mark stationary → updates suppressed until it moves again.
- **Updates** (confirmed + non-stationary): zone inertia, score/box/area, UpdateEvent + publish.
- **End**: a track absent from this frame ends after `cooldown = 5s` (hardcoded, separate from tracker `maxDisappeared`). If unconfirmed → discard; else set EndTime, publish, and spawn `saveClip` in a goroutine.
- **Snapshots** (`internal/snapshot/`): `saveSnapshot` writes `/snapshots/<id>.jpg` (quality 75, optional bboxes) and sets `has_snapshot`. Runs inline (fast; needs the live frame). *(Fixed 2026-07-26 — the Saver existed but was never called, so 0 snapshots ever.)*
- **Clips** (`saveClip`, off-lock goroutine): queries recording segments overlapping `[start-pre, end+post]` (pre/post 5s from `record.events`), ffmpeg `-f concat -c copy -movflags +faststart` into `/clips/<id>.mp4`, then `SetHasClip`. *(Implemented 2026-07-26 — clips never existed before; 0 clips.)*

---

## 9. Recording & retention (`internal/recorder/`, `internal/storage/`)

- **Continuous segments**: `recordLoop` runs `ffmpeg -c copy -f segment -segment_time 10 -strftime 1 <recordingsDir>/<camera>/%Y-%m-%d/%H/%M%S.mp4` (stream copy, no re-encode). Directory = **local time**; filename = minute+second.
- **Segment → DB**: ffmpeg appends each finished segment to a `/tmp` list file; `tailSegmentList` polls it **every 2s**, resolves the absolute path, sends it up to `recorder.Dispatch` → `InsertRecording` (`ON CONFLICT (path) DO NOTHING`). `start_time`/`duration` are **hardcoded to end_time−10s / 10.0** (from file mtime), not ffprobed — correct only while `segment_duration=10`.
- **Retention** (`RetentionManager`, hourly): deletes recording **files + rows** older than `record.retain.days` (prod **3 days**), filtering `end_time <= cutoff`.
- **⚠ Retention only touches `recordings`.** There is **no scheduled pruning of `events`, `snapshots`, or `clips`** anywhere. `record.events.retain.days` (14) and `snapshots.retain_days` (14) are **parsed but dead**; `retain.mode` (`all`/`active_objects`) is also unused. This is why month-old events accumulate and the DB bloated to 233k car events (cleaned manually 2026-07-26). `retain_indefinitely`/`false_positive` columns exist but no pruner consults them. Only manual `DELETE /api/events/{id}` removes events (and its media).
- **Storage layout**: `SnapshotPath`=`/snapshots/<id>.jpg`, `ClipPath`=`/clips/<id>.mp4` (both flat). `RecordingDir(camera)`=`<rec>/<camera>`. **`RecordingPath()` is dead code and describes the WRONG (`YYYY/MM/DD`) layout** — the real tree is `<rec>/<camera>/<YYYY-MM-DD>/<HH>/<MMSS>.mp4` from the worker.

---

## 10. Database schema (`internal/db/migrations/`, pgx + golang-migrate, auto-run on startup)

- **events** (PK `id` = `"unixtime.shortuuid"`): camera, label, sub_label(TEXT, comma-joined []string — a comma in a name breaks round-trip), score, top_score, false_positive, start_time, end_time(nullable), has_clip, has_snapshot, retain_indefinitely, box/region/data (JSONB), area, entered_zones/current_zones (TEXT[]), thumbnail(BYTEA, never written), model_hash/detector_type/model_type. Indexes on camera, label, start_time DESC, end_time DESC, (camera,label), false_positive, has_clip, has_snapshot.
- **recordings** (PK id, `path` UNIQUE): camera, start/end_time, duration, motion, objects(TEXT[]), segment_size. Indexed by camera/time/motion.
- **snapshots** (PK id=event_id): camera, path, score, label, box. *(Table exists but is empty in prod — the snapshot Saver writes files + sets `events.has_snapshot`, it does not populate this table.)*
- **previews** (PK id, path UNIQUE): hourly timelapse — **no code reads/writes it** (unimplemented).
- **cameras**, **zones**: schema present, not read by the Store at runtime (config-driven).
- **face_identities** (migration 002): `id, name, embedding vector(512), source_event_id → events(id) ON DELETE SET NULL`. IVFFlat cosine index (`lists=100`). **The only FK in the schema.** pgvector's sole purpose. *(See §14 — unused.)*
- **Down-migration gotchas**: `001…down.sql` drops a nonexistent `detections` table and leaves `previews`/`zones` behind.

### Store API (`internal/events/store.go`)
`InsertEvent`, `UpdateEvent` (mutable fields only — not sub_label/camera/start_time), `GetEvent`, `ListEvents` (dynamic WHERE incl. `entered_zones && $zones` overlap; ORDER BY start_time DESC; default LIMIT 100), `InsertRecording`, `ListRecordings` (**`end_time <= Before`**; default LIMIT 1000; ORDER start_time ASC), `SetHasClip`, `DeleteRecordingsBefore`, `GetRecordingsSummary` (per-day, `objects` hardcoded 0), `RecentStationaryBox` (legacy — no longer called by the pipeline; uses a fragile string compare for no-rows). **No `DeleteEvent` in the Store** — event deletion lives in `internal/api/events.go`.
- **EventBus** (`bus.go`): in-process pub/sub, **non-blocking Publish drops events for any subscriber whose 256-buffer is full** → slow WS/MQTT consumers silently lose events.

---

## 11. REST API (`internal/api/`, chi v5, `:5000`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | `{"status":"ok"}` (Docker healthcheck) |
| GET | `/ws` | WebSocket event stream (see §12) |
| GET | `/api/stats` | the reference implementation-compat stats (per-camera FPS all **zero**; `detection_enabled` hardcoded true) |
| GET | `/api/version` | `{"version"}` (always `dev`) |
| GET | `/api/config` | **STUB** — `{"note":"…not yet implemented"}` |
| GET | `/api/events` | list (params: camera,label,sub_label,after,before,has_clip,has_snapshot,false_positive,zone,limit,skip) |
| GET | `/api/events/summary` | counts by camera/label (O(cameras×events) in-memory) |
| GET/DELETE | `/api/events/{id}` | get / delete (delete also removes snapshot+clip files) |
| POST | `/api/events/{id}/false_positive` · `/retain` | flags |
| GET | `/api/events/{id}/snapshot.jpg` · `/clip.mp4` | media (plain `io.Copy` — **no range/seek**) |
| GET | `/api/recordings` · `/recordings/summary` · `/cameras/{name}/recordings` | continuous recordings |
| GET | `/api/cameras` · `/cameras/{name}` · `/cameras/{name}/latest-frame` | camera info + live preview JPEG (`online` hardcoded true) |
| POST | `/api/notifications/proxy` | server-side proxy to HA mobile webhook (avoids browser CORS) |
| GET | `/vod/{date}/{hour}/{camera}/index.m3u8` | HLS VOD playlist — **built from actual on-disk files** (`os.ReadDir`), not a DB query (avoids the UTC-vs-local-dir skew bug fixed 2026-07-26) |
| GET | `/vod/{date}/{hour}/{camera}/{segment}` · `/clips/{file}` | segment/clip MP4 via `http.ServeContent` (**range/seek OK**) |
| GET | `/*` | embedded SPA (`//go:embed webdist`) |
- **No face routes.** **Auth disabled by default** (`auth_enabled:false`); when on, Bearer/`?api_key=`, `/ws` exempt. **CORS default `["*"]`** (reflects Origin). Middleware: requestID → logging → CORS → Recoverer → (optional auth).
- **Range inconsistency**: VOD/clip-file/latest-frame use `http.ServeContent` (seek OK); `/api/events/{id}/clip.mp4` + `/snapshot.jpg` use plain `io.Copy` (no seek).

---

## 12. WebSocket (`/ws`, `internal/api/websocket.go`)

- Server subscribes the EventBus and pushes each event as **flat `events.Event` JSON** (`type` = new/update/end). Per-client `{"subscribe":{"cameras":[],"labels":[]}}` filters. Slow clients dropped (256 buffer). Server PING every 30s.
- **⚠ Critical UI contract bug**: the UI (`ws.ts` + pages) expects the event nested under `msg.payload` and early-returns `if (!msg.payload)`. The server sends it **flat (no `payload` wrapper)**. Result: the Nav "new events" badge works (reads top-level `msg.type`), but **live real-time updates in the Events and Live pages are dead** — they only populate via initial/paginated loads. Fix = wrap server output as `{type, payload}` or change the UI handlers to read top-level fields.

---

## 13. MQTT (`internal/mqtt/`, integration-friendly)

- Broker `mosquitto:1883`, `topic_prefix: sentinel`, `client_id: sentinel-nvr`, QoS 1, CleanSession false, LWT.
- **Published**: `<prefix>/available` (retained online/offline via LWT + OnConnect) and `<prefix>/events` (every event). Payload `{type, before, after}` where `after` is a the published `EventData` (`before`=null for new; end_time nulled for update/end).
- **Defined but NEVER published**: `<prefix>/stats`, `<prefix>/<cam>/motion`, `<prefix>/<cam>/<label>` (object counts), `/state`, `/snapshot`. So HA integrations relying on the reference implementation per-camera object-count or stats topics get nothing — only the event stream + availability.
- Same EventBus feeds both MQTT and WS (same event stream).

---

## 14. Web UI (`web/src/`, Svelte 5 + Tailwind SPA)

- **Hash routing**, 4 tabs via `Nav.svelte`: **Live**, **Events**, **Recordings**, **Settings**. `StatusDot` = WS state. All API calls relative (same origin).
- **Live**: camera grid (`CameraCard` thumbnails = `latest-frame` refreshed every 5s); fullscreen → `VideoPlayer` (live).
- **Events**: filterable/paginated feed (`EventCard`); clip playback = plain `<video src=/api/events/{id}/clip.mp4>`; motion-only events fall back to playing a nearby recording segment.
- **Recordings**: camera+date → 24 hour-blocks (blue if that *day* has footage — a per-day approximation, not per-hour) → `VodPlayer`.
- **Settings**: read-only diagnostics (stats + `/api/config` stub). Cannot change anything.
- **Two players**:
  - `VideoPlayer.svelte` = **LIVE**: speaks go2rtc's MSE WebSocket (`ws://host:1984/api/ws?src=NAME`), hand-parses fMP4 boxes, feeds MediaSource. (go2rtc port 1984 hardcoded in `Live.svelte`.)
  - `VodPlayer.svelte` = **RECORDINGS** (added 2026-07-26): fetches the `.m3u8`, plays the plain 10s segment MP4s sequentially in one `<video>` with a global hour scrubber, dependency-free (no hls.js; caps consecutive segment errors at 3 so a stale-playlist tail can't cascade). Replaced the old bug where the Recordings tab wrongly fed the VOD URL into the *live* player → always "Stream unavailable" (Chrome can't play native HLS anyway).

---

## 15. Config reference (`internal/config/config.go`) — key knobs

`database.url` (required) · `mqtt.{enabled,host,port,user,password,topic_prefix,client_id}` · `detector.{type:onnx|cpu,use_gpu,gpu_device_id,num_threads,onnx.{model_path,label_path,threshold,input_width,input_height}}` · `objects.{track[],filters.<label>.{min_score,min_width,min_height}}` (note `min_area/max_area/mask` parsed but **ignored**) · `motion.{enabled,threshold,alpha,contour_area(%),lightning_thresh(%)}` (`frame_alpha` ignored) · `record.{enabled,segment_duration,retain.days,events.{pre_capture,post_capture,retain.days(DEAD)}}` · `snapshots.{enabled,bboxes,quality,retain_days(DEAD)}` · `storage.{recordings,snapshots,clips,exports,tmp}_dir` · `api.{listen,auth_enabled,api_key,cors_origins}` · `go2rtc.url` · `cameras.<name>.{ffmpeg.inputs[].{path,roles[]},detect.{width,height,fps,max_disappeared,stationary.threshold},motion,record,snapshots,objects,zones}` · `face_recognition.{enabled,model_path,threshold,min_face_size}` (**unused**).
Cameras inherit global `motion/record/snapshots/objects` when omitted. `Validate` requires: db url, ≥1 camera, each camera ≥1 input with a `detect` role, detect W/H/fps > 0, api_key if auth_enabled.

**Prod values**: detector onnx/GPU, threshold 0.45, 640×640; person min_score 0.55 / car 0.50; motion enabled (contour 2.0, lightning 15); record 10s segments, retain 3 days; snapshots q75; storage `/recordings` etc.; api `0.0.0.0:5000` auth off cors `*`.

---

## 16. Known issues / gotchas / dead code (prioritized)

**Real functional gaps:**
1. **Events / snapshots / clips never auto-expire** — only continuous recordings are pruned (3 days). `record.events.retain.days` + `snapshots.retain_days` are dead. → DB + snapshot/clip dirs grow forever. Needs an event-retention pass (delete old events + their media, honoring `retain_indefinitely`/`false_positive`). This is the root cause of the 233k-car-event bloat.
2. **Face recognition is scaffold-only** — README claims "Built-in (ArcFace + pgvector)" but there is **no runtime code** (grep of `internal/**.go` finds only the config struct, the migration, and a comment). The table/index/config/`sub_label` slot exist; setting `enabled:true` does nothing.
3. **WebSocket payload shape mismatch** — server sends flat Event, UI reads `msg.payload` → live updates in Events/Live pages are dead (only the badge works).
4. **Motion gates detection** — mistuned motion can silently blind YOLO.
5. **`deepstream` detector type** is configured/composed (gpu override + a deepstream service) but **not selectable in `buildDetector`** (would error "unknown detector type").

**Misleading / cosmetic:**
6. Model is **YOLOv8n**, code named "YOLOv9" (harmless — same tensor layout).
7. **No TensorRT** (CUDA EP only), despite naming.
8. Detection "batch" is sequential-under-mutex, not a real GPU batch.
9. Stretch resize (letterbox is dead code) → some accuracy loss on non-square aspect ratios.
10. `RecordingPath()` is dead code documenting the wrong disk layout.
11. Config **hot-reload is a no-op** (`UpdateConfig` TODO); build version hardcoded `dev`; `/api/config` + `/api/stats` per-camera FPS are stubs/zeros; `online` always true.
12. `deploy/docker-compose.yml` template storage mounts (`/media/...`) don't match `config.yaml`; the authoritative running compose is the NAS repo root file.
13. Ignored config fields: `objects.filters.*.{min_area,max_area,mask}`, `zones.*.filters`, `motion.frame_alpha`, `detect.stationary.{interval,max_frames}`.
14. `sub_label` comma-joined storage; `RecentStationaryBox` fragile no-rows string compare; `EventBus`/WS/MQTT silently drop on full buffers.

**Secrets in-repo** (config.yaml, deploy/*, go2rtc.yaml): MQTT `<mqtt-user>` pw, Postgres pw, RTSP/eufy camera creds — do not commit to any public repo.

---

## 17. Roadmap hooks (home automation integration)

Point Sentinel's MQTT client at your home automation platform's broker and subscribe a `<prefix>/#` handler there to turn events into per-camera entities and automations; the `:5000` REST API plus live and VOD endpoints can be proxied or embedded in that platform's UI. Model the handler on the MQTT schema, but note that only `<prefix>/events` and `<prefix>/available` are actually published today — per-camera object-count topics would need to be added to Sentinel first.
