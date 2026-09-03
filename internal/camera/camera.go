// 카메라 코어 파이프라인 - 프레임 변환, 팬아웃, 수명 주기 관리
package camera

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/config"
	"igocam/internal/frame"
	"igocam/internal/gostream"
	"igocam/internal/mjpeg"
	"igocam/internal/onvif"
	"igocam/internal/ptz"
	"igocam/internal/recorder"
	"igocam/internal/streamer"
)

// StreamingMode 스트리밍 모드.
type StreamingMode string

const (
	ModeGo2rtc StreamingMode = "go2rtc"
	ModeMJPEG  StreamingMode = "mjpeg"
)

// Camera 카메라 인스턴스 하나.
// 프레임 파이프라인(PTZ → display transforms → timestamp → outbound 클론 →
// MJPEG/recorder/go2rtc 팬아웃)과 모든 서브시스템의 수명 주기를 관리한다.
type Camera struct {
	Config *config.CameraConfig
	PTZ    *ptz.Controller

	// MJPEG: go2rtc 독립적 항상 사용 가능한 폴백.
	MJPEG *mjpeg.Streamer

	// Recorder: 항상 생성되지만 활성 녹화/프리레코드 시에만 동작.
	Recorder *recorder.Recorder

	// go2rtc 서브프로세스 관리자 + ffmpeg 인코딩 push 스트리머.
	GoStream *gostream.Manager
	Streamer *streamer.Streamer

	// ONVIF SOAP 서비스 (discovery 및 HTTP /onvif/* 엔드포인트에서 사용).
	Onvif *onvif.Service

	// HTTP 서버 인터페이스 (실제 서버는 외부에서 주입).
	HTTPServer HTTPServer

	mu               sync.Mutex
	running          atomic.Bool
	restarting       atomic.Bool
	useMjpegFallback atomic.Bool
	streamingMode    atomic.Value // StreamingMode

	// 프레임 파이프라인 상태
	streamStartTime time.Time
	frameCount      int64
	lastFPS         int

	// 스냅샷 (독립 복사본, 뮤텍스 보호)
	lastFrame     *gocv.Mat
	lastFrameLock sync.Mutex

	// 비디오 업로드 모드
	videoUploadMode  atomic.Bool
	currentVideoPath atomic.Pointer[string]
	videoError       atomic.Pointer[string]
}

// HTTPServer 카메라가 HTTP 서버에 노출하는 인터페이스.
// Phase 3에서 구현체가 주입된다.
type HTTPServer interface {
	Start() error
	Stop() error
}

// New 카메라 생성.
func New(cfg *config.CameraConfig) *Camera {
	ptzCtrl := ptz.New(cfg.MainWidth, cfg.MainHeight, 4.0, "ptz_presets.json")
	c := &Camera{
		Config:   cfg,
		PTZ:      ptzCtrl,
		MJPEG:    mjpeg.New(80, cfg.SubWidth, cfg.SubHeight),
		Recorder: recorder.New(cfg),
		Onvif:    onvif.New(cfg, ptzCtrl),
		Streamer: streamer.New(&streamer.Config{
			Width:               cfg.MainWidth,
			Height:              cfg.MainHeight,
			FPS:                 cfg.MainFPS,
			Bitrate:             cfg.MainBitrate,
			KeyframeInterval:    cfg.MainKeyframeInterval,
			HWAccel:             streamer.HWAccel(cfg.HWAccel),
			MainOptions:         cfg.MainOptions,
			SubWidth:            cfg.SubWidth,
			SubHeight:           cfg.SubHeight,
			SubBitrate:          cfg.SubBitrate,
			SubKeyframeInterval: cfg.SubKeyframeInterval,
			SubOptions:          cfg.SubOptions,
		}),
	}
	c.streamingMode.Store(StreamingMode(ModeGo2rtc))
	return c
}

// SetHTTPServer HTTP 서버를 주입한다.
func (c *Camera) SetHTTPServer(s HTTPServer) {
	c.HTTPServer = s
}

// SetGoStream go2rtc 관리자를 설정한다 (외부에서 주입).
func (c *Camera) SetGoStream(m *gostream.Manager) {
	c.GoStream = m
}

