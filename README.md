# igocam

**i**nstant **Go** **Cam** — 가상 IP 카메라 서버.

ONVIF / RTSP / RTMP / WebRTC / Web UI / MJPEG / PTZ / 녹화를 지원하는 다중 카메라 스트리밍 서버입니다.
동영상 파일 / 웹캠 / RTSP 소스를 실제 IP 카메라처럼 서비스합니다.
Python 프로젝트 [IPyCam](https://github.com/)을 Go로 포팅했습니다.

> ⚠️ 이 프로젝트는 **Go 언어를 몰라도 사용할 수 있습니다.** 아래 설명을 순서대로 따라하세요.

---

## 📖 프로젝트 설명서 (아키텍처)

시스템 구조, 데이터 파이프라인, 포트 구성, 기능 상세는 설명서를 참고하세요.

| 문서 | 설명 |
|------|------|
| [doc/architecture.html](doc/architecture.html) | 아키텍처 다이어그램 (그림 한 장으로 보는 전체 구조) |
| [doc/architecture.md](doc/architecture.md) | 아키텍처 상세 문서 (패키지별 구성, 설정 필드, 운영 제약) |

---

## 📦 다운로드

```bash
git clone https://github.com/사용자명/igocam.git
cd igocam
```

---

## ✅ 필요 준비물

### 1. Go 설치 (한 번만)

| OS | 설치 방법 |
|----|----------|
| **macOS** | `brew install go` |
| **Windows** | https://go.dev/dl/ 에서 설치 프로그램 다운로드 |
| **Linux** | `sudo apt install golang` 또는 공식 사이트 |

확인: `go version` → `go1.27.0` 이상 출력되어야 함

### 2. OpenCV 설치 (한 번만)

**macOS:**
```bash
brew install opencv@4
```

**Linux (Ubuntu/Debian):**
```bash
sudo apt install libopencv-dev
```

**Windows:**
https://opencv.org/releases/ 에서 OpenCV 4.x 다운로드 후 설치

---

## 🔧 빌드하기

터미널에서 프로젝트 폴더로 이동한 후:

```bash
go build -o bin/igocam ./cmd/igocam/
```

빌드가 끝나면 `bin/igocam` 실행 파일이 생성됩니다.

> 참고: 첫 빌드는 OpenCV 라이브러리를 다운로드하므로 시간이 조금 걸릴 수 있습니다.

---

## 🚀 실행하기

### 1. go2rtc 설치

igocam은 RTSP/WebRTC 스트리밍을 위해 [go2rtc](https://github.com/AlexxIT/go2rtc)가 필요합니다.

**자동 다운로드 (권장):**
```bash
go install github.com/AlexxIT/go2rtc@v1.9.14
```

또는 [릴리스 페이지](https://github.com/AlexxIT/go2rtc/releases)에서 `go2rtc_*` 바이너리를 받아 `bin/` 폴더에 넣으세요.

> Docker로 실행하면 go2rtc/ffmpeg가 이미지에 포함되어 별도 설치가 필요 없습니다.

### 2. 실행

```bash
./bin/igocam --config bin/camera_config.json
```

**옵션:**

| 플래그 | 기본값 | 설명 |
|--------|--------|------|
| `--config` | `bin/camera_config.json` | 카메라 설정 JSON 경로 |
| `--port` | `8080` | 관리자 페이지 포트 |
| `--admin-user` / `--admin-pass` | 없음 | 관리자 페이지 로그인 (미지정 시 인증 비활성) |
| `--log-level` | `INFO` | DEBUG/INFO/WARNING/ERROR |

`.env` 파일로도 설정 가능합니다 (`IGOCAM_CONFIG`, `IGOCAM_PORT`, `IGOCAM_ADMIN_USER`, `IGOCAM_ADMIN_PASS`, `IGOCAM_LOG_LEVEL`). 환경변수가 CLI 플래그보다 우선합니다.

### 3. 접속하기

실행 후 터미널에 각 카메라의 접속 주소가 출력됩니다. 예시:

```
Camera: Virtual Camera 1
  Web UI:    http://192.168.1.10:8090/
  ONVIF:     http://192.168.1.10:8090/onvif/device_service
  RTSP:      rtsp://192.168.1.10:8553/video_main
  MJPEG:     http://192.168.1.10:8090/stream.mjpeg
```

**관리자 페이지:** `http://<서버IP>:8080/` — 카메라 추가/수정/삭제, 전체 시작/정지, 일시정지, 스냅샷 썸네일, 상태 확인

---

## 📋 설정 파일

`bin/camera_config.json`에서 카메라를 설정합니다.

**기본 구조:**

```json
[
  {
    "name": "Virtual Camera 1",
    "source": "/path/to/sample.mp4",
    "onvif_port": 8090,
    "rtsp_port": 8553,
    "rtmp_port": 1934,
    "web_port": 8081,
    "go2rtc_api_port": 1984,
    "webrtc_port": 8555,
    "main_width": 640,
    "main_height": 360,
    "main_fps": 30,
    "main_bitrate": "1M",
    "hw_accel": "videotoolbox",
    "bypass": false
  }
]
```

**주요 필드 설명:**

| 필드 | 설명 |
|------|------|
| `name` | 카메라 이름 (Web UI/관리자 페이지에 표시) |
| `source` | 비디오 파일 경로, RTSP 주소, 또는 카메라 번호("0") |
| `onvif_port` | ONVIF / 카메라 Web UI / MJPEG / 스냅샷 접속 포트 |
| `rtsp_port` | RTSP 스트리밍 포트 (main: `/video_main`, sub: `/video_sub`) |
| `main_width` / `main_height` | 출력 영상 해상도 |
| `main_fps` | 초당 프레임 수 (30fps 권장, 소스 FPS로 자동 보정됨) |
| `hw_accel` | 인코딩 방식: `cpu`(소프트웨어), `nvenc`(NVIDIA), `qsv`(Intel), `videotoolbox`(macOS) |
| `bypass` | `true`면 H264 소스를 트랜스코딩 없이 go2rtc로 직통 전송 (CPU 절감) |

**카메라 여러 대 추가:** 배열 안에 객체를 추가하면 됩니다. 포트가 겹치지 않게 주의하세요.
관리자 페이지에서 추가하면 포트를 자동 제안합니다.

전체 필드 목록은 [doc/architecture.md](doc/architecture.md#6-설정-cameraconfig)를 참고하세요.

---

## 🎥 사용 예시

### 비디오 파일 재생
`source`에 동영상 파일 경로를 입력하면 자동 반복 재생됩니다.

### 웹캠 사용
`source`에 `"0"`을 입력하면 첫 번째 웹캠을 사용합니다.

### RTSP 카메라
`source`에 `"rtsp://카메라주소:554/stream"` 을 입력합니다.

### 파일 업로드
카메라 Web UI에서 비디오 파일을 업로드하면 즉시 재생이 전환됩니다.

> H264 소스를 권장합니다. VP9/AV1 4K는 OpenCV 소프트웨어 디코딩 한계로 실시간 재생이 어렵습니다. (bypass 모드에서는 AV1이 자동 재인코딩됩니다)

---

## 🐳 Docker로 실행하기 (선택)

Docker가 설치되어 있다면 빌드 없이 바로 실행할 수 있습니다.

```bash
# 1. 카메라 설정의 source 경로를 실제 동영상 경로로 수정
#    (camera_config.json의 "source" 필드)

# 2. docker-compose.yml에서 비디오 경로 변경
#    /path/to/your/videos 를 실제 동영상이 있는 폴더로 수정

# 3. 빌드 & 실행
docker compose up -d

# 중지
docker compose down
```

Docker 이미지에는 go2rtc와 ffmpeg가 포함되어 있어 별도 설치가 필요 없습니다.

---

## 📁 프로젝트 구조

```
igocam/
├── bin/                  # 실행 파일 (빌드 결과)
│   ├── igocam            # 메인 서버
│   ├── go2rtc            # RTSP/WebRTC 스트리밍 (별도 설치)
│   └── camera_config.json # 카메라 설정
├── cmd/igocam/main.go    # 프로그램 시작점
├── internal/             # 내부 패키지
│   ├── camera/           # 카메라 파이프라인
│   ├── onvif/            # ONVIF SOAP 서비스
│   ├── ptz/              # PTZ 컨트롤러
│   ├── discovery/        # WS-Discovery
│   ├── httpserver/       # 카메라 Web UI + REST API
│   ├── admin/            # 관리자 서버 (8080)
│   ├── manager/          # 다중 카메라 오케스트레이터
│   └── streamer/         # FFmpeg 인코딩
├── doc/                  # 문서
│   ├── architecture.html # 아키텍처 다이어그램
│   └── architecture.md   # 아키텍처 상세
├── examples/             # 사용 예제
│   ├── basic/            # 웹캠 기본 사용
│   ├── video_file/       # 비디오 파일 재생
│   ├── generated_content/# 프로그램 생성 영상
│   ├── ptz_demo/         # PTZ 자동 데모
│   ├── custom_config/    # 설정 커스터마이징
│   └── hardware_ptz/     # 하드웨어 PTZ 컨트롤러 연결
└── go.mod                # Go 모듈 설정
```

**예제 실행 방법:**
```bash
# 웹캠 사용
go run ./examples/basic/

# 비디오 파일 재생 (파일 경로를 인자로)
go run ./examples/video_file/ your_video.mp4

# PTZ 자동 데모
go run ./examples/ptz_demo/
```

---

## 🔧 문제 해결

**"go2rtc: executable file not found"**
→ go2rtc 바이너리를 `bin/` 폴더에 넣거나, `PATH`에 등록하세요.

**"OpenCV not found"**
→ OpenCV 설치를 확인하세요. (macOS: `brew install opencv@4`, Ubuntu: `sudo apt install libopencv-dev`)

**화면이 안 나와요**
→ `bin/camera_config.json`의 `source` 경로에 실제 동영상 파일이 있는지 확인하세요.

**"bind: address already in use"**
→ 해당 포트가 이미 사용 중입니다. `camera_config.json`에서 포트 번호를 변경하세요.

**관리자 페이지 썸네일이 안 나와요**
→ 최신 빌드인지 확인하세요. 실행 중인 프로세스가 구버전 바이너리일 수 있습니다. (`ps aux | grep igocam` 시작 시각 vs `bin/igocam` 빌드 시각 비교)

**igocam을 죽였는데 포트가 여전히 사용 중이에요**
→ go2rtc가 고아 프로세스로 남은 것입니다. `ps aux | grep go2rtc`로 확인 후 종료하세요.

---

## 📄 라이선스

MIT License
