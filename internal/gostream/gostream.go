// go2rtc 서브프로세스 관리자 - 수명 주기 + 헬스체크
package gostream

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Config go2rtc 설정.
type Config struct {
	// Go2rtcBin go2rtc 바이너리 경로. 비어 있으면 PATH에서 찾음.
	Go2rtcBin string
	// ConfigPath go2rtc.yaml 경로.
	ConfigPath string
	// APIPort go2rtc API 포트 (헬스체크용).
	APIPort int
	// RTSPPort go2rtc RTSP 포트.
	RTSPPort int
	// RTMPPort go2rtc RTMP 포트.
	RTMPPort int
	// WebRTCPort go2rtc WebRTC 포트.
	WebRTCPort int
	// MainStream 메인 스트림 이름.
	MainStream string
	// SubStream 서브 스트림 이름.
	SubStream string
}

// New go2rtc 관리자 생성.
func New(cfg Config) *Manager {
	if cfg.Go2rtcBin == "" {
		cfg.Go2rtcBin = "go2rtc"
	}
	if cfg.APIPort == 0 {
		cfg.APIPort = 1984
	}
	if cfg.RTSPPort == 0 {
		cfg.RTSPPort = 8554
	}
	if cfg.RTMPPort == 0 {
		cfg.RTMPPort = 1935
	}
	if cfg.WebRTCPort == 0 {
		cfg.WebRTCPort = 8555
	}
	if cfg.MainStream == "" {
		cfg.MainStream = "video_main"
	}
	if cfg.SubStream == "" {
		cfg.SubStream = "video_sub"
	}
	return &Manager{cfg: cfg}
}

// Manager go2rtc 서브프로세스 관리자.
type Manager struct {
	cfg    Config
	cmd    *exec.Cmd
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
}

// SetOutput 로그 출력을 설정한다.
func (m *Manager) SetOutput(stdout, stderr io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stdout = stdout
	m.stderr = stderr
}

// ConfigPath 설정 파일 경로 반환.
func (m *Manager) ConfigPath() string { return m.cfg.ConfigPath }

// APIPort API 포트 반환.
func (m *Manager) APIPort() int { return m.cfg.APIPort }

// Start go2rtc 서브프로세스를 시작한다.
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.cmd != nil {
		m.mu.Unlock()
		return nil
	}
	args := []string{"-c", m.cfg.ConfigPath}
	cmd := exec.Command(m.cfg.Go2rtcBin, args...)
	m.mu.Unlock()

	// stdout/stderr 연결.
	m.mu.Lock()
	if m.stdout != nil {
		cmd.Stdout = m.stdout
	}
	if m.stderr != nil {
		cmd.Stderr = m.stderr
	}
	m.mu.Unlock()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("go2rtc start failed: %w", err)
	}
	m.mu.Lock()
	m.cmd = cmd
	m.mu.Unlock()

	// API 포트가 응답할 때까지 대기.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m.apiReady() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("go2rtc started but API not ready within 5s")
}

// Stop go2rtc 서브프로세스를 정지한다.
func (m *Manager) Stop() {
	m.mu.Lock()
	cmd := m.cmd
	m.cmd = nil
	m.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

// Running 실행 중인지 확인.
func (m *Manager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.Process != nil
}

// apiReady go2rtc API 응답 여부.
func (m *Manager) apiReady() bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/api", m.cfg.APIPort)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// CheckRTSP RTSP 포트 연결 가능 여부.
func (m *Manager) CheckRTSP() bool {
	return checkTCPPort(m.cfg.RTSPPort)
}

// checkTCPPort 지정 포트에 TCP 연결 가능 여부.
func checkTCPPort(port int) bool {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// WriteGo2rtcConfig 한 카메라의 go2rtc.yaml을 생성한다.
// IPyCam 패턴: 카메라마다 자체 go2rtc 인스턴스 (자체 포트) 사용.
// Source가 비어 있지 않으면 bypass 모드: go2rtc가 소스를 직접 읽어
// 트랜스코딩 없이 전송한다 (서브는 메인을 참조 복사).
func WriteGo2rtcConfig(path string, cam CameraStream) error {
	var sb strings.Builder
	sb.WriteString("streams:\n")
	if cam.Source != "" {
		sb.WriteString(fmt.Sprintf("  %s: %s\n", cam.MainStream, cam.Source))
		sb.WriteString(fmt.Sprintf("  %s: %s\n", cam.SubStream, cam.MainStream))
	} else {
		sb.WriteString(fmt.Sprintf("  %s: null\n", cam.MainStream))
		sb.WriteString(fmt.Sprintf("  %s: null\n", cam.SubStream))
	}
	sb.WriteString(fmt.Sprintf("\napi:\n  listen: \":%d\"\n", cam.APIPort))
	sb.WriteString(fmt.Sprintf("rtsp:\n  listen: \":%d\"\n", cam.RTSPPort))
	sb.WriteString(fmt.Sprintf("rtmp:\n  listen: \":%d\"\n", cam.RTMPPort))
	sb.WriteString(fmt.Sprintf("webrtc:\n  listen: \":%d\"\n", cam.WebRTCPort))
	sb.WriteString("  candidates:\n    - host.docker.internal:8555\n")

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}

// CameraStream 카메라 스트림/포트 정보.
type CameraStream struct {
	MainStream string
	SubStream  string
	APIPort    int
	RTSPPort   int
	RTMPPort   int
	WebRTCPort int
	// Source가 비어 있지 않으면 bypass 모드: go2rtc가 이 소스를 직접 읽는다.
	Source string
}
