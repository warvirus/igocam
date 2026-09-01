# 관리 서버 + 카메라 CRUD + 서비스 제어 기획안 (v2)

## ⚠️ 추가 발견: 카메라별 WebUI 저장 시 config 배열 파괴 (2026-09-01)

**증상**: 관리 화면(8080)에서 카메라 클릭 → 카메라별 WebUI(onvif_port) 열림 → 설정 저장 → `camera_config.json`이 **배열에서 단일 객체 1개로** 바뀜.

**원인**:
- `camera_config.json`은 multi 카메라 모드에서 **JSON 배열** 구조.
- 카메라별 WebUI의 저장 경로: `internal/httpserver/server.go:290` → `s.config.Save("")`
- `config.CameraConfig.Save()` (config.go:488)는 **현재 카메라 1개를 단일 객체로 직렬화**해 `ConfigPath`(= 공유 배열 파일)에 **전체를 덮어씀** → 배열 파괴.

**해결책 (사용자 제안 "포트 매칭"보다 더 나은 방법: ID 기반 매칭)**:
`Save()`를 **멀티 카메라 인지형**으로 개선:
1. 대상 파일이 **JSON 배열**이면:
   - 배열 내에서 `id` 필드로 해당 카메라 항목을 찾음 (없으면 `onvif_port`로 폴백 매칭)
   - 찾으면 → 해당 항목만 교체 (다른 카메라 보존)
   - 없으면 → 배열 끝에 **추가**
   - 전체 배열을 atomic write로 저장
2. 대상 파일이 **단일 객체**(레거시 단일 카메라 모드) 또는 **없으면** → 기존처럼 단일 객체 저장

**변경 파일**:
- `internal/config/config.go`: `Save()` 로직 개선 + `saveIntoArray`/`saveSingle` 헬퍼 분리. `SaveAll`은 유지.
- httpserver/server.go는 변경 불필요 (`s.config.Save("")` 그대로, Save가 배열 인지).

**검증 계획**:
- 배열 파일(카메라 8대)에서 cam1 WebUI 설정 변경 저장 → 배열 8개 유지, cam1 항목만 변경 확인
- cam2 WebUI 저장 → cam2만 변경, 나머지 보존
- 단일 객체 파일(레거시) 저장 → 단일 객체 유지
- 새 카메라(배열에 없는 onvif_port) 저장 → 배열에 추가
- go vet + 전체 테스트 green

---

## 1. 아키텍처 개요

```
┌──────────────────────────────────────────────────────────┐
│  cmd/igocam/main.go                                      │
│  --config (기존) --port (신규, 기본 8080)                 │
│  --admin-user / --admin-pass (신규, 선택)                 │
├──────────────────────────────────────────────────────────┤
│  Manager (기존 확장)                                      │
│  ├─ Start()                  ← 전체 기동 (기존)          │
│  ├─ AddCamera(cfg)           ← 신규: 카메라 동적 추가     │
│  ├─ RemoveCamera(id)         ← 신규: 카메라 동적 제거     │
│  ├─ UpdateCamera(id,updates) ← 신규: 설정 변경 + 재시작   │
│  ├─ ReloadFromConfig()       ← 신규: config diff 동기화  │
│  ├─ PauseStreams() / ResumeStreams() ← 신규: 스트림 일시정지/재개
│  ├─ StartAll() / StopAll()   ← 신규: 전체 서비스 제어     │
│  └─ Cameras() ← 기존 보유                                 │
├──────────────────────────────────────────────────────────┤
│  Admin Server (신규) ← http://0.0.0.0:8080              │
│  ├─ HTML/JS: 카메라 목록 + 추가/수정/삭제 + 서비스 제어    │
│  ├─ REST API: GET/POST/PUT/DELETE /api/cameras           │
│  ├─ POST /api/reload, /api/start-all, /api/stop-all,      │
│  │   /api/pause-streams, /api/resume-streams             │
│  ├─ GET /api/cameras/{id}/snapshot (프록시 썸네일)         │
│  └─ 선택적 Basic Auth                                     │
├──────────────────────────────────────────────────────────┤
│  Per-Camera (기존 유지) ← http://ip:onvif_port/         │
│    Web UI + MJPEG + PTZ + ONVIF + 업로드                 │
└──────────────────────────────────────────────────────────┘
```

