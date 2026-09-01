// 여러 카메라를 한 프로세스에서 구동하는 오케스트레이터
package manager

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/camera"
	"igocam/internal/capture"
	"igocam/internal/config"
	"igocam/internal/discovery"
	"igocam/internal/gostream"
	"igocam/internal/httpserver"
	"igocam/internal/onvif"
)

// Manager 여러 카메라 + 공유 discovery + 캡처 스레드 오케스트레이터.
type Manager struct {
	configs   []*config.CameraConfig
	cameras   []*camera.Camera
	discovery *discovery.Server
	stopCh    chan struct{}
	wg        sync.WaitGroup

	// go2rtc 바이너리 경로 (빈 값이면 PATH).
	go2rtcBin string
}

// New 매니저 생성.
func New(configs []*config.CameraConfig) *Manager {
	return &Manager{
		configs: configs,
		stopCh:  make(chan struct{}),
	}
}

// SetGo2rtcBin go2rtc 바이너리 경로 설정.
func (m *Manager) SetGo2rtcBin(path string) {
	m.go2rtcBin = path
}

// Cameras 카메라 목록 반환.
func (m *Manager) Cameras() []*camera.Camera {
	return m.cameras
}

// Start 모든 카메라, 공유 discovery, 캡처 스레드를 시작한다.
// 어떤 카메라든 시작 실패 시 이미 시작된 것들을 모두 정지하고 false 반환.
func (m *Manager) Start() bool {
	for _, cfg := range m.configs {
		// 비디오 파일 소스는 원본 FPS를 main_fps로 반영해 실시간 속도로 재생한다.
		// (main_fps를 그대로 쓰면 30fps 소스가 15fps로 절반 속도 재생됨)
		srcType, _ := capture.InferSourceType(cfg.Source)
		if (srcType == capture.SourceVideoFile || srcType == capture.SourceRTSP) && cfg.Source != "video" {
			if fps := capture.ProbeFPS(cfg.Source); fps > 0 {
				// 반올림: 59.94fps(NTSC 60i) 등은 60fps로, 29.97fps는 30fps로.
				cfg.MainFPS = int(fps + 0.5)
				if cfg.MainFPS < 1 {
					cfg.MainFPS = 1
				}
				cfg.SubFPS = cfg.MainFPS
			}
		}

		// bypass는 파일/RTSP 소스에서만 의미가 있다. 카메라 디바이스/업로드 모드는
		// go2rtc가 직접 읽을 수 없으므로 일반 인코딩으로 강제한다.
		bypass := cfg.Bypass
		if bypass {
			switch srcType {
			case capture.SourceVideoFile, capture.SourceRTSP:
				// OK: go2rtc가 소스를 직접 읽어 트랜스코딩 없이 전송한다.
			default:
				fmt.Printf("[WARN] Camera '%s': bypass는 파일/RTSP 소스에만 적용됩니다 (현재 %s). 일반 인코딩으로 진행합니다.\n",
					cfg.Name, srcType)
				bypass = false
			}
		}
		cfg.Bypass = bypass

		cam := camera.New(cfg)

		// go2rtc 인스턴스 생성 + 시작 (카메라별 포트).
		go2rtcPath := filepath.Join("data", fmt.Sprintf("go2rtc_%s.yaml", cfg.Name))
		cs := gostream.CameraStream{
			MainStream: cfg.MainStreamName,
			SubStream:  cfg.SubStreamName,
			APIPort:    cfg.Go2rtcAPIPort,
			RTSPPort:   cfg.RTSPPort,
			RTMPPort:   cfg.RTMPPort,
			WebRTCPort: cfg.WebRTCPort,
		}
		if bypass {
			// go2rtc가 소스를 직접 읽어 트랜스코딩 없이 전송한다.
			// - 파일 소스: go2rtc는 로컬 파일 경로를 직접 지원하지 않으므로
			//   `ffmpeg:` 스킴으로 스트림 복사(copy, 재인코딩 없음)를 사용한다.
			// - RTSP/HTTP URL: go2rtc가 네이티브로 읽는다.
			switch srcType {
			case capture.SourceVideoFile:
				cs.Source = "ffmpeg:" + cfg.Source
			default:
				cs.Source = cfg.Source
			}
		}
		if err := gostream.WriteGo2rtcConfig(go2rtcPath, cs); err != nil {
			m.stopAll()
			return false
		}
		goStream := gostream.New(gostream.Config{
			Go2rtcBin:  m.go2rtcBin,
			ConfigPath: go2rtcPath,
			APIPort:    cfg.Go2rtcAPIPort,
			RTSPPort:   cfg.RTSPPort,
			RTMPPort:   cfg.RTMPPort,
			WebRTCPort: cfg.WebRTCPort,
		})
		if err := goStream.Start(); err != nil {
			fmt.Printf("[ERR] Camera '%s': go2rtc start failed: %v\n", cfg.Name, err)
			m.stopAll()
			return false
		}
		cam.SetGoStream(goStream)

		// HTTP 서버 주입.
		srv := httpserver.New(cam)
		cam.SetHTTPServer(srv)

		if !cam.Start(false) {
			fmt.Printf("[ERR] Camera '%s' failed to start\n", cfg.Name)
			m.stopAll()
			return false
		}
		m.cameras = append(m.cameras, cam)
		fmt.Printf("  [OK] Camera '%s' on :%d\n", cfg.Name, cfg.OnvifPort)
	}

	// 공유 WS-Discovery 응답기.
	svcs := make([]*onvif.Service, 0, len(m.cameras))
	for _, cam := range m.cameras {
		svcs = append(svcs, cam.Onvif)
	}
	m.discovery = discovery.New(svcs, 0)
	if err := m.discovery.Start(); err != nil {
		fmt.Printf("[WARN] WS-Discovery start failed: %v\n", err)
	} else {
		fmt.Printf("  WS-Discovery: shared responder for %d camera(s) on :3702\n", len(m.cameras))
	}

	// 캡처 스레드.
	for _, cam := range m.cameras {
		m.wg.Add(1)
		go m.captureLoop(cam)
	}
	return true
}

