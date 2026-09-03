# 아키텍처 다이어그램 구현 플랜

- 작성: 2026-09-03 17:40
- 목표: `doc/architecture.md`를 시각화한 self-contained HTML 다이어그램 생성 (`doc/architecture.html`)
- 스킬: architecture-diagram (다크 테마 SVG + 내보내기 툴바 템플릿)

## 플랜

다크 테마 단일 HTML로 igocam 전체 아키텍처를 표현한다.

### 다이어그램 레이아웃 (viewBox 1360×900)

```
[소스(slate)]      [igocam 프로세스 — amber dashed region]      [go2rtc region]   [클라이언트(slate)]
 비디오 파일 ──┐    Row1: HTTPServer(cyan) · Manager(emerald) · Admin(rose)   go2rtc ×N       관리 브라우저
 웹캠 디바이스 ─┼──► Row2: captureLoop → Camera.Stream → Streamer            (ffmpeg: 직독)   카메라 브라우저
 RTSP URL    ──┘    Row3: MJPEG · Recorder(violet) · WS-Discovery      ▲                 NVR/VMS
                       └── RTMP push ──────────────────────────────┘
 [bypass dashed rose: 소스 → go2rtc 직결, 하단 우회 경로]
 [범례: 모든 경계 밖, y=660+]
```

### 색상 매핑
- emerald: igocam 내부 파이프라인 (captureLoop/Camera.Stream/Streamer/MJPEG/Manager)
- cyan: HTTPServer (Web UI 프론트)
- amber: go2rtc 서브프로세스 + 프로세스/리전 경계
- violet: Recorder (저장소)
- rose: Admin Server (인증/관리) + bypass 직결 화살표
- slate: 외부 소스/클라이언트/WS-Discovery

### 화살표 (12개)
소스×3 → captureLoop, captureLoop → Camera.Stream (frame.Frame), Camera.Stream → Streamer/MJPEG/Recorder (팬아웃), Streamer → go2rtc (RTMP push), Admin ↔ Manager, 관리 브라우저 → Admin(:8080), 카메라 브라우저 → HTTPServer(:onvif_port) + → go2rtc(미리보기), go2rtc → NVR(RTSP/WebRTC), bypass dashed (소스 → go2rtc)

### 요약 카드 3개
1. 데이터 파이프라인 — 참조 카운팅 Frame, drop-oldest, 팬아웃
2. Bypass 모드 — 소스 직독, -stream_loop 무한 루프, #video=h264, IdleBypass
3. 포트 구성 — onvif_port 통합 / go2rtc 3포트 / 관리 :8080

## 체크리스트

- [x] 플랜 파일 작성
- [x] doc/architecture.html 생성 (템플릿 구조·내보내기 툴바/SRI 해시 유지)
- [x] 레이아웃 검증: 컴포넌트 겹침 0, 리전 침범 0, 범례 경계 밖 (y=710 > 리전 bottom 610), viewBox 이탈 0 — 파이썬 좌표 검증 + headless Chrome 스크린샷 생성 (이미지 뷰 미지원으로 좌표 검증으로 대체 확인)
- [x] MEMORY.md 기록

## 컨텍스트 노트

- (2026-09-03 23:10) JSON 인자 크기 초과로 write 실패 → bash heredoc 2청크로 분리 작성 성공.
- (2026-09-03 23:10) 카메라 브라우저는 HTTPServer에만 화살표 연결, go2rtc 미리보기는 go2rtc 박스 내 주석("브라우저 미리보기: stream.html")으로 표현 — 클라이언트 3번째 화살표 경로가 go2rtc 리전을 가로지르는 문제 회피.
- (2026-09-03 23:10) SVG 좌표 검증 스크립트로 rect 겹침/리전 침범/범례 위치 자동 확인 — 이미지 뷰가 불가한 환경에서의 검증 대안.