**핵심:** 관리 서버는 `manager`에 대한 참조를 보유하고, CRUD·서비스 제어 요청을 manager에 위임한다. manager는 config 파일 저장 후 runtime에 카메라를 추가/제거/재시작한다. 개별 카메라 Web UI는 기존 `onvif_port` 서버를 그대로 사용하며, 관리 화면은 새 탭 링크로 연결된다.

---

## 2. 작업 목록

### Phase 1 — 기반 작업 (config + manager)

- [ ] **config.go**: `CameraConfig`에 `ID string json:"id"` 필드 추가 (비편집 필드, 로드 시 없으면 자동 생성). `SaveAll(path, configs)` 함수 추가 (atomic write).
- [ ] **config.go**: ID 생성 로직 (uuid). `Load`/`LoadAll`에서 ID 누락 시 생성, 기존 ID 유지.
- [ ] **manager.go**: `AddCamera(cfg) (*camera.Camera, error)` — go2rtc config 쓰기 → goStream Start → HTTP 서버 생성/주입 → cam.Start → captureLoop goroutine 시작 → 목록 추가.
- [ ] **manager.go**: `RemoveCamera(id string) error` — cam.Stop → goStream.Stop → captureLoop 종료 → 목록 제거. discovery 서비스 갱신.
- [ ] **manager.go**: `UpdateCamera(id string, updates map[string]any) (*camera.Camera, error)` — 필드 검증 → config 변경 → 재시작 필요 시 restart (RestartStream 가능 변경 / Remove+Add 불가능 변경).
- [ ] **manager.go**: `CameraByID(id string) *camera.Camera`, `FindAvailablePorts() PortSet`.
- [ ] **manager.go**: `ReloadFromConfig() (added, updated, removed []string, err error)` — 디스크 config 재읽기 → 현재 실행 상태와 diff:
  - 파일에 있고 실행 중에 없음 → AddCamera
  - 둘 다 있지만 필드 변경 → UpdateCamera
  - 실행 중에 있고 파일에 없음 → RemoveCamera
  - 포트 충돌/무결성 오류 시 부분 적용 + 에러 보고
- [ ] **manager.go**: `PauseStreams()` / `ResumeStreams()` — 각 camera의 streamer를 pause/resume. (streamer에 Pause/Resume 상태 추가 필요 — 아래 Phase 1b)
- [ ] **manager.go**: `StartAll()` / `StopAll()` — 전체 카메라 시작/완전 정지 (기존 Start/Stop 로직 재사용).

### Phase 1b — Streamer Pause/Resume

- [ ] **streamer.go**: `Pause() error` / `Resume() error` 추가. Pause 시 writeLoop가 stdin에 프레임 쓰기 중단(마지막 프레임 고정), ffmpeg는 유지하되 RTMP push만 중단. Resume 시 재개.
  - 구현 방향: writeLoop에 pause atomic.Bool 체크. paused면 프레임을 drop하고 대기. go2rtc에는 데이터가 안 가므로 스트림 일시정지 효과.
  - 대안: go2rtc API로 해당 카메라 스트림 stop/start. 단, streamer Pause가 더 단순.

### Phase 2 — 관리 서버 (Admin HTTP Server)

- [ ] **internal/admin/ — 신규 패키지**. `admin.Server`:
  - `port int`, `manager *manager.Manager`, `adminUser/adminPass string`
  - `New(port, mgr, adminUser, adminPass)`, `Start() error`, `Stop() error`
- [ ] **REST API `/api/cameras`**:
  - `GET /api/cameras` — 목록 (id, name, source, hw_accel, onvif_port, status, Web UI URL, snapshot URL)
  - `POST /api/cameras` — 추가 (포트 생략 시 자동제안)
  - `PUT /api/cameras/{id}` — 변경
  - `DELETE /api/cameras/{id}` — 삭제
- [ ] **서비스 제어 API**:
  - `POST /api/reload` — ReloadFromConfig 실행 → diff 결과 JSON 반환
  - `POST /api/start-all`, `POST /api/stop-all` — 전체 서비스 시작/정지
  - `POST /api/pause-streams`, `POST /api/resume-streams` — 스트림 일시정지/재개
  - `GET /api/status` — 전체 상태 스냅샷 (각 카메라 running/paused + 스트리밍 모드)
