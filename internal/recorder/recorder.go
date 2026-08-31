// 로컬 디스크 분할 녹화 - 프리레코드 링버퍼 + 파일 크기 로테이션
package recorder

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/config"
	"igocam/internal/frame"
)

// RecordingFormatMap recording_format -> (확장자, fourcc).
var RecordingFormatMap = map[string][2]string{
	"mp4": {".mp4", "mp4v"},
	"avi": {".avi", "MJPG"},
}

const (
	defaultFormat          = "mp4"
	sizePollFrames         = 30
	defaultQueueSize       = 8
	ringMemoryWarnBytes    = 512 * 1024 * 1024
)

// Recorder 레코더 구현. 항상 생성되지만 작업자는 실행 중 && (녹화 중 || 프리레코드
// 유지 중)일 때만 동작한다.
type Recorder struct {
	config *config.CameraConfig

	mu        sync.Mutex
	queue     *frame.Queue
	queueSize int
	sizePoll  int

	worker       chan struct{}
	workerDone   chan struct{}
	workerRunning bool

	preSeconds int
	ring       []*frame.Frame // 프리레코드 링버퍼 (순환 인덱스)
	ringHead   int

	recording     bool
	writer        *gocv.VideoWriter
	currentFile   string
	outDir        string
	ext           string
	fourccStr     string
	safeName      string
	fps           int
	frameW        int
	frameH        int
	baseTimestamp time.Time
	segmentIndex  int
	segments      []string
	framesWritten int
	framesInSeg   int
	bytesWritten  int64
	maxBytes      int64
}

// New 레코더 생성.
func New(cfg *config.CameraConfig) *Recorder {
	r := &Recorder{
		config:    cfg,
		queueSize: defaultQueueSize,
		sizePoll:  sizePollFrames,
		segments:  []string{},
	}
	r.queue = frame.NewQueue(r.queueSize)
	r.queue.OnDrop = func(item any) {
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	r.rebuildRingLocked()
	return r
}

// Start 레코더 워커를 시작한다. 이미 실행 중이면 no-op.
func (r *Recorder) Start() {
	r.mu.Lock()
	if r.workerRunning {
		r.mu.Unlock()
		return
	}
	r.rebuildRingLocked()
	r.queue = frame.NewQueue(r.queueSize)
	r.queue.OnDrop = func(item any) {
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	r.workerRunning = true
	r.workerDone = make(chan struct{})
	r.mu.Unlock()

	r.worker = make(chan struct{})
	go r.recordLoop()
}

// Stop 활성 녹화를 종료하고 워커를 join한다.
func (r *Recorder) Stop() {
	r.StopRecording()
	r.mu.Lock()
	if !r.workerRunning {
		r.mu.Unlock()
		return
	}
	r.workerRunning = false
	r.mu.Unlock()

	r.queue.Close()
	select {
	case <-r.workerDone:
	case <-time.After(3 * time.Second):
	}
	// 남은 프레임 해제 (드레인).
	for {
		item := r.queue.Get(0)
		if item == nil {
			break
		}
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	r.mu.Lock()
	r.workerDone = nil
	r.mu.Unlock()
}

// WorkerRunning 워커 실행 여부.
func (r *Recorder) WorkerRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.workerRunning
}

// Reconfigure 설정을 다시 읽는다. 녹화 중이 아닐 때만 링버퍼 재구성.
func (r *Recorder) Reconfigure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		r.rebuildRingLocked()
	}
}

// WantsFrames 캡처 스레드가 프레임을 enqueue해야 하는지 여부 (lock-free).
func (r *Recorder) WantsFrames() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.workerRunning && (r.recording || r.preSeconds > 0)
}

// Submit 아웃바운드 프레임을 enqueue한다. 비블로킹, drop-oldest.
// 프레임 참조는 소비자가 Release할 때까지 유지된다.
func (r *Recorder) Submit(f *frame.Frame) {
	if !r.WantsFrames() {
		f.Release()
		return
	}
	r.queue.Put(f)
}

