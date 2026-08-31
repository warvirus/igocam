// go2rtc RTMP push용 비디오 스트리머 - ffmpeg 인코딩 + 하드웨어 가속 사다리
package streamer

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/frame"
)

// HWAccel 하드웨어 가속 타입.
type HWAccel string

const (
	HWAuto  HWAccel = "auto"
	HWNVENC HWAccel = "nvenc"
	HWQSV   HWAccel = "qsv"
	HWCPU   HWAccel = "cpu"
)

// Config 스트림 설정.
type Config struct {
	Width            int
	Height           int
	FPS              int
	Bitrate          string
	KeyframeInterval int // 기본값 = fps (1초)
	HWAccel          HWAccel
	SubWidth         int
	SubHeight        int
	SubBitrate       string
}

// Stats 슬라이딩 윈도우 FPS 포함 통계.
type Stats struct {
	FramesSent    int64
	BytesSent     int64
	StartTime     time.Time
	DroppedFrames int64
	LastFrameTime time.Time
	frameTimes    []time.Time
}

// NewStats 새 통계 생성.
func NewStats() *Stats {
	return &Stats{StartTime: time.Now()}
}

// ElapsedTime 경과 시간.
func (s *Stats) ElapsedTime() float64 {
	return time.Since(s.StartTime).Seconds()
}

// ActualFPS 최근 5초 윈도우의 FPS.
func (s *Stats) ActualFPS() float64 {
	if len(s.frameTimes) < 2 {
		return 0
	}
	now := time.Now()
	cutoff := now.Add(-5 * time.Second)
	recent := 0
	var oldest time.Time
	for _, ts := range s.frameTimes {
		if ts.After(cutoff) {
			if recent == 0 {
				oldest = ts
			}
			recent++
		}
	}
	if recent < 2 {
		return 0
	}
	span := now.Sub(oldest).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(recent) / span
}

// BitrateMbps 평균 비트레이트 (Mbps).
func (s *Stats) BitrateMbps() float64 {
	elapsed := s.ElapsedTime()
	if elapsed > 0 {
		return float64(s.BytesSent) * 8 / (elapsed * 1_000_000)
	}
	return 0
}

// RecordFrame 프레임 타임스탬프 기록.
func (s *Stats) RecordFrame(ts time.Time) {
	s.frameTimes = append(s.frameTimes, ts)
	if len(s.frameTimes) > 150 {
		s.frameTimes = s.frameTimes[len(s.frameTimes)-150:]
	}
}

// Streamer go2rtc RTMP push 스트리머.
type Streamer struct {
	Config *Config
	Stats  *Stats

	mu           sync.Mutex
	ffmpeg       *exec.Cmd
	stdin        *os.File
	stderrBuf    []byte
	activeHW     HWAccel
	rtmpURL      string
	rtmpURLSub   string
	isRunning    bool
	writerRunning bool
	shutdown     chan struct{}
	frameQueue   *frame.Queue
	writerDone   chan struct{}
	reconnectCnt int
}

// New 스트리머 생성.
func New(cfg *Config) *Streamer {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.KeyframeInterval <= 0 {
		cfg.KeyframeInterval = cfg.FPS
	}
	if cfg.FPS <= 0 {
		cfg.FPS = 30
	}
	if cfg.Width <= 0 {
		cfg.Width = 1920
	}
	if cfg.Height <= 0 {
		cfg.Height = 1080
	}
	if cfg.Bitrate == "" {
		cfg.Bitrate = "4M"
	}
	if cfg.HWAccel == "" {
		cfg.HWAccel = HWAuto
	}
	if cfg.SubBitrate == "" {
		cfg.SubBitrate = "1M"
	}
	if cfg.SubWidth <= 0 {
		cfg.SubWidth = 640
	}
	if cfg.SubHeight <= 0 {
		cfg.SubHeight = 360
	}
	return &Streamer{
		Config:   cfg,
		Stats:    NewStats(),
		shutdown: make(chan struct{}),
	}
}