- [ ] **스냅샷 프록시**: `GET /api/cameras/{id}/snapshot` — 내부적으로 해당 카메라 onvif_port의 `/snapshot.jpg`를 http.Get으로 가져와 반환. 실패 시 placeholder 이미지.
- [ ] **Auth 미들웨어**: `adminUser/adminPass` 설정 시 `/api/*`에 Basic Auth. 설정 안 되면 인증 없음.
- [ ] **정적 파일 라우팅**: `/` → 리스트 페이지, `/static/` → css/js (Tailwind CDN 포함).

### Phase 3 — 관리 UI (HTML/JS) — Tailwind CSS

- [ ] **frontend-design 스킬 설치 (전제 조건)**: 구현 전에 오픈 스킬 생태계에서 frontend-design 스킬을 설치 (`npx skills find frontend-design` → `npx skills add <owner/repo>@frontend-design -g -y`). UI 설계·시각 디자인 지침을 구현 에이전트가 로드해 적용한다. (미설치 시에도 Tailwind로 진행하되 스킬 우선)
- [ ] **카메라 리스트 페이지 `index.html`** (Tailwind CDN + 커스텀 CSS):
  - **헤더**: 관리자 타이틀, 새로고침(reload) 버튼, 서비스 제어 버튼 그룹 (전체 시작 / 전체 정지 / 스트림 멈춤 / 스트림 재개)
  - **카메라 그리드/카드**: 각 카드에 snapshot 썸네일(프록시 URL), 이름, 상태 뱃지(● running / ⏸ paused / ● stopped), 소스, 인코더, 포트, Web UI 링크
  - 카드 클릭 → 개별 Web UI 새 탭
  - 카드에 "수정"/"삭제" 버튼
  - 상단 "카메라 추가" 버튼
  - 상태는 `/api/status`를 3~5초 주기로 폴링하여 갱신
- [ ] **카메라 추가/수정 폼 (모달)**: 기본 정보 + 포트(자동채움+편집) + 스트림/인코더 설정 + ONVIF 식별.
- [ ] **삭제 확인 다이얼로그**.
- [ ] **Reload 결과 토스트**: `POST /api/reload` 응답(added/updated/removed)을 토스트로 표시.
- [ ] **반응형/다크모드**: Tailwind 기반 세련된 디자인 (card, modal, badge, toast, skeleton loading).

### Phase 4 — CLI 연동

- [ ] **cmd/igocam/main.go**: `--port`(기본 8080), `--admin-user`, `--admin-pass` flag 추가. Manager Start 후 Admin Server Start. 종료 시 admin Stop 후 manager Stop. 배너에 관리 URL 출력.

---

## 3. 주요 설계 결정 (완료)

| 결정 | 선택 | 이유 |
|------|------|------|
| 추가/삭제 적용 | **실시간 반영** | UX 좋음. manager에 hot CRUD 구현 |
| 카메라 식별자 | **id 필드 도입** | 이름 중복/변경에도 안전한 CRUD |
| 포트 할당 | **자동제안 + 수동편집** | 편의성 + 유연성 |
| 관리 인증 | **선택적 인증** | `--admin-user/--admin-pass`로 활성화 |
| 개별 Web UI 접근 | **새 탭 링크** | 단순, 기존 UI 활용 |
| **Reload** | **config diff 동기화** | 수동 JSON 편집 → reload 버튼으로 반영 |
| **"멈춤" 의미** | **스트림 Pause** | ffmpeg 전송만 중단, 캡처/Web UI/ONVIF/PTZ 유지 |
| **서비스 제어 범위** | **전체(all) 버튼** | 상단 일괄 제어, 단순 명확 |
| **스냅샷 썸네일** | **프록시** | 8080 단일 포트 접속, 인증·혼합콘텐츠 문제 없음 |
| **UI 스타일** | **Tailwind CSS** | 현대적·세련된 디자인, CDN으로 간편 적용 |

---

## 4. 데이터 구조

### camera_config.json (id 필드 추가)
```json
[
  { "id": "cam_a1b2c3d4", "name": "Virtual Camera 1", "onvif_port": 8090, ... }
]
```
- `id`: 로드 시 없으면 자동 생성, 저장 시 유지. EditableFields에 없음(변경 불가).

### SaveAll(path, configs)
config 배열 전체를 atomic write로 저장 (임시 파일 → Rename).