// StartRecording 녹화를 시작한다. 성공 시 true.
func (r *Recorder) StartRecording() bool {
	if !r.WorkerRunning() {
		r.Start()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.recording {
		return true
	}

	outDir := r.resolveOutputDir()
	if outDir == "" {
		return false
	}

	fmt_ := r.config.RecordingFormat
	if fmt_ == "" {
		fmt_ = defaultFormat
	}
	pair, ok := RecordingFormatMap[fmt_]
	if !ok {
		pair = RecordingFormatMap[defaultFormat]
	}
	r.ext = pair[0]
	r.fourccStr = pair[1]
	r.safeName = sanitizeName(r.config.Name)
	r.outDir = outDir
	r.fps = r.config.MainFPS
	if r.fps < 1 {
		r.fps = 30
	}
	r.frameW, r.frameH = r.inferFrameSizeLocked()

	maxMB := r.config.RecordingMaxFileMB
	if maxMB > 0 {
		r.maxBytes = int64(maxMB) * 1024 * 1024
	} else {
		r.maxBytes = 0
	}

	r.baseTimestamp = time.Now()
	r.segmentIndex = 0
	r.segments = []string{}
	r.framesWritten = 0
	r.framesInSeg = 0
	r.bytesWritten = 0

	if !r.openWriterLocked() {
		r.writer = nil
		r.currentFile = ""
		return false
	}

	// 프리레코드 링버퍼를 첫 세그먼트로 flush (flush 중엔 rotate 안 함).
	if r.preSeconds > 0 && r.ringHead > 0 {
		for i := 0; i < r.ringHead; i++ {
			r.writeFrameLocked(r.ring[i], false)
			r.ring[i].Release()
			r.ring[i] = nil
		}
		r.ringHead = 0
	}

	r.recording = true
	return true
}

// StopRecording 현재 녹화를 종료하고 세그먼트 경로를 반환한다.
func (r *Recorder) StopRecording() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return nil
	}
	r.recording = false
	r.closeWriterLocked()
	return append([]string{}, r.segments...)
}

// IsRecording 녹화 중 여부.
func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recording
}

// Dropped 큐가 가득 차서 버린 프레임 수.
func (r *Recorder) Dropped() int64 {
	return r.queue.Dropped()
}

// Stats 상태 스냅샷.
func (r *Recorder) Stats() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return map[string]any{
		"recording":          r.recording,
		"worker_running":     r.workerRunning,
		"file":               r.currentFile,
		"bytes":              r.bytesWritten,
		"segments":           len(r.segments),
		"segment_files":      append([]string{}, r.segments...),
		"frames_written":     r.framesWritten,
		"dropped":            r.queue.Dropped(),
		"pre_record_seconds": r.preSeconds,
		"format":             r.fourccStr,
	}
}

// recordLoop 워커: 큐를 소비해 라이브 프레임을 쓰거나 링버퍼를 채운다.
func (r *Recorder) recordLoop() {
	defer close(r.workerDone)
	for {
		r.mu.Lock()
		running := r.workerRunning
		r.mu.Unlock()
		if !running {
			return
		}
		item := r.queue.Get(500 * time.Millisecond)
		if item == nil {
			continue
		}
		f := item.(*frame.Frame)
		r.mu.Lock()
		if r.recording {
			r.writeFrameLocked(f, true)
		} else if r.preSeconds > 0 {
			r.appendRingLocked(f)
		}
		r.mu.Unlock()
		f.Release()
	}
}

// writeFrameLocked 단일 프레임을 기록한다. 반드시 r.mu 보유 상태로 호출.
func (r *Recorder) writeFrameLocked(f *frame.Frame, allowRotate bool) {
	if r.writer == nil || f.Mat == nil {
		return
	}
	mat := f.Mat
	fw, fh := mat.Cols(), mat.Rows()
	if fw != r.frameW || fh != r.frameH {
		resized := gocv.NewMat()
		gocv.Resize(*mat, &resized, image.Pt(r.frameW, r.frameH), 0, 0, gocv.InterpolationDefault)
		// 이미지 크기가 다르면 resize 결과를 임시 Mat으로 쓴다.
		if resized.Cols() == r.frameW && resized.Rows() == r.frameH {
			r.writer.Write(resized)
			resized.Close()
		} else {
			resized.Close()
			return
		}
	} else {
		r.writer.Write(*mat)
	}
	r.framesWritten++
	r.framesInSeg++
	r.bytesWritten = fileSize(r.currentFile)

	if allowRotate && r.maxBytes > 0 && r.framesInSeg%r.sizePoll == 0 {
		r.maybeRotateLocked()
	}
}