// Start 카메라를 시작한다. MJPEG 스트리머와 레코더(설정된 경우)를 시작.
// go2rtc+streamer는 manager가 사전에 시작해 주입해야 한다.
func (c *Camera) Start(discovery bool) bool {
	c.MJPEG.Start()
	if c.Config.RecordingEnabled {
		c.Recorder.Start()
	}
	// go2rtc가 준비됐으면 streamer 시작.
	if c.GoStream != nil && c.GoStream.Running() {
		if c.Config.Bypass {
			// bypass: go2rtc가 소스를 직접 읽어 트랜스코딩 없이 전송하므로
			// 로컬 ffmpeg 인코딩 스트리머는 시작하지 않는다.
			c.useMjpegFallback.Store(false)
			c.streamingMode.Store(StreamingMode(ModeGo2rtc))
		} else {
			mainURL := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", c.Config.RTMPPort, c.Config.MainStreamName)
			subURL := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", c.Config.RTMPPort, c.Config.SubStreamName)
			if c.Streamer.Start(mainURL, subURL) {
				c.useMjpegFallback.Store(false)
				c.streamingMode.Store(StreamingMode(ModeGo2rtc))
			} else {
				c.useMjpegFallback.Store(true)
				c.streamingMode.Store(StreamingMode(ModeMJPEG))
			}
		}
	} else {
		c.useMjpegFallback.Store(true)
		c.streamingMode.Store(StreamingMode(ModeMJPEG))
	}
	if c.HTTPServer != nil {
		if err := c.HTTPServer.Start(); err != nil {
			return false
		}
	}
	c.running.Store(true)
	return true
}

// Stop 모든 서브시스템을 정지한다.
func (c *Camera) Stop() {
	c.running.Store(false)
	c.PTZ.Stop()
	c.Streamer.Stop()
	if c.GoStream != nil {
		c.GoStream.Stop()
	}
	c.Recorder.Stop()
	c.MJPEG.Stop()
	if c.HTTPServer != nil {
		c.HTTPServer.Stop()
	}
	c.lastFrameLock.Lock()
	if c.lastFrame != nil {
		c.lastFrame.Close()
		c.lastFrame = nil
	}
	c.lastFrameLock.Unlock()
}

// IsRunning 카메라 실행 중 여부.
func (c *Camera) IsRunning() bool {
	return c.running.Load()
}

// StreamingMode 현재 스트리밍 모드 반환.
func (c *Camera) StreamingMode() string {
	if v, ok := c.streamingMode.Load().(StreamingMode); ok {
		return string(v)
	}
	return string(ModeGo2rtc)
}

// UsingMJPEGFallback MJPEG 폴백 사용 중인지 여부.
func (c *Camera) UsingMJPEGFallback() bool {
	return c.useMjpegFallback.Load()
}

// IdleBypass bypass 모드에서 로컬 소비자(MJPEG 클라이언트/레코더)가 없어
// 로컬 프레임 파이프라인이 불필요한 상태인지 여부.
// 이 경우 go2rtc가 소스를 직접 읽어 전송하므로 로컬 캡처/인코딩을 낮출 수 있다.
func (c *Camera) IdleBypass() bool {
	if !c.Config.Bypass {
		return false
	}
	if c.MJPEG != nil && c.MJPEG.ClientCount() > 0 {
		return false
	}
	if c.Recorder != nil && c.Recorder.WantsFrames() {
		return false
	}
	return true
}