### PortAllocator
```go
type PortSet struct {
    OnvifPort, RTSPPort, RTMPPort, Go2rtcAPIPort, WebPort, WebRTCPort int
}
func FindAvailablePorts(configs []*CameraConfig) PortSet
```
- 각 port family별 현재 최대값 +1부터, 사용 중인 포트 제외하고 순차 할당.

---

## 5. Manager 확장 (hot CRUD + 서비스 제어)

### AddCamera(cfg) 흐름
1. ID 할당(없으면) + 포트 할당(미지정 시) → configs에 추가 → `SaveAll`
2. go2rtc config 작성 → goStream Start → HTTP 서버 생성/주입 → cam.Start → captureLoop 시작 → `m.cameras`에 추가
3. discovery 갱신. 반환: `(*camera.Camera, error)`

### RemoveCamera(id) 흐름
1. id로 카메라 찾기 → cam.Stop (running=false → captureLoop 자연 종료) → goStream.Stop
2. 목록 제거 → `SaveAll` → discovery 갱신

### UpdateCamera(id, updates) 흐름
1. `ValidateUpdate` 검증 → `ApplyUpdates` 적용 → `SaveAll`
2. RestartFields 변경 → RestartStream (해당 스트림만) 또는 Remove+Add (포트 등 구조 변경)
3. 비RestartFields만 변경 → config 저장 + 필요한 경우 적용

### ReloadFromConfig() 흐름
1. 디스크 config 파일 재읽기 (`LoadAll`), ID 기준으로 실행 중 상태와 diff
2. 신규: AddCamera / 변경: UpdateCamera / 삭제: RemoveCamera
3. 부분 실패 시 에러 메시지와 함께 나머지 반영. 결과(added/updated/removed) 반환

### PauseStreams() / ResumeStreams()
- 각 카메라의 `Streamer.Pause()`/`Resume()` 호출. 카메라 상태에 paused 플래그 반영(`/api/status`에 노출).

### StartAll() / StopAll()
- StopAll: 모든 카메라 cam.Stop + goStream.Stop (완전 종료, config 유지). StartAll: 모든 카메라 재시작.

---

## 6. 위험 요소 & 엣지 케이스

| 리스크 | 대응 |
|--------|------|
| captureLoop 개별 종료 | `cam.IsRunning()` 조건 → cam.Stop 시 자연 종료. 검증 필요 |
| go2rtc config 잔여 | RemoveCamera 시 yaml 삭제 또는 재작성. OK |
| discovery 갱신 | hot CRUD 시 재빌드 필요. Phase 1: 재시작 반영, 별도 이슈 가능 |
| Pause 시 MJPEG/Web UI 유지 | Pause는 streamer만 중단. MJPEG/PTZ는 계속 동작 — 의도된 동작 |
| config 동시 쓰기 | Web UI save + admin SaveAll 충돌. Phase 1: 파일 Lock 생략, 단일 admin 경유 권장 |
| ID 중복/누락 | LoadAll에서 생성·유지 |
| 스냅샷 프록시 인증 | 카메라별 인증이 걸려 있으면 admin이 자격증명 전달 불가 → placeholder 처리 |
| 전체 Stop 후 상태 복원 | StopAll 후 재시작 시 config 기준 재기동. 상태 뱃지 정확히 표시 |

---

## 7. 검증 계획

1. 단위 테스트: SaveAll/LoadAll round-trip, ID 생성, FindAvailablePorts
2. 기동: `igocam --port 8080 --config camera_config.json` → 8080 접속 → 카메라 리스트 + snapshot 표시
3. 추가/수정/삭제 CRUD 검증 (config 파일 반영 + RTSP/Web UI 확인)
4. **Reload 검증**: config 파일 수동 편집(카메라 추가/삭제/변경) → reload 버튼 → diff 결과 반영 확인
5. **서비스 제어 검증**: 멈춤 → RTSP 프레임 중단 확인 + Web UI 유지 / 재개 → RTSP 재개 / 전체 정지 → go2rtc 종료 / 전체 시작 → 재기동
6. 반복 CRUD + reload 10회 → FDS/메모리 누수 없음
7. 인증 검증: --admin-user/--admin-pass → 401 → Basic Auth 시 200
8. 포트 중복 → 400 에러
9. 스냅샷 프록시: 카메라별 인증 없음 → 썸네일 정상 로드. 인증 있음 → placeholder
