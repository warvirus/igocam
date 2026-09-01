// 순수 Go MJPEG 스트리머 - go2rtc 없이 항상 사용 가능한 기저 폴백
package mjpeg

import (
	"bytes"
	"fmt"
	"image"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/frame"
)

const (
	// clientQueueSize 각 클라이언트가 버퍼링하는 인코딩 완료 프레임 수.
	// 작게 유지: 느린 클라이언트는 자기 프레임만 버리고 다른 클라이언트나
	// 인코더를 막을 수 없다.
	clientQueueSize = 3
	// defaultSubW/H 고정 sub 크기가 없을 때 사용하는 기본값.
	defaultSubW = 640
	defaultSubH = 360
	// encodeQueueSize 인코더 워커가 버퍼링하는 원본 프레임 수.
	encodeQueueSize = 2
)

// Client 연결된 MJPEG 클라이언트 하나.
type Client struct {
	// Write는 클라이언트 소켓에 인코딩된 multipart 청크를 쓴다 (HTTP 스레드).
	Write func([]byte) error

	mu         sync.Mutex
	connected  atomic.Bool
	framesSent atomic.Int64
	queue      *frame.Queue
	stream     string // 'main' 또는 'sub'
	parent     *Streamer
}

// Streamer MJPEG 스트리머. 인코더 워커가 프레임을 1회 인코딩해 각 클라이언트
// 큐로 팬아웃한다.
type Streamer struct {
	quality     int
	subW        int
	subH        int
	useFixedSub bool

	mu      sync.Mutex
	clients map[*Client]bool

	frameQueue *frame.Queue
	workerStop chan struct{}
	workerDone chan struct{}
	running    atomic.Bool

	framesSent atomic.Int64
	startTime  time.Time
	frameTimes []time.Time
	fpsMu      sync.Mutex
}

// New 스트리머 생성.
func New(quality, subW, subH int) *Streamer {
	s := &Streamer{
		quality: quality,
		subW:    subW,
		subH:    subH,
		clients: make(map[*Client]bool),
	}
	if subW > 0 && subH > 0 {
		s.useFixedSub = true
	}
	s.frameQueue = frame.NewQueue(encodeQueueSize)
	s.frameQueue.OnDrop = func(item any) {
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	return s
}

// Start 스트리머와 인코더 워커를 시작한다.
func (s *Streamer) Start() bool {
	if s.running.Swap(true) {
		return true
	}
	s.startTime = time.Now()
	s.frameQueue = frame.NewQueue(encodeQueueSize)
	s.frameQueue.OnDrop = func(item any) {
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	s.workerStop = make(chan struct{})
	s.workerDone = make(chan struct{})
	go s.encodeLoop()
	return true
}

// Stop 스트리머, 워커, 모든 클라이언트를 정지한다.
func (s *Streamer) Stop() {
	if !s.running.Swap(false) {
		return
	}
	close(s.workerStop)
	s.frameQueue.Close()
	select {
	case <-s.workerDone:
	case <-time.After(2 * time.Second):
	}
	// 남은 프레임 해제 (드레인).
	for {
		item := s.frameQueue.Get(0)
		if item == nil {
			break
		}
		if f, ok := item.(*frame.Frame); ok {
			f.Release()
		}
	}
	s.mu.Lock()
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = map[*Client]bool{}
	s.mu.Unlock()
	for _, c := range clients {
		c.connected.Store(false)
		c.queue.Close()
	}
}

// Running 실행 여부.
func (s *Streamer) Running() bool { return s.running.Load() }

// ClientCount 연결된 클라이언트 수.
func (s *Streamer) ClientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// FramesSent 스트리밍에 제출된 총 프레임 수.
func (s *Streamer) FramesSent() int64 { return s.framesSent.Load() }

// FramesDropped 인코더가 뒤처져 인코딩 전에 버린 프레임 수.
func (s *Streamer) FramesDropped() int64 { return s.frameQueue.Dropped() }

// ElapsedTime 시작 후 경과 시간.
func (s *Streamer) ElapsedTime() float64 {
	if s.startTime.IsZero() {
		return 0
	}
	return time.Since(s.startTime).Seconds()
}

// ActualFPS 최근 5초 슬라이딩 윈도우의 FPS.
func (s *Streamer) ActualFPS() float64 {
	s.fpsMu.Lock()
	defer s.fpsMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-5 * time.Second)
	var recent []time.Time
	for _, ts := range s.frameTimes {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}
	if len(recent) < 2 {
		return 0
	}
	span := now.Sub(recent[0]).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(len(recent)) / span
}

// AddClient 새 클라이언트를 추가한다. write는 클라이언트 연결에 청크를 쓴다.
func (s *Streamer) AddClient(write func([]byte) error, stream string) *Client {
	if stream != "main" && stream != "sub" {
		stream = "main"
	}
	c := &Client{
		Write:  write,
		queue:  frame.NewQueue(clientQueueSize),
		stream: stream,
		parent: s,
	}
	c.connected.Store(true)
	s.mu.Lock()
	s.clients[c] = true
	s.mu.Unlock()
	return c
}

// RemoveClient 클라이언트를 제거한다.
func (s *Streamer) RemoveClient(c *Client) {
	c.connected.Store(false)
	c.queue.Close()
	s.mu.Lock()
	delete(s.clients, c)
	s.mu.Unlock()
}

// StreamFrame 프레임을 제출한다. 비블로킹, drop-oldest.
// 인코딩이나 소켓을 건드리지 않는다. 프레임 참조는 소비자가 Release할 때까지
// 유지된다 (생산자는 enqueue 후 Release 책임).
func (s *Streamer) StreamFrame(f *frame.Frame) bool {
	if !s.running.Load() {
		f.Release()
		return false
	}
	s.framesSent.Add(1)
	s.fpsMu.Lock()
	s.frameTimes = append(s.frameTimes, time.Now())
	if len(s.frameTimes) > 150 {
		s.frameTimes = s.frameTimes[len(s.frameTimes)-150:]
	}
	s.fpsMu.Unlock()
	s.frameQueue.Put(f)
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients) > 0
}

