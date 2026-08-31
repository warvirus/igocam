# syntax=docker/dockerfile:1

# ── 1단계: 빌드 스테이지 (OpenCV + Go로 igocam 빌드) ──
FROM golang:1.27-bookworm AS builder

# OpenCV 4.x 개발 라이브러리 (gocv CGO 빌드에 필요)
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopencv-dev \
    ffmpeg \
    wget \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /build

# go.mod/go.sum 복사 → 의존성 캐시 활용
COPY go.mod go.sum ./
RUN go mod download

# 소스 복사 후 빌드 (static 웹 UI는 internal/httpserver에 go:embed 포함)
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY examples/ ./examples/

RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o /build/igocam ./cmd/igocam/

# ── 2단계: 실행 스테이지 (가벼운 런타임) ──
FROM debian:bookworm-slim

# OpenCV 런타임 라이브러리 + ffmpeg + go2rtc
RUN apt-get update && apt-get install -y --no-install-recommends \
    libopencv-dev \
    ffmpeg \
    ca-certificates \
    wget \
    && rm -rf /var/lib/apt/lists/*

# go2rtc 바이너리 다운로드 (linux amd64)
RUN wget -q https://github.com/AlexxIT/go2rtc/releases/download/v1.9.14/go2rtc_linux_amd64 \
    -O /usr/local/bin/go2rtc \
    && chmod +x /usr/local/bin/go2rtc

WORKDIR /app

# 빌드된 igocam + 설정 파일 복사
COPY --from=builder /build/igocam /usr/local/bin/igocam
COPY bin/camera_config.json ./camera_config.json
COPY bin/ptz_presets.json ./ptz_presets.json 2>/dev/null || true

# 런타임 디렉터리 생성
RUN mkdir -p /app/data /app/recordings /app/videos

# 포트 노출
# 8090~8120 - ONVIF/Web UI, 8554~8584 - RTSP, 1935~1965 - RTMP
# 1984~2014 - go2rtc API, 8555~8558 - WebRTC
EXPOSE 8090 8100 8110 8120
EXPOSE 8554 8564 8574 8584
EXPOSE 1935 1945 1955 1965
EXPOSE 1984 1994 2004 2014
EXPOSE 8555/udp 8556/udp 8557/udp 8558/udp
EXPOSE 3702/udp

# 기본 실행 명령
CMD ["igocam", "--config", "/app/camera_config.json"]
