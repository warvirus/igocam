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

		cam := camera.New(cfg)

		// go2rtc 인스턴스 생성 + 시작 (카메라별 포트).
		go2rtcPath := filepath.Join("data", fmt.Sprintf("go2rtc_%s.yaml", cfg.Name))
		if err := gostream.WriteGo2rtcConfig(go2rtcPath, gostream.CameraStream{
			MainStream:  cfg.MainStreamName,
			SubStream:   cfg.SubStreamName,
			APIPort:     cfg.Go2rtcAPIPort,
			RTSPPort:    cfg.RTSPPort,
			RTMPPort:    cfg.RTMPPort,
			WebRTCPort:  cfg.WebRTCPort,
		}); err != nil {
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
	for cam.IsRunning() {
		select {
		case <-m.stopCh:
			return
		default:
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