// RunForever SIGINT/SIGTERM을 받을 때까지 블로킹한다.
func (m *Manager) RunForever() int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
	m.Stop()
	return 0
}

// ConfigPath config 파일 경로 반환 (첫 번째 config 기준).
func (m *Manager) ConfigPath() string {
	if len(m.configs) == 0 {
		return ""
	}
	return m.configs[0].ConfigPath
}

// CameraByID ID로 카메라 조회.
func (m *Manager) CameraByID(id string) *camera.Camera {
	for _, c := range m.cameras {
		if c.Config.ID == id {
			return c
		}
	}
	return nil
}

// FindAvailablePorts 현재 등록된 카메라 포트를 기준으로 충돌 없는 포트 세트를 제안한다.
func (m *Manager) FindAvailablePorts() PortSet {
	used := map[int]bool{}
	for _, cfg := range m.configs {
		used[cfg.OnvifPort] = true
		used[cfg.RTSPPort] = true
		used[cfg.RTMPPort] = true
		used[cfg.Go2rtcAPIPort] = true
		used[cfg.WebPort] = true
		used[cfg.WebRTCPort] = true
	}
	next := func(base, step int) int {
		p := base
		for used[p] {
			p += step
		}
		used[p] = true
		return p
	}
	return PortSet{
		OnvifPort:     next(8090, 10),
		RTSPPort:      next(8553, 10),
		RTMPPort:      next(1934, 10),
		Go2rtcAPIPort: next(1984, 10),
		WebPort:       next(8091, 10),
		WebRTCPort:    next(8554, 10),
	}
}

// PortSet 카메라 포트 묶음.
type PortSet struct {
	OnvifPort     int
	RTSPPort      int
	RTMPPort      int
	Go2rtcAPIPort int
	WebPort       int
	WebRTCPort    int
}

// applyToConfig 포트 세트를 cfg에 적용한다.
func (ps PortSet) applyToConfig(cfg *config.CameraConfig) {
	cfg.OnvifPort = ps.OnvifPort
	cfg.RTSPPort = ps.RTSPPort
	cfg.RTMPPort = ps.RTMPPort
	cfg.Go2rtcAPIPort = ps.Go2rtcAPIPort
	cfg.WebPort = ps.WebPort
	cfg.WebRTCPort = ps.WebRTCPort
}

