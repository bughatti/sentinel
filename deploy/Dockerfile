FROM golang:1.23-bookworm AS builder

WORKDIR /build

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates git curl gcc \
    && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /opt/ort && \
    curl -fsSL https://github.com/microsoft/onnxruntime/releases/download/v1.20.1/onnxruntime-linux-x64-gpu-1.20.1.tgz \
    | tar -xz --strip-components=1 -C /opt/ort

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN mkdir -p internal/api/webdist

# Build version — override with: docker compose build --build-arg VERSION=1.2.3
ARG VERSION=1.0.0

ENV CGO_ENABLED=1
ENV CGO_CFLAGS="-I/opt/ort/include"
ENV CGO_LDFLAGS="-L/opt/ort/lib -lonnxruntime"

RUN go build \
    -ldflags="-s -w -X github.com/bughatti/sentinel/internal/app.Version=${VERSION}" \
    -o /build/sentinel \
    ./cmd/sentinel

FROM nvidia/cuda:12.6.3-cudnn-runtime-ubuntu24.04

# ffmpeg/curl/tz + TensorRT 10 runtime (libnvinfer.so.10 etc.) for the ONNX
# Runtime TensorRT execution provider. TensorRT packages come from the CUDA
# apt repo already configured in this base image.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg ca-certificates curl tzdata \
    libnvinfer10 libnvinfer-plugin10 libnvonnxparsers10 \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /opt/ort/lib/libonnxruntime*.so* /usr/local/lib/
RUN ldconfig

COPY --from=builder /build/sentinel /usr/local/bin/sentinel

RUN mkdir -p /recordings /snapshots /clips /exports /tmp/sentinel /etc/sentinel /models

ENV SENTINEL_CONFIG=/etc/sentinel/config.yaml

EXPOSE 5000

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -f http://localhost:5000/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/sentinel"]
CMD ["--config", "/etc/sentinel/config.yaml"]