// Stream 프레임을 파이프라인에 제출한다.
// 순서: PTZ → display transforms → timestamp → outbound 클론 → 팬아웃.
//
// outbound 프레임은 불변 계약 하에 있다: 매 반복마다 새 버퍼를 할당하며
// 소비자는 절대 변이하지 않는다. 이 계약 덕분에 다운스트림이 참조를
// 저장해도 복사할 필요가 없다.
func (c *Camera) Stream(src *gocv.Mat) {
	if !c.running.Load() {
		return
	}

	// bypass 모드에서 로컬 소비자(MJPEG 클라이언트/레코더)가 없으면
	// 무거운 파이프라인(PTZ/변환/타임스탬프/클론/팬아웃)을 건너뛰고
	// 스냅샷만 갱신한다. go2rtc가 소스를 직접 읽어 전송하므로 로컬 처리가 불필요하다.
	if c.IdleBypass() {
		c.lastFrameLock.Lock()
		if c.lastFrame != nil {
			c.lastFrame.Close()
		}
		snap := src.Clone()
		c.lastFrame = &snap
		c.lastFrameLock.Unlock()
		return
	}

	// 1. PTZ 변환
	ptzMat := gocv.NewMat()
	defer ptzMat.Close()
	c.PTZ.ApplyPTZ(src, &ptzMat)

	// 2. Display transforms (flip/mirror/rotate)
	transformed := gocv.NewMat()
	defer transformed.Close()
	frame.ApplyTransforms(&ptzMat, &transformed, c.Config.Flip, c.Config.Mirror, c.Config.Rotation)

	// 3. 타임스탬프 (변환 후 최종 방향에 그림)
	if c.Config.ShowTimestamp {
		pos := frame.ParseTimestampPosition(c.Config.TimestampPosition)
		fmt := frame.TimestampFormatGo(c.Config.TimestampFormat)
		frame.DrawTimestamp(&transformed, pos, fmt)
	}

	// 4. 불변 outbound 프레임 (참조 카운팅 래퍼, 1회 생성).
	// 각 소비자에 enqueue 전 Retain, enqueue 후 생산자 몫 Release.
	// 마지막 Release가 Mat을 Close한다 (비동기 워커와의 use-after-free 방지).
	outboundMat := transformed.Clone()
	f := frame.NewFrame(&outboundMat)
	defer f.Release()

	// 5. 스냅샷용 최신 프레임 저장 (요청 시 Clone하므로 여기서는 참조만 저장).
	// outboundMat는 Frame의 refcount로 관리되므로, lastFrame은 독립 Clone을 보관한다.
	// 매 프레임 Clone을 피할 수 없지만(GetSnapshotFrame이 언제든 호출 가능),
	// 이전 Clone은 Close되므로 메모리는 1 프레임으로 유계된다.
	c.lastFrameLock.Lock()
	if c.lastFrame != nil {
		c.lastFrame.Close()
	}
	snap := outboundMat.Clone()
	c.lastFrame = &snap
	c.lastFrameLock.Unlock()

	// 6. MJPEG 팬아웃 (비블로킹 enqueue).
	if c.MJPEG.ClientCount() > 0 {
		f.Retain()
		c.MJPEG.StreamFrame(f)
	}

	// 7. Recorder 팬아웃.
	if c.Recorder.WantsFrames() {
		f.Retain()
		c.Recorder.Submit(f)
	}

	// 8. go2rtc 팬아웃 (streamer writer 스레드가 리사이즈/인코딩).
	// bypass 모드에서는 go2rtc가 소스를 직접 읽으므로 로컬 인코딩 생략.
	if !c.Config.Bypass && !c.useMjpegFallback.Load() && c.Streamer != nil {
		f.Retain()
		c.Streamer.Stream(f)
	}

	// 9. 프레임 페이싱.
	c.paceFrame()
}

// GetSnapshotFrame 최신 프레임의 안전한 독립 복사본을 반환한다.
// HTTP 스냅샷 핸들러용 (캡처 스레드와 다른 스레드에서 호출).
func (c *Camera) GetSnapshotFrame() *gocv.Mat {
	c.lastFrameLock.Lock()
	defer c.lastFrameLock.Unlock()
	if c.lastFrame == nil || c.lastFrame.Empty() {
		return nil
	}
	clone := c.lastFrame.Clone()
	return &clone
}

// StartRecording 녹화를 시작한다.
func (c *Camera) StartRecording() bool {
	return c.Recorder.StartRecording()
}

// StopRecording 녹화를 종료하고 세그먼트 경로를 반환한다.
func (c *Camera) StopRecording() []string {
	return c.Recorder.StopRecording()
}

// IsRecording 녹화 중인지 여부.
func (c *Camera) IsRecording() bool {
	return c.Recorder.IsRecording()
}