// startCamera 하나의 카메라를 시작한다 (AddCamera / StartAll에서 사용).
func (m *Manager) startCamera(cfg *config.CameraConfig) (*camera.Camera, error) {
	cam := camera.New(cfg)

	// go2rtc config 작성.
	go2rtcPath := filepath.Join("data", fmt.Sprintf("go2rtc_%s.yaml", cfg.Name))
	cs := gostream.CameraStream{
		MainStream: cfg.MainStreamName,
		SubStream:  cfg.SubStreamName,
		APIPort:    cfg.Go2rtcAPIPort,
		RTSPPort:   cfg.RTSPPort,
		RTMPPort:   cfg.RTMPPort,
		WebRTCPort: cfg.WebRTCPort,
	}
	if cfg.Bypass {
		srcType, _ := capture.InferSourceType(cfg.Source)
		switch srcType {
		case capture.SourceVideoFile:
			cs.Source = "ffmpeg:" + cfg.Source
		default:
			cs.Source = cfg.Source
		}
	}
	if err := gostream.WriteGo2rtcConfig(go2rtcPath, cs); err != nil {
		return nil, fmt.Errorf("write go2rtc config: %w", err)
	}
	goStream := gostream.New(gostream.Config{
		Go2rtcBin:  m.go2rtcBin,
		ConfigPath: go2rtcPath,
		APIPort:    cfg.Go2rtcAPIPort,
		RTSPPort:   cfg.RTSPPort,
		RTMPPort:   cfg.RTMPPort,
		WebRTCPort: cfg.WebRTCPort,
	})
	if err := goStream.Start(); err != nil {
		return nil, fmt.Errorf("go2rtc start: %w", err)
	}
	cam.SetGoStream(goStream)

	srv := httpserver.New(cam)
	cam.SetHTTPServer(srv)

	if !cam.Start(false) {
		goStream.Stop()
		return nil, fmt.Errorf("camera start failed")
	}
	return cam, nil
}

// AddCamera 새 카메라를 동적으로 추가하고 시작한다.
func (m *Manager) AddCamera(cfg *config.CameraConfig) (*camera.Camera, error) {
	cfg.EnsureID()
	if cfg.OnvifPort == 0 {
		ps := m.FindAvailablePorts()
		ps.applyToConfig(cfg)
	}
	cam, err := m.startCamera(cfg)
	if err != nil {
		return nil, err
	}
	m.configs = append(m.configs, cfg)
	m.cameras = append(m.cameras, cam)
	if err := config.SaveAll(cfg.ConfigPath, m.configs); err != nil {
		fmt.Printf("[WARN] Camera '%s': config save failed: %v\n", cfg.Name, err)
	}
	m.wg.Add(1)
	go m.captureLoop(cam)
	fmt.Printf("  [OK] Camera '%s' added on :%d\n", cfg.Name, cfg.OnvifPort)
	return cam, nil
}

// RemoveCamera 카메라를 동적으로 제거하고 정지한다.
func (m *Manager) RemoveCamera(id string) error {
	idx := -1
	var cam *camera.Camera
	for i, c := range m.cameras {
		if c.Config.ID == id {
			idx = i
			cam = c
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("camera not found: %s", id)
	}

	cam.Stop()
	if cam.GoStream != nil {
		cam.GoStream.Stop()
	}

	m.cameras = append(m.cameras[:idx], m.cameras[idx+1:]...)
	cfgIdx := -1
	for i, cfg := range m.configs {
		if cfg.ID == id {
			cfgIdx = i
			break
		}
	}
	if cfgIdx >= 0 {
		m.configs = append(m.configs[:cfgIdx], m.configs[cfgIdx+1:]...)
	}
	if err := config.SaveAll(m.ConfigPath(), m.configs); err != nil {
		fmt.Printf("[WARN] remove camera %s: config save failed: %v\n", id, err)
	}
	fmt.Printf("  [OK] Camera '%s' removed\n", cam.Config.Name)
	return nil
}

// UpdateCamera 카메라 설정을 변경하고 필요 시 재시작한다.
func (m *Manager) UpdateCamera(id string, updates map[string]any) error {
	cam := m.CameraByID(id)
	if cam == nil {
		return fmt.Errorf("camera not found: %s", id)
	}
	cfg := cam.Config
	applied, rejected, restartKeys := cfg.ApplyUpdates(updates)
	if len(rejected) > 0 {
		return fmt.Errorf("rejected fields: %v", rejected)
	}
	if len(applied) == 0 {
		return nil
	}
	if err := config.SaveAll(m.ConfigPath(), m.configs); err != nil {
		return fmt.Errorf("config save: %w", err)
	}
	if len(restartKeys) > 0 {
		return m.RestartCamera(id)
	}
	return nil
}

// RestartCamera 카메라를 재시작한다 (Remove + Add).
func (m *Manager) RestartCamera(id string) error {
	cam := m.CameraByID(id)
	if cam == nil {
		return fmt.Errorf("camera not found: %s", id)
	}
	cfg := cam.Config
	if err := m.RemoveCamera(id); err != nil {
		return err
	}
	_, err := m.AddCamera(cfg)
	return err
}

// ReloadFromConfig 디스크 config 파일을 다시 읽어 실행 중인 상태와 동기화한다.
// 수동으로 JSON을 편집한 변경사항을 반영한다.
func (m *Manager) ReloadFromConfig() (added, updated, removed []string, err error) {
	path := m.ConfigPath()
	if path == "" {
		return nil, nil, nil, fmt.Errorf("no config path")
	}
	diskConfigs, err := config.LoadAll(path)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load config: %w", err)
	}

	// 현재 실행 중인 ID 맵.
	running := map[string]int{} // id → camera index
	for i, cam := range m.cameras {
		running[cam.Config.ID] = i
	}

	// 디스크에 있지만 실행 중에 없는 것 → 추가.
	diskIDs := map[string]bool{}
	for _, dc := range diskConfigs {
		diskIDs[dc.ID] = true
		if _, exists := running[dc.ID]; !exists {
			if _, err := m.AddCamera(dc); err != nil {
				fmt.Printf("[WARN] Reload: add camera '%s' failed: %v\n", dc.Name, err)
				continue
			}
			added = append(added, dc.Name)
		}
	}

	// 실행 중에 있지만 디스크에 없는 것 → 제거.
	for _, cam := range m.cameras {
		if !diskIDs[cam.Config.ID] {
			if err := m.RemoveCamera(cam.Config.ID); err != nil {
				fmt.Printf("[WARN] Reload: remove camera '%s' failed: %v\n", cam.Config.Name, err)
				continue
			}
			removed = append(removed, cam.Config.Name)
		}
	}

	// 추가/제거 후 나머지에 대해 RestartStream으로 변경 반영.
	// (실제 diff 검증은 추후 고도화)
	return added, updated, removed, nil
}