// IsRunning 실행 중이면서 영구 실패하지 않은 상태.
func (s *Streamer) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isRunning
}

// FrameSize (width, height).
func (s *Streamer) FrameSize() (int, int) {
	return s.Config.Width, s.Config.Height
}

// ExpectedFrameBytes BGR24 프레임당 바이트 수.
func (s *Streamer) ExpectedFrameBytes() int {
	return s.Config.Width * s.Config.Height * 3
}

// Start 스트리밍을 시작한다.
func (s *Streamer) Start(rtmpURL, rtmpURLSub string) bool {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		return false
	}
	s.rtmpURL = rtmpURL
	s.rtmpURLSub = rtmpURLSub
	s.Stats = NewStats()
	s.reconnectCnt = 0
	s.shutdown = make(chan struct{})
	s.mu.Unlock()

	// HW 사다리: AUTO -> NVENC -> QSV -> CPU. 특정 HW 요청이면 CPU last-resort.
	hwOrder := []HWAccel{}
	switch s.Config.HWAccel {
	case HWAuto:
		hwOrder = []HWAccel{HWNVENC, HWQSV, HWCPU}
	case HWCPU:
		hwOrder = []HWAccel{HWCPU}
	default:
		hwOrder = []HWAccel{s.Config.HWAccel, HWCPU}
	}

	for _, hw := range hwOrder {
		if hw == HWCPU && s.Config.HWAccel != HWAuto && s.Config.HWAccel != HWCPU {
			fmt.Printf("[WARN] %s hardware encoder unavailable - falling back to CPU (libx264)\n",
				strings.ToUpper(string(s.Config.HWAccel)))
		}
		if !checkHWEncoderAvailable(hw) {
			fmt.Printf("[--] %s not available\n", strings.ToUpper(string(hw)))
			continue
		}
		fmt.Printf("[OK] %s available\n", strings.ToUpper(string(hw)))

		s.mu.Lock()
		s.stderrBuf = nil
		s.mu.Unlock()

		if s.startFFmpeg(rtmpURL, rtmpURLSub, hw) && s.checkFFmpegRunning() {
			if s.warmUpEncoder() {
				s.mu.Lock()
				s.activeHW = hw
				s.isRunning = true
				s.mu.Unlock()
				s.startWriter()
				fmt.Printf("[OK] Streamer started with %s acceleration\n", strings.ToUpper(string(hw)))
				return true
			}
			fmt.Printf("[FAIL] %s encoder initialization failed\n", strings.ToUpper(string(hw)))
		} else {
			fmt.Printf("[FAIL] %s failed to start\n", strings.ToUpper(string(hw)))
		}
		s.cleanupFFmpeg()
	}

	fmt.Println("[FAIL] Failed to start streamer with any hardware acceleration")
	return false
}

// Stop 스트리밍을 정지하고 정리한다.
func (s *Streamer) Stop() {
	s.mu.Lock()
	s.isRunning = false
	s.mu.Unlock()
	select {
	case <-s.shutdown:
	default:
		close(s.shutdown)
	}
	s.stopWriter()
	s.cleanupFFmpeg()
}

// Stream 프레임을 제출한다. 비블로킹, drop-oldest.
// writeLoop에서 리사이즈/변환 후 ffmpeg stdin에 쓴다. 프레임은 처리 후 Release.
func (s *Streamer) Stream(f *frame.Frame) bool {
	s.mu.Lock()
	if !s.isRunning || s.stdin == nil {
		s.mu.Unlock()
		f.Release()
		return false
	}
	s.mu.Unlock()
	if !s.frameQueue.Put(f) {
		s.mu.Lock()
		s.Stats.DroppedFrames++
		s.mu.Unlock()
	}
	return true
}

