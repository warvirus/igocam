# igocam 아키텍처

`igocam` (**i**nstant **Go** **Cam**) — 가상 IP 카메라 서버.
Python 프로젝트 IPyCam을 Go로 포팅한 것으로, 동영상 파일 / 웹캠 / RTSP 소스를
ONVIF / RTSP / RTMP / WebRTC / Web UI / MJPEG / PTZ 카메라처럼 서비스한다.

- 최종 갱신: 2026-09-03
- Go 1.27 + OpenCV(gocv) + ffmpeg + [go2rtc](https://github.com/AlexxIT/go2rtc) 1.9.14 (외부 바이너리)

---

## 1. 시스템 개요

```
                    ┌─────────────────────────────────────────────────┐
                    │                  igocam 프로세스                  │
                    │                                                 │
  config JSON ────► │  ┌──────────┐   ┌───────────┐   ┌─────────────┐ │      ┌──────────────┐
                    │  │ Manager   │──►│ Camera ×N │──►│ HTTPServer  │ │◄────►│ NVR / 브라우저 │
  CLI (.env) ─────► │  │ (오케스트 │   │ (파이프라 │   │ ONVIF+WebUI │ │      └──────────────┘
                    │  │  레이터)  │   │  인)      │   └─────────────┘ │
                    │  └──────────┘   └─────┬─────┘   ┌─────────────┐ │      ┌──────────────┐
                    │  ┌──────────┐         │         │ AdminServer │ │◄────►│ 관리 브라우저  │
                    │  │ Discovery│         ▼         │  (8080)     │ │      │  (8080)      │
                    │  │ WS-Disc. │   ┌───────────┐   └─────────────┘ │      └──────────────┘
                    │  └──────────┘   │ go2rtc ×N │  카메라마다 1개
                    │                 │ 서브프로세 │  RTSP/RTMP/WebRTC
                    │                 └─────┬─────┘
                    └───────────────────────┼─────────────────────────
                                            ▼
                                   NVR / WebRTC 클라이언트
```

- **카메라 1대 = 독립된 포트 세트** (ONVIF/Web UI, RTSP, RTMP, go2rtc API, WebRTC) + 독립 go2rtc 서브프로세스.
- 관리 서버(8080) 1개가 Manager를 통해 전체 카메라를 제어한다.

### 포트 구성 (카메라별)

| 포트 | 용도 |
|---|---|
| `onvif_port` | ONVIF SOAP + 카메라 Web UI + MJPEG + 스냅샷 + 업로드 (HTTP 전부 여기 바인딩) |
| `rtsp_port` | go2rtc RTSP 서버 (`/video_main`, `/video_sub`) |
| `rtmp_port` | streamer → go2rtc RTMP push |
| `go2rtc_api_port` | go2rtc API (헬스체크/스트림 상태) |
| `webrtc_port` | go2rtc WebRTC (ICE) |
| `8080` (공유) | 관리 서버 (Admin UI + REST) |

---

## 2. 데이터 파이프라인

### 2.1 일반 모드 (bypass=false)

```
소스 (파일/웹캠/RTSP)                       카메라마다 goroutine
      │
      ▼
 captureLoop ──── 캡처/디코딩 (OpenCV) ─── 원본 FPS 반영, 파일 업로드 전환 감시
      │                                    IdleBypass 시 1fps 저주기
      ▼
 frame.Frame (참조 카운팅 래퍼)
      │
      ▼
 Camera.Stream ── PTZ 변환 → display transform(반전/회전) → 타임스탬프 오버레이
      │              → outbound 클론 → 팬아웃(큐별 drop-oldest)
      │
      ├─► Streamer (ffmpeg 자식 프로세스, HW 가속 인코딩) ── RTMP ──► go2rtc
      │     main(고해상도) + sub(저해상도) 2개 스트림
      ├─► MJPEG 서버 ── 브라우저 live 뷰 / 폴백
      └─► Recorder ── mp4 녹화 (파일 분할, pre-record 버퍼)
```

### 2.2 bypass 모드 (bypass=true, 파일/RTSP 소스 전용)

```
파일/RTSP 소스 ──► go2rtc가 직접 읽어 전송 (트랜스코딩 없음)
                    video_main: ffmpeg:/path/file.mp4#input=loop
                    video_sub:  video_main (참조 복사)

OpenCV 파이프라인은 스냅샷/MJPEG/녹화용으로만 유지
IdleBypass: 로컬 소비자(MJPEG 클라이언트·레코더)가 없으면 캡처 1fps로 저하 + 파이프라인 생략 → CPU 절감
```

- **루프**: go2rtc `ffmpeg:` 소스에 `#input=loop` 쿼리 → yaml의 `ffmpeg: loop: "-stream_loop -1 -re -i {input}"` 템플릿으로 무한 반복. (go2rtc는 파일 끝에서 producer를 재시작하지 않으므로 루프가 필수)
- **코덱**: `ProbeCodec`으로 확인 후 H264가 아니면(AV1 등) `#video=h264`를 추가해 go2rtc가 libx264로 재인코딩 (RTSP는 AV1 패킷화 불가).
- **제약**: 카메라 디바이스/업로드 모드는 bypass 불가 → 자동 일반 인코딩 전환 + 경고.

### 2.3 프레임 참조 카운팅 (internal/frame)

IPyCam의 "outbound immutability contract"를 Go에서 재현:

- `Frame` = `gocv.Mat` + `refs atomic.Int64`
- 생산자: `NewFrame(mat)` (refs=1) → 큐 enqueue 전 `Retain()` → enqueue 직후 `Release()`
- 소비자: 처리 후 `Release()` — 마지막 Release가 Mat을 Close
- 큐는 drop-oldest 정책. 닫힌 큐에 Put되면 `OnDrop` 콜백으로 Mat 누수 방지

이 규칙 덕분에 비동기 워커(streamer/mjpeg/recorder) 간 use-after-free 없이 하나의 Mat을 공유한다.

---

## 3. 패키지 구성 (internal/)

| 패키지 | 역할 | 핵심 타입/함수 |
|---|---|---|
| `config` | 카메라 설정 로드/검증/원자적 저장, ID 생성 | `CameraConfig`, `LoadAll/SaveAll`, `GenerateID` |
| `capture` | 프레임 소스 추상화 (웹캠/파일/RTSP), FPS·코덱 프로브 | `FrameSource` 인터페이스, `ProbeFPS`, `ProbeCodec`, `LoopSource` |
| `frame` | 참조 카운팅 프레임 + drop-oldest 큐 + transform | `Frame`, `Queue`, `transform.go` |
| `camera` | 단일 카메라 파이프라인 (fanout, 페이싱, 스냅샷) | `Camera`, `Stream()`, `IdleBypass()` |
| `streamer` | ffmpeg 자식 프로세스로 H264 인코딩 → RTMP push | `Streamer`, `HWAccel` (nvenc/qsv/videotoolbox/cpu), main/sub 2개 출력 |
| `gostream` | go2rtc 서브프로세스 수명주기 + yaml 생성 | `Manager`, `WriteGo2rtcConfig` |
| `mjpeg` | MJPEG 스트리밍 서버 (multipart) | `Server`, 클라이언트 관리 |
| `recorder` | mp4 녹화 워커 (파일 분할, pre-record) | `Recorder`, `Submit` |
| `ptz` | PTZ 상태 머신 (Continuous/Absolute/Relative/프리셋) + 하드웨어 핸들러 | `Controller`, `HardwareHandler` |
| `onvif` | ONVIF Profile S SOAP (Device/Media/PTZ), WS-Security | `Server`, `HandleAction` |
| `discovery` | WS-Discovery UDP 멀티캐스트 (ProbeMatch/Hello/Bye) | `Server` |
| `httpserver` | 카메라별 Web UI + REST + MJPEG/스냅샷 + ONVIF 라우팅 | `Server` (onvif_port에 바인딩) |
| `admin` | 전체 관리 서버: 카메라 CRUD/제어 + HUD 테마 UI | `Server` (8080), `html.go` |
| `manager` | 다중 카메라 오케스트레이터 + 캡처 스레드 | `Manager`, `Start/StartAll/ReloadFromConfig` |
| `http`, `logging` | (빈 디렉터리, 예약됨) | — |

---

## 4. 기능별 상세

### 4.1 소스 관리 (capture)

- 소스 타입: `SourceCamera`(디바이스 인덱스) / `SourceVideoFile` / `SourceRTSP` / `SourceCustom`(업로드 대기)
- `InferSourceType`으로 문자열 자동 판별 ("0" → 웹캠, URL → RTSP, 파일 존재 → 파일)
- `ProbeFPS`: 원본 FPS 조회 → `main_fps`에 반영 (59.94→60 반올림, 재생 속도 보정)
- `ProbeCodec`: FourCC 코덱 조회 (bypass에서 AV1 감지용)
- `LoopSource`: 파일 끝 도달 시 처음부터 재시작 (일반 모드 루프)
- VP9/AV1 4K는 OpenCV 소프트웨어 디코딩으로 실시간 불가 → H264 소스 권장

### 4.2 스트리밍 (streamer + gostream)

**일반 모드 인코딩 (streamer)**
- rawvideo(BGR)를 stdin으로 받는 ffmpeg 파이프, main/sub 동시 인코딩
- HW 가속: `HWAuto` 사다리 (NVENC → QSV → VideoToolbox(macOS) → CPU), 시작 전 인코더 가용성 검사
- 스트림별 옵션: `main/sub_keyframe_interval`(-g), `main/sub_options`, `-maxrate/-bufsize`
- writeLoop: Mat 공유 race 방지를 위해 `Clone()` 후 write, 연결 끊김 시 자동 재접속
- `Pause()/Resume()`: 전송만 중단 (ffmpeg/캡처 유지)

**go2rtc 관리 (gostream)**
- 카메라마다 독립 go2rtc 서브프로세스 (`data/go2rtc_{name}.yaml`)
- `WriteGo2rtcConfig`: 스트림 정의 + api/rtsp/rtmp/webrtc 포트. candidates는 미지정 → 로컬 IP 자동 감지 (Docker `network_mode: host`와 호환)
- Start 후 API 헬스체크로 준비 대기 (5s deadline)

### 4.3 ONVIF / WS-Discovery (onvif / discovery)

- `http://ip:onvif_port/onvif/device_service` 등 SOAP 액션:
  Device(GetDeviceInformation, GetCapabilities, GetServices, GetSystemDateAndTime, GetUsers…),
  Media(GetProfiles, GetStreamUri, GetSnapshotUri, GetVideoEncoderConfiguration…),
  PTZ(ContinuousMove, AbsoluteMove, RelativeMove, GotoHomePosition, Get/SetPreset…)
- WS-Security PasswordDigest 인증 (설정 시), 401은 SOAP 1.2 규격 `s:Sender` fault
- XML 주입 방지 이스케이프 (preset token/name, camera_name 등)
- WS-Discovery: UDP 239.255.255.250:3702 멀티캐스트, Probe → ProbeMatch, Hello/Bye announce
- `GetStreamUri`는 go2rtc RTSP 주소(`rtsp://ip:rtsp_port/video_main`) 반환

### 4.4 Web UI (httpserver)

onvif_port에 바인딩 (web_port 미사용 — IPyCam 호환 설계):

| 엔드포인트 | 설명 |
|---|---|
| `/` | Web UI (HUD 테마, 미리보기는 go2rtc stream.html iframe + MJPEG 폴백) |
| `/api/config` GET/PUT | 카메라 설정 조회/변경 (검증 후 즉시 반영) |
| `/api/stats` | FPS/비트레이트/연결 수 실시간 |
| `/api/ptz` | PTZ 제어 REST |
| `/api/recording/*` | 녹화 시작/정지/상태 |
| `/api/restart` | 스트림 재시작 |
| `/api/video/upload` | 비디오 파일 업로드 (multipart 필드명 `file`, 업로드 즉시 재생 전환) |
| `/snapshot.jpg` / `/stream.mjpeg` | 스냅샷 / MJPEG live |
| `/onvif/*` | SOAP 라우팅 |

- http.Server: `ReadHeaderTimeout 10s`, `IdleTimeout 60s` (좀비 keep-alive 방지; MJPEG 보호를 위해 WriteTimeout 미설정)

### 4.5 관리 서버 (admin, 8080)

| 엔드포인트 | 설명 |
|---|---|
| `POST /api/auth/login·logout` | 세션 쿠키 로그인 (HUD 테마 로그인 화면) |
| `GET/POST /api/cameras` | 카메라 목록 / 추가 (포트 자동 제안 +10, DefaultConfig 기반) |
| `GET/PUT/DELETE /api/cameras/{id}` | 조회 / 수정 / 삭제 (hot CRUD — 실행 중에도 반영) |
| `GET /api/cameras/{id}/snapshot` | 스냅샷 프록시 — **인증 예외** (`<img>` 태그는 Authorization 헤더를 실을 수 없음) |
| `POST /api/reload` | 디스크 config 재로딩 → diff 동기화 (추가/제거/재시작) |
| `POST /api/start-all·stop-all` | 전체 시작/정지 |
| `POST /api/pause-streams·resume-streams` | 전체 전송 pause/resume |
| `GET /api/status` | 전체 카메라 상태/FPS |
| `GET /api/ports/available` | 사용 가능한 포트 제안 |

- Basic Auth + 세션 쿠키 하이브리드, `/api/*`에만 적용
- Manager API: `AddCamera/RemoveCamera/UpdateCamera/RestartCamera/CameraByID/FindAvailablePorts/StartAll/StopAll/PauseStreams/ResumeStreams/ReloadFromConfig`
- ReloadFromConfig: 실행 중인 config 포인터를 in-place 갱신해 HTTPServer와 일관성 유지

### 4.6 PTZ (ptz)

- 소프트웨어 PTZ: 화면 이동 시뮬레이션 (pan/tilt/zoom 상태 유지, transform에 반영)
- `HardwareHandler` 인터페이스로 실제 서보/하드웨어 연결 가능 (`examples/hardware_ptz` 참조)
- 프리셋: `ptz_presets.json` 저장/불러오기
- ONVIF PTZ 액션과 REST `/api/ptz` 모두 지원

### 4.7 녹화 (recorder)

- 프레임 수신 대기형 워커: 녹화 시작 전에도 pre-record 버퍼 유지 (`RecordingPreSec`)
- mp4로 파일 저장, `RecordingMaxFileMB` 초과 시 파일 분할
- `WantsFrames()`: 녹화 중 여부 — IdleBypass 판단에도 사용

### 4.8 성능 최적화 (IdleBypass)

bypass 모드 + 로컬 소비자 없음(MJPEG 클라이언트 0 + 녹화 중 아님)일 때:

1. `captureLoop`이 1fps 저주기로 캡처 (스냅샷 유지용)
2. `Camera.Stream()`은 전체 파이프라인(PTZ/변환/타임스탬프/클론/팬아웃)을 생략하고 스냅샷용 lastFrame만 갱신
3. MJPEG 클라이언트 접속 즉시 원래 파이프라인 복귀

→ NVR이 go2rtc만 보고 있을 때 igocam 측 CPU를 크게 절감한다.

---

## 5. 실행 흐름 (cmd/igocam/main.go)

```
main
 ├─ loadDotEnv(.env)            # 실제 환경변수 우선: IGOCAM_CONFIG/PORT/LOG_LEVEL/ADMIN_USER/ADMIN_PASS
 ├─ config.LoadAll + Validate   # 카메라 배열 로드, ID 자동 생성, 검증
 ├─ findGo2rtc()                # 실행 파일 동일 디렉터리 → PATH 순
 ├─ manager.Start()             # 카메라별: FPS 프로브 → bypass 검증 → go2rtc yaml/프로세스
 │                              #   → HTTPServer(onvif_port) → camera.Start(streamer) → captureLoop
 ├─ admin.Start(8080)           # 관리 UI + REST
 └─ RunForever()                # SIGINT/SIGTERM 대기, 종료 시 전체 정지
```

---

## 6. 설정 (CameraConfig)

필드 그룹:

- **식별**: `id`(자동 생성), `name`, `manufacturer/model/serial_number/firmware_version` (ONVIF 응답에 노출)
- **포트**: `onvif_port`, `rtsp_port`, `rtmp_port`, `web_port`, `go2rtc_api_port`, `webrtc_port`
- **메인 스트림**: `main_width/height/fps/bitrate`, `main_stream_name`, `main_keyframe_interval`, `main_options`
- **서브 스트림**: `sub_*` (동일 구조)
- **소스**: `source`(파일/URL/디바이스 번호), `source_type`, `source_info`
- **인코딩**: `hw_accel`(cpu/nvenc/qsv/videotoolbox), `bypass`
- **화면**: `show_timestamp`(format/position), `flip/mirror/rotation`
- **녹화**: `recording_enabled/format/path/max_file_mb/pre_seconds`
- **URL**: `mjpeg_url`(기본 stream.mjpeg), `snapshot_url`(기본 snapshot.jpg)
- **ONVIF 인증**: `username/password` (비면 인증 비활성화)

운영 노트:

- admin CRUD는 `SaveAll`로 배열 전체를 원자적 파일 쓰기
- 포트 충돌 방지: 카메라 추가 시 `FindAvailablePorts` 제안 (+10 단위)

---

## 7. 운영 경험 / 알려진 제약

| 항목 | 내용 |
|---|---|
| HW 인코더 동시 세션 | macOS VideoToolbox 동시 세션 용량 초과 시 인코딩 speed 저하 (11대에서 0.24x~0.65x 관측). 4대 이하 운영 또는 `hw_accel: cpu` 분산 권장 |
| VP9/AV1 4K | OpenCV 소프트웨어 디코딩 실시간 불가. H264 소스 사용. bypass 모드는 AV1을 `#video=h264`로 자동 재인코딩 |
| bypass 파일 루프 | go2rtc는 producer를 자동 재시작하지 않음 → `#input=loop`(-stream_loop)로 해결. RTSP 소스는 루프 적용 안 함 |
| 구버전 바이너리 | 재시작 없이 수정 반영 안 됨 — 프로세스 시작 시각 vs 바이너리 빌드 시각 비교로 진단 가능 |
| go2rtc 고아 프로세스 | igocam 비정상 종료 시 go2rtc가 포트 점유. `ps aux | grep go2rtc`로 확인 후 정리 |
| 포트 중복 이름 | go2rtc yaml은 카메라 `name` 기반 (`data/go2rtc_{name}.yaml`) — 동일 name 카메라 2대는 같은 yaml 파일 공유 (잠재 이슈, 현재는 시작 순서 보존으로 동작) |
| WebRTC ICE | candidates 하드코딩 금지 (Docker 전용 호스트명은 네이티브에서 DNS 미해석) → go2rtc 자동 감지 사용 |

---

## 8. 예제 (examples/)

| 예제 | 내용 |
|---|---|
| `basic` | 웹캠 스트리밍 최소 구성 |
| `video_file` | 파일 소스 재생 (경로 인자) |
| `generated_content` | 프로그램 생성 영상 (카메라 디바이스 대체) |
| `ptz_demo` | PTZ 자동 데모 |
| `hardware_ptz` | `HardwareHandler` 구현 (서보 연결) |
| `custom_config` | `CameraConfig` 직접 생성 → `Save()` → 전체 배선 |
| `runner` | 예제 공용 헬퍼 (go2rtc + HTTP 서버 배선) |

```bash
go run ./examples/basic/
go run ./examples/video_file/ your_video.mp4
go run ./examples/ptz_demo/
```

---

## 9. 테스트 / 검증

- 전체: `go test ./...` / 정적 분석: `go vet ./...`
- 주요 테스트: `config`(검증/저장), `frame`(큐 drop/참조), `gostream`(yaml 생성), `httpserver`(핸들러), `onvif`(SOAP 회귀 6개), `ptz`, `recorder`, `mjpeg`, `streamer`
- 실측 방식 (서버 기동 상태에서):
  - RTSP: `ffmpeg -rtsp_transport tcp -i rtsp://... -t 2 -f null -`
  - go2rtc 스트림 상태: `curl http://127.0.0.1:{api}/api/streams?src=video_main`
  - 스냅샷/JPEG 유효성, MJPEG 첫 프레임, ffmpeg 프로세스 명령줄 인자 확인