// StartAll 모든 카메라를 시작한다 (이미 실행 중인 것은 무시).
func (m *Manager) StartAll() {
	for _, cfg := range m.configs {
		if m.CameraByID(cfg.ID) != nil {
			continue
		}
		if _, err := m.AddCamera(cfg); err != nil {
			fmt.Printf("[ERR] StartAll: camera '%s': %v\n", cfg.Name, err)
		}
	}
}

// StopAll 모든 카메라를 완전 정지한다 (config 유지).
func (m *Manager) StopAll() {
	for _, cam := range m.cameras {
		cam.Stop()
		if cam.GoStream != nil {
			cam.GoStream.Stop()
		}
	}
	m.cameras = nil
}

// PauseStreams 모든 카메라의 스트림 전송을 일시중지한다.
func (m *Manager) PauseStreams() {
	for _, cam := range m.cameras {
		cam.Streamer.Pause()
	}
}

// ResumeStreams 일시중지된 스트림 전송을 재개한다.
func (m *Manager) ResumeStreams() {
	for _, cam := range m.cameras {
		cam.Streamer.Resume()
	}
}

// Stop 캡처 루프, 카메라, discovery를 정지한다.
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
	for _, cam := range m.cameras {
		cam.Stop()
	}
	m.cameras = nil
	if m.discovery != nil {
		m.discovery.Stop()
		m.discovery = nil
	}
}

// stopAll 시작 중 실패 시 정리.
func (m *Manager) stopAll() {
	select {
	case <-m.stopCh:
	default:
		close(m.stopCh)
	}
	m.wg.Wait()
	for _, cam := range m.cameras {
		cam.Stop()
	}
	m.cameras = nil
	if m.discovery != nil {
		m.discovery.Stop()
		m.discovery = nil
	}
}