// resolveSubSize 'sub' 스트림 인코딩 크기 결정.
func (s *Streamer) resolveSubSize(w, h int) (int, int) {
	if s.useFixedSub {
		return s.subW, s.subH
	}
	halfW, halfH := w/2, h/2
	if halfW < 16 || halfH < 16 {
		return defaultSubW, defaultSubH
	}
	return halfW, halfH
}

// wrapMultipart JPEG 바이트를 multipart/x-mixed-replace 청크로 감싼다.
func (s *Streamer) wrapMultipart(jpeg []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("--frame\r\n")
	buf.WriteString("Content-Type: image/jpeg\r\n")
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(jpeg))
	buf.Write(jpeg)
	buf.WriteString("\r\n")
	return buf.Bytes()
}

// encodeLoop 워커: 큐에서 프레임을 1회 인코딩해 각 클라이언트 큐로 팬아웃.
func (s *Streamer) encodeLoop() {
	defer close(s.workerDone)
	for {
		select {
		case <-s.workerStop:
			return
		default:
		}
		item := s.frameQueue.Get(500 * time.Millisecond)
		if item == nil {
			if !s.running.Load() {
				return
			}
			continue
		}
		f := item.(*frame.Frame)
		mat := f.Mat

		// main (전체 해상도) JPEG을 1회 인코딩.
		buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, *mat, []int{gocv.IMWriteJpegQuality, s.quality})
		if err != nil || buf == nil || buf.Len() == 0 {
			f.Release()
			continue
		}
		jpeg := make([]byte, buf.Len())
		copy(jpeg, buf.GetBytes())
		buf.Close()
		mainData := s.wrapMultipart(jpeg)

		s.mu.Lock()
		clients := make([]*Client, 0, len(s.clients))
		hasSub := false
		for c := range s.clients {
			clients = append(clients, c)
			if c.connected.Load() && c.stream == "sub" {
				hasSub = true
			}
		}
		s.mu.Unlock()

		var subData []byte
		if hasSub {
			subW, subH := s.resolveSubSize(mat.Cols(), mat.Rows())
			subMat := gocv.NewMat()
			gocv.Resize(*mat, &subMat, image.Pt(subW, subH), 0, 0, gocv.InterpolationArea)
			subBuf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, subMat, []int{gocv.IMWriteJpegQuality, s.quality})
			subMat.Close()
			if err == nil && subBuf != nil && subBuf.Len() > 0 {
				subData = s.wrapMultipart(subBuf.GetBytes())
				subBuf.Close()
			}
		}

		// 프레임 사용 완료 — Release.
		f.Release()

		for _, c := range clients {
			if !c.connected.Load() {
				continue
			}
			if c.stream == "sub" && subData != nil {
				c.queue.Put(subData)
			} else {
				c.queue.Put(mainData)
			}
		}
	}
}

// ServeClient 한 클라이언트의 블로킹 writer 루프 (HTTP 스레드에서 실행).
func (s *Streamer) ServeClient(c *Client) {
	defer s.RemoveClient(c)
	for s.running.Load() && c.connected.Load() {
		data := c.queue.Get(500 * time.Millisecond)
		if data == nil {
			continue
		}
		chunk := data.([]byte)
		if err := c.Write(chunk); err != nil {
			c.connected.Store(false)
			return
		}
		c.framesSent.Add(1)
	}
}

// Headers MJPEG 스트림 응답 헤더.
func (s *Streamer) Headers() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	h.Set("Cache-Control", "no-cache, no-store, must-revalidate")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")
	h.Set("Connection", "close")
	return h
}

// CheckTCPPort 지정 호스트/포트가 TCP 연결을 받는지 확인.
func CheckTCPPort(host string, port int, timeout time.Duration) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// CheckGo2rtcRunning go2rtc API 포트가 살아 있는지 확인.
func CheckGo2rtcRunning(host string, port int, timeout time.Duration) bool {
	if !CheckTCPPort(host, port, timeout) {
		return false
	}
	url := fmt.Sprintf("http://%s:%d/api", host, port)
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// CheckRTSPPortAvailable RTSP 포트가 연결을 받는지 확인.
func CheckRTSPPortAvailable(host string, port int, timeout time.Duration) bool {
	return CheckTCPPort(host, port, timeout)
}