// startWriter 프레임 큐를 ffmpeg stdin으로 전달하는 워커를 시작한다.
func (s *Streamer) startWriter() {
	s.mu.Lock()
	s.frameQueue = frame.NewQueue(2)
	s.frameQueue.OnDrop = func(item any) {
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	s.writerRunning = true
	s.writerDone = make(chan struct{})
	s.mu.Unlock()
	go s.writeLoop()
}

func (s *Streamer) stopWriter() {
	s.mu.Lock()
	if !s.writerRunning {
		s.mu.Unlock()
		return
	}
	s.writerRunning = false
	s.mu.Unlock()
	if s.frameQueue != nil {
		s.frameQueue.Close()
	}
	select {
	case <-s.writerDone:
	case <-time.After(3 * time.Second):
	}
	// 남은 프레임 해제 (드레인).
	if s.frameQueue != nil {
		for {
			item := s.frameQueue.Get(0)
			if item == nil {
				break
			}
			if f, ok := item.(*frame.Frame); ok {
				f.Release()
			}
		}
	}
}

// writeLoop 큐를 소비해 ffmpeg stdin에 쓴다.
func (s *Streamer) writeLoop() {
	defer close(s.writerDone)
	for {
		s.mu.Lock()
		running := s.writerRunning
		s.mu.Unlock()
		if !running {
			return
		}
		item := s.frameQueue.Get(500 * time.Millisecond)
		if item == nil {
			continue
		}
		f := item.(*frame.Frame)

		// 독립 복사본: OpenCV Mat은 스레드 안전하지 않으므로 원본을 공유하지 않는다.
		mat := f.Mat.Clone()
		f.Release()

		// 리사이즈 + BGR24 변환 (IPyCam _write_loop와 동일).
		writeMat := &mat
		resized := gocv.NewMat()
		if mat.Cols() != s.Config.Width || mat.Rows() != s.Config.Height {
			gocv.Resize(mat, &resized, image.Pt(s.Config.Width, s.Config.Height), 0, 0, gocv.InterpolationDefault)
			writeMat = &resized
		}
		conv := gocv.NewMat()
		if writeMat.Channels() != 3 {
			gocv.CvtColor(*writeMat, &conv, gocv.ColorBGRToGray)
			if conv.Channels() != 3 {
				gocv.CvtColor(*writeMat, &conv, gocv.ColorGrayToBGR)
			}
			writeMat = &conv
		}

		data := writeMat.ToBytes()
		resized.Close()
		conv.Close()
		mat.Close()

		s.mu.Lock()
		stdin := s.stdin
		s.mu.Unlock()
		if stdin == nil {
			continue
		}
		_, err := stdin.Write(data)
		if err != nil {
			fmt.Println("[WARN] FFmpeg pipe broken")
			s.dumpFFmpegError()
			if s.reconnect() {
				continue
			}
			s.mu.Lock()
			s.isRunning = false
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		s.Stats.FramesSent++
		s.Stats.BytesSent += int64(len(data))
		s.Stats.LastFrameTime = time.Now()
		s.Stats.RecordFrame(time.Now())
		s.mu.Unlock()
	}
}

// reconnect 제한된 백오프로 ffmpeg을 재시작한다.
func (s *Streamer) reconnect() bool {
	s.cleanupFFmpeg()

	backoff := 1.0
	for attempt := 1; attempt <= 4; attempt++ {
		s.mu.Lock()
		if !s.writerRunning {
			s.mu.Unlock()
			return false
		}
		shutdown := s.shutdown
		activeHW := s.activeHW
		s.mu.Unlock()

		fmt.Printf("[WARN] Reconnecting to FFmpeg (attempt %d/4) in %.1fs...\n", attempt, backoff)
		select {
		case <-shutdown:
			return false
		case <-time.After(time.Duration(backoff * float64(time.Second))):
		}
		s.mu.Lock()
		if !s.writerRunning {
			s.mu.Unlock()
			return false
		}
		s.mu.Unlock()

		if activeHW == "" {
			activeHW = HWCPU
		}
		s.mu.Lock()
		s.stderrBuf = nil
		s.mu.Unlock()
		if s.startFFmpeg(s.rtmpURL, s.rtmpURLSub, activeHW) && s.checkFFmpegRunning() && s.warmUpEncoder() {
			s.mu.Lock()
			s.reconnectCnt++
			s.mu.Unlock()
			fmt.Printf("[OK] FFmpeg reconnected (attempt %d)\n", attempt)
			return true
		}
		fmt.Printf("[FAIL] Reconnect attempt %d did not come up\n", attempt)
		s.cleanupFFmpeg()
		backoff = min(backoff*2, 8.0)
	}
	fmt.Println("[FAIL] FFmpeg reconnect exhausted; giving up")
	return false
}

// checkHWEncoderAvailable 인코더가 ffmpeg에 컴파일되어 있는지 확인.
func checkHWEncoderAvailable(hw HWAccel) bool {
	if hw == HWCPU {
		return true
	}
	encoder := map[HWAccel]string{HWNVENC: "h264_nvenc", HWQSV: "h264_qsv"}[hw]
	if encoder == "" {
		return true
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-encoders")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), encoder)
}

// startFFmpeg ffmpeg 프로세스를 시작한다.
func (s *Streamer) startFFmpeg(rtmpURL, rtmpURLSub string, hw HWAccel) bool {
	hwConfigs := map[HWAccel]struct {
		codec       string
		extraEncode []string
	}{
		HWNVENC: {"h264_nvenc", []string{"-gpu", "0", "-preset", "p1", "-rc", "cbr", "-bf", "0"}},
		HWQSV:   {"h264_qsv", []string{"-preset", "faster", "-global_quality", "20", "-look_ahead", "0", "-bf", "0"}},
		HWCPU:   {"libx264", []string{"-preset", "faster", "-crf", "20", "-tune", "zerolatency", "-bf", "0"}},
	}
	cfg := hwConfigs[hw]

	cmdArgs := []string{
		"-y",
		"-f", "rawvideo",
		"-vcodec", "rawvideo",
		"-s", fmt.Sprintf("%dx%d", s.Config.Width, s.Config.Height),
		"-pix_fmt", "bgr24",
		"-r", fmt.Sprint(s.Config.FPS),
		"-i", "-",
		"-c:v", cfg.codec,
		"-pix_fmt", "yuv420p",
	}
	cmdArgs = append(cmdArgs, cfg.extraEncode...)
	cmdArgs = append(cmdArgs,
		"-g", fmt.Sprint(s.Config.KeyframeInterval),
		"-b:v", s.Config.Bitrate,
		"-maxrate", s.Config.Bitrate,
		"-bufsize", "1M",
		"-avoid_negative_ts", "make_zero",
		// 고정 프레임레이트: rawvideo 입력의 PTS가 손실되어 go2rtc가 영상을
		// 실시간보다 빠르게 재생하는 문제를 방지한다.
		"-fps_mode", "cfr",
		"-r", fmt.Sprint(s.Config.FPS),
	)

	if strings.HasPrefix(rtmpURL, "rtsp://") {
		cmdArgs = append(cmdArgs, "-f", "rtsp", "-rtsp_transport", "tcp", rtmpURL)
	} else {
		cmdArgs = append(cmdArgs, "-flags", "+global_header", "-f", "flv", rtmpURL)
	}

	// 서브스트림 출력
	if rtmpURLSub != "" {
		cmdArgs = append(cmdArgs, "-c:v", cfg.codec, "-pix_fmt", "yuv420p")
		cmdArgs = append(cmdArgs, cfg.extraEncode...)
		cmdArgs = append(cmdArgs,
			"-s", fmt.Sprintf("%dx%d", s.Config.SubWidth, s.Config.SubHeight),
			"-b:v", s.Config.SubBitrate,
			"-g", fmt.Sprint(s.Config.KeyframeInterval),
		)
		if strings.HasPrefix(rtmpURLSub, "rtsp://") {
			cmdArgs = append(cmdArgs, "-f", "rtsp", "-rtsp_transport", "tcp", rtmpURLSub)
		} else {
			cmdArgs = append(cmdArgs, "-flags", "+global_header", "-f", "flv", rtmpURLSub)
		}
	}

	cmd := exec.Command("ffmpeg", cmdArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return false
	}
	cmd.Stdout = io.Discard // DEVNULL (파이프 채워지는 교착 방지)

	// stderr는 파이프로 읽어 에러 버퍼에 수집 (에러 감지용).
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return false
	}

	if err := cmd.Start(); err != nil {
		return false
	}
	// stdin pipe를 *os.File로 변환 (비동기 쓰기용).
	f, ok := stdin.(*os.File)
	if !ok {
		cmd.Process.Kill()
		return false
	}
	s.mu.Lock()
	s.ffmpeg = cmd
	s.stdin = f
	s.mu.Unlock()

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderr.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.stderrBuf = append(s.stderrBuf, buf[:n]...)
				if len(s.stderrBuf) > 8192 {
					s.stderrBuf = s.stderrBuf[len(s.stderrBuf)-8192:]
				}
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return true
}

// checkFFmpegRunning ffmpeg이 정상 시작됐는지 확인.
func (s *Streamer) checkFFmpegRunning() bool {
	s.mu.Lock()
	proc := s.ffmpeg
	s.mu.Unlock()
	if proc == nil {
		return false
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if proc.ProcessState != nil {
			s.dumpFFmpegError()
			return false
		}
		s.mu.Lock()
		errText := strings.ToLower(string(s.stderrBuf))
		s.mu.Unlock()
		for _, pat := range []string{
			"could not open encoder", "error while opening encoder", "cannot load",
			"invalid encoder", "unknown encoder", "encoder not found", "unsupported codec",
			"init failed", "conversion failed",
		} {
			if strings.Contains(errText, pat) {
				return false
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return proc.ProcessState == nil
}

// warmUpEncoder 인코더가 실제 동작하는지 테스트 프레임으로 확인.
func (s *Streamer) warmUpEncoder() bool {
	black := make([]byte, s.ExpectedFrameBytes())
	s.mu.Lock()
	stdin := s.stdin
	s.mu.Unlock()
	if stdin == nil {
		return false
	}
	deadline := time.Now().Add(5 * time.Second)
	sent := 0
	for sent < 3 && time.Now().Before(deadline) {
		_, err := stdin.Write(black)
		if err != nil {
			return false
		}
		sent++
		time.Sleep(50 * time.Millisecond)
		s.mu.Lock()
		proc := s.ffmpeg
		errText := strings.ToLower(string(s.stderrBuf))
		s.mu.Unlock()
		if proc != nil && proc.ProcessState != nil {
			return false
		}
		for _, pat := range []string{
			"driver does not support", "could not open encoder", "error while opening encoder",
			"error sending frames", "conversion failed", "cannot load", "no nvenc capable",
		} {
			if strings.Contains(errText, pat) {
				return false
			}
		}
	}
	return sent >= 3
}

// dumpFFmpegError ffmpeg stderr 로그 출력.
func (s *Streamer) dumpFFmpegError() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.stderrBuf) > 0 {
		fmt.Printf("FFmpeg error output:\n%s\n", string(s.stderrBuf))
	}
}

// cleanupFFmpeg ffmpeg 프로세스 정리 (kill + wait).
func (s *Streamer) cleanupFFmpeg() {
	s.mu.Lock()
	proc := s.ffmpeg
	stdin := s.stdin
	s.ffmpeg = nil
	s.stdin = nil
	s.mu.Unlock()
	if stdin != nil {
		stdin.Close()
	}
	if proc != nil {
		if proc.ProcessState == nil {
			proc.Process.Kill()
		}
		proc.Wait()
	}
}

// ReconnectCount 재연결 횟수.
func (s *Streamer) ReconnectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconnectCnt
}

// procAlive 프로세스가 살아 있는지 확인 (Signal(0) probe).
func procAlive(proc *exec.Cmd) bool {
	if proc == nil || proc.Process == nil {
		return false
	}
	err := proc.Process.Signal(syscall.Signal(0))
	return err == nil
}

// fmtProgress 로그 출력 헬퍼 (streamer는 별도 logger 없이 stderr로 출력).
func fmtProgress(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