// RecordingStats 레코더 상태 스냅샷.
func (c *Camera) RecordingStats() map[string]any {
	return c.Recorder.Stats()
}

// ApplyRecordingConfig 설정 변경 후 레코더를 재조정한다.
func (c *Camera) ApplyRecordingConfig() {
	c.Recorder.Reconfigure()
	if c.Config.RecordingEnabled && !c.Recorder.WorkerRunning() {
		c.Recorder.Start()
	} else if !c.Config.RecordingEnabled && c.Recorder.WorkerRunning() && !c.Recorder.IsRecording() {
		c.Recorder.Stop()
	}
}

// RestartStream 스트리머를 재시작한다.
func (c *Camera) RestartStream() bool {
	c.restarting.Store(true)
	defer c.restarting.Store(false)
	c.PTZ.OutputWidth = c.Config.MainWidth
	c.PTZ.OutputHeight = c.Config.MainHeight
	return true
}

// paceFrame 프레임 페이싱을 유지한다.
// windup 방지: 실제 시간이 예상 시간보다 한 프레임 이상 뒤쳐지면
// 스케줄을 재설정해 CPU를 100% 소모하지 않게 한다.
func (c *Camera) paceFrame() {
	now := time.Now()
	fps := c.Config.MainFPS
	if fps < 1 {
		fps = 30
		c.Config.MainFPS = 30
	}
	targetFrameTime := time.Second / time.Duration(fps)

	// 첫 프레임이거나 FPS가 바뀌었거나 한 프레임 이상 뒤쳐졌으면 재설정.
	if c.streamStartTime.IsZero() || c.lastFPS != fps {
		c.streamStartTime = now
		c.frameCount = 0
		c.lastFPS = fps
	}

	c.frameCount++
	expected := c.streamStartTime.Add(time.Duration(c.frameCount) * targetFrameTime)
	behind := now.Sub(expected)
	if behind > 0 {
		// 뒤쳐짐: windup 방지를 위해 재설정.
		if behind > targetFrameTime {
			c.streamStartTime = now
			c.frameCount = 0
		}
		// 여전히 뒤쳐져 있으면 최소한의 sleep으로 CPU 스핀 방지.
		time.Sleep(time.Millisecond)
		return
	}
	// 앞서 있음: sleep으로 페이싱.
	time.Sleep(expected.Sub(now))
}

// VideoUploadMode 비디오 업로드 모드 설정.
func (c *Camera) SetVideoUploadMode(enabled bool) {
	c.videoUploadMode.Store(enabled)
}

// VideoUploadModeValue 비디오 업로드 모드 여부.
func (c *Camera) VideoUploadModeValue() bool {
	return c.videoUploadMode.Load()
}

// GetCurrentVideoPath 현재 비디오 파일 경로 반환.
func (c *Camera) GetCurrentVideoPath() string {
	ptr := c.currentVideoPath.Load()
	if ptr == nil {
		return ""
	}
	return *ptr
}

// SetCurrentVideoPath 현재 비디오 파일 경로 설정.
func (c *Camera) SetCurrentVideoPath(path string) {
	c.currentVideoPath.Store(&path)
}

// GetVideoError 마지막 비디오 오류 반환.
func (c *Camera) GetVideoError() string {
	ptr := c.videoError.Load()
	if ptr == nil {
		return ""
	}
	return *ptr
}

// NotifyVideoError 비디오 오류를 알린다.
func (c *Camera) NotifyVideoError(errMsg string) {
	c.videoError.Store(&errMsg)
}

// NotifyVideoLoaded 비디오 로드 성공을 알린다.
func (c *Camera) NotifyVideoLoaded(path string) {
	c.videoError.Store(nil)
	c.Config.SourceInfo = path
}

// CleanupOldVideos 이전 비디오 파일을 정리한다.
func (c *Camera) CleanupOldVideos(currentPath string) {
	// Manager에서 처리하므로 여기서는 빈 구현.
}

// GetWebUIHTML 웹 UI HTML 템플릿을 렌더링한다.
// Phase 3에서 http.ServeStatic에서 처리.
func (c *Camera) GetWebUIHTML() string {
	return ""
}