func (r *Recorder) maybeRotateLocked() {
	if r.currentFile == "" {
		return
	}
	size := fileSize(r.currentFile)
	r.bytesWritten = size
	if size >= r.maxBytes {
		r.closeWriterLocked()
		r.segmentIndex++
		if !r.openWriterLocked() {
			r.recording = false
		}
	}
}

func (r *Recorder) openWriterLocked() bool {
	filename := fmt.Sprintf("%s_%s_%03d%s",
		r.safeName, r.baseTimestamp.Format("20060102_150405"), r.segmentIndex, r.ext)
	path := filepath.Join(r.outDir, filename)
	fourcc := r.fourccStr
	writer, err := gocv.VideoWriterFile(path, fourcc, float64(r.fps), r.frameW, r.frameH, true)
	if err != nil || !writer.IsOpened() {
		if writer != nil {
			writer.Close()
		}
		return false
	}
	r.writer = writer
	r.currentFile = path
	r.framesInSeg = 0
	r.segments = append(r.segments, path)
	return true
}

func (r *Recorder) closeWriterLocked() {
	if r.writer != nil {
		r.writer.Close()
		r.writer = nil
	}
	if r.currentFile != "" {
		r.bytesWritten = fileSize(r.currentFile)
	}
}

// inferFrameSizeLocked 실제 버퍼된 프레임 크기를 우선해 writer 크기를 추론.
func (r *Recorder) inferFrameSizeLocked() (int, int) {
	if r.ringHead > 0 {
		last := r.ring[r.ringHead-1]
		if last != nil && last.Mat != nil && !last.Mat.Empty() {
			return last.Mat.Cols(), last.Mat.Rows()
		}
	}
	w := r.config.MainWidth
	h := r.config.MainHeight
	if w < 1 {
		w = 1280
	}
	if h < 1 {
		h = 720
	}
	if rot := r.config.Rotation; rot == 90 || rot == 270 {
		w, h = h, w
	}
	return w, h
}

// rebuildRingLocked 설정에서 프리레코드 링버퍼를 (재)생성.
func (r *Recorder) rebuildRingLocked() {
	preSeconds := r.config.RecordingPreSec
	fps := r.config.MainFPS
	if fps < 1 {
		fps = 30
	}
	r.preSeconds = preSeconds
	if preSeconds < 0 {
		r.preSeconds = 0
	}
	maxlen := r.preSeconds * fps
	r.ring = make([]*frame.Frame, maxlen)
	r.ringHead = 0
	if maxlen > 0 {
		w := r.config.MainWidth
		h := r.config.MainHeight
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		est := int64(maxlen) * int64(w) * int64(h) * 3
		if est >= ringMemoryWarnBytes {
			// 메모리 경고 로그 (기본 logger 없음; 크기 정보만 남김)
			_ = est
		}
	}
}

func (r *Recorder) appendRingLocked(f *frame.Frame) {
	maxlen := len(r.ring)
	if maxlen == 0 {
		return
	}
	f.Retain() // 링에 저장하므로 참조 추가
	if r.ringHead == maxlen {
		// 가장 오래된 항목 해제
		r.ring[0].Release()
		copy(r.ring, r.ring[1:])
		r.ring[r.ringHead-1] = f
	} else {
		r.ring[r.ringHead] = f
		r.ringHead++
	}
}

func (r *Recorder) resolveOutputDir() string {
	raw := r.config.RecordingPath
	if raw == "" {
		raw = "recordings"
	}
	if containsNull(raw) {
		return ""
	}
	outDir := filepath.Join(".", raw)
	if abs, err := filepath.Abs(outDir); err == nil {
		outDir = abs
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return ""
	}
	info, err := os.Stat(outDir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return outDir
}

var unsafeNameRE = regexp.MustCompile(`[^\w\-]`)

func sanitizeName(name string) string {
	safe := unsafeNameRE.ReplaceAllString(name, "_")
	safe = trimUnderscores(safe)
	if safe == "" {
		return "recording"
	}
	return safe
}

func trimUnderscores(s string) string {
	for len(s) > 0 && s[0] == '_' {
		s = s[1:]
	}
	for len(s) > 0 && s[len(s)-1] == '_' {
		s = s[:len(s)-1]
	}
	return s
}

func containsNull(s string) bool {
	for _, c := range s {
		if c == 0 {
			return true
		}
	}
	return false
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