// captureLoop 한 카메라의 소스를 열어 프레임을 cam.Stream()에 공급한다.
func (m *Manager) captureLoop(cam *camera.Camera) {
	defer m.wg.Done()

	source := cam.Config.Source
	srcType, srcInfo := capture.InferSourceType(source)
	cam.Config.SourceType = string(srcType)
	cam.Config.SourceInfo = srcInfo

	if strings.ToLower(source) == "video" {
		m.videoUploadLoop(cam)
		return
	}

	src, srcType, _, err := capture.OpenSource(source)
	if err != nil {
		fmt.Printf("[ERR] Camera '%s': could not open source: %v\n", cam.Config.Name, err)
		return
	}
	defer src.Close()

	// 카메라 디바이스면 MJPG + 낮은 버퍼 설정.
	if srcType == capture.SourceCamera {
		if cs, ok := src.(*capture.CameraSource); ok {
			cs.Configure(cam.Config.MainWidth, cam.Config.MainHeight, cam.Config.MainFPS)
		}
	}
	if srcType == capture.SourceVideoFile {
		cam.SetVideoUploadMode(true)
		cam.SetCurrentVideoPath(absPath(source))
	}

	// 캡처 루프 자체는 페이싱하지 않는다. 페이싱은 camera.Stream 내부의
	// paceFrame()이 담당하며, Start()에서 MainFPS를 소스 FPS로 설정했으므로
	// 비디오 파일/RTSP는 원본 속도, 카메라 등은 main_fps로 실시간 재생된다.
	origSource := absPath(source)
	for cam.IsRunning() {
		select {
		case <-m.stopCh:
			return
		default:
		}

		// 파일 소스: 새 파일이 업로드되면 (GetCurrentVideoPath가 원본과 달라지면)
		// 업로드된 파일로 재오픈해 전환한다.
		if srcType == capture.SourceVideoFile {
			currentPath := cam.GetCurrentVideoPath()
			if currentPath != "" && currentPath != origSource {
				src.Close()
				source = currentPath
				origSource = currentPath
				newSrc, err := capture.NewVideoFileSource(source)
				if err != nil {
					cam.NotifyVideoError(fmt.Sprintf("Could not open video: %s", filepath.Base(source)))
					time.Sleep(500 * time.Millisecond)
					continue
				}
				src = newSrc
				cam.Config.SourceInfo = filepath.Base(source)
				cam.NotifyVideoLoaded(source)
			}
		}

		img, ok := src.Read()
		if !ok {
			img.Close()
			select {
			case <-m.stopCh:
				return
			default:
			}
			// 파일/URL이 끝나면 처음부터 반복 재생.
			if srcType == capture.SourceVideoFile || srcType == capture.SourceRTSP {
				newSrc, frameMat, looped := capture.LoopSource(src, source)
				if !looped {
					src = newSrc
					time.Sleep(time.Second)
					continue
				}
				src = newSrc
				// 루프 프레임도 구성 해상도로 리사이즈 후 스트림.
				resized := capture.ResizeFrame(frameMat, cam.Config.MainWidth, cam.Config.MainHeight)
				frameMat.Close()
				if resized.Empty() {
					resized.Close()
					continue
				}
				cam.Stream(&resized)
				resized.Close()
				continue
			}
			return
		}
		// 캡처 직후 리사이즈: 소스가 1080p/4K여도 파이프라인은 구성 해상도로 제한해
		// C 메모리 사용을 크게 줄인다.
		frame := capture.ResizeFrame(img, cam.Config.MainWidth, cam.Config.MainHeight)
		img.Close()
		if frame.Empty() {
			frame.Close()
			continue
		}
		cam.Stream(&frame)
		frame.Close()
	}
}

// videoUploadLoop source=='video': 업로드된 비디오를 재생하거나 플레이스홀더 표시.
func (m *Manager) videoUploadLoop(cam *camera.Camera) {
	cam.SetVideoUploadMode(true)
	for cam.IsRunning() {
		select {
		case <-m.stopCh:
			return
		default:
		}
		videoPath := cam.GetCurrentVideoPath()
		if videoPath == "" {
			// 플레이스홀더 프레임.
			placeholder := gocv.NewMatWithSize(cam.Config.MainHeight, cam.Config.MainWidth, gocv.MatTypeCV8UC3)
			placeholder.SetTo(gocv.NewScalar(30, 20, 10, 255))
			cam.Stream(&placeholder)
			placeholder.Close()
			time.Sleep(time.Second / time.Duration(cam.Config.MainFPS))
			continue
		}
		src, err := capture.NewVideoFileSource(videoPath)
		if err != nil {
			cam.NotifyVideoError(fmt.Sprintf("Could not open video: %s", filepath.Base(videoPath)))
			time.Sleep(500 * time.Millisecond)
			continue
		}
		cam.Config.SourceInfo = filepath.Base(videoPath)
		cam.NotifyVideoLoaded(videoPath)

		for cam.IsRunning() {
			select {
			case <-m.stopCh:
				src.Close()
				return
			default:
			}
			img, ok := src.Read()
			if !ok {
				img.Close()
				src.Close()
				break // 다음 루프에서 재오픈
			}
			// 업로드 비디오도 구성 해상도로 리사이즈.
			frame := capture.ResizeFrame(img, cam.Config.MainWidth, cam.Config.MainHeight)
			img.Close()
			if frame.Empty() {
				frame.Close()
				continue
			}
			cam.Stream(&frame)
			frame.Close()
		}
	}
}

func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
