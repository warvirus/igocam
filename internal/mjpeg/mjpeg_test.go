// MJPEG 스트리머 테스트
package mjpeg

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/frame"
)

func testFrame(w, h int) *frame.Frame {
	img := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)
	return frame.NewFrame(&img)
}

func TestNewAndStart(t *testing.T) {
	s := New(80, 640, 360)
	if s.Running() {
		t.Fatal("should not be running before start")
	}
	if !s.Start() {
		t.Fatal("start failed")
	}
	if !s.Running() {
		t.Fatal("should be running")
	}
	s.Stop()
	if s.Running() {
		t.Fatal("should stop running")
	}
}

func TestAddRemoveClient(t *testing.T) {
	s := New(80, 640, 360)
	s.Start()
	defer s.Stop()

	c := s.AddClient(func([]byte) error { return nil }, "main")
	if s.ClientCount() != 1 {
		t.Fatalf("client count = %d, want 1", s.ClientCount())
	}
	s.RemoveClient(c)
	if s.ClientCount() != 0 {
		t.Fatalf("client count = %d, want 0", s.ClientCount())
	}
}

func TestStreamFrame(t *testing.T) {
	s := New(80, 640, 360)
	if s.StreamFrame(testFrame(100, 100)) {
		t.Fatal("stream_frame before start should return false")
	}
	s.Start()
	defer s.Stop()
	// 클라이언트가 없어도 프레임은 카운트된다 (반환은 false).
	if s.StreamFrame(testFrame(100, 100)) {
		t.Fatal("stream_frame without clients should return false")
	}
	if s.FramesSent() != 1 {
		t.Fatalf("frames_sent = %d, want 1", s.FramesSent())
	}
}

func TestServeClientReceivesFrames(t *testing.T) {
	s := New(80, 640, 360)
	s.Start()
	defer s.Stop()

	var mu sync.Mutex
	var received [][]byte
	c := s.AddClient(func(data []byte) error {
		mu.Lock()
		received = append(received, append([]byte{}, data...))
		mu.Unlock()
		return nil
	}, "main")

	done := make(chan struct{})
	go func() {
		s.ServeClient(c)
		close(done)
	}()

	// 몇 프레임 푸시.
	for i := 0; i < 5; i++ {
		s.StreamFrame(testFrame(320, 240))
	}

	// 서버 클라이언트가 프레임을 받을 때까지 대기.
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client received %d frames, want >= 1", n)
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !bytes.HasPrefix(received[0], []byte("--frame\r\nContent-Type: image/jpeg\r\n")) {
		t.Fatalf("bad multipart header: %q", received[0][:50])
	}
}

func TestSubStream(t *testing.T) {
	s := New(80, 320, 180)
	s.Start()
	defer s.Stop()

	var mu sync.Mutex
	var received [][]byte
	c := s.AddClient(func(data []byte) error {
		mu.Lock()
		received = append(received, append([]byte{}, data...))
		mu.Unlock()
		return nil
	}, "sub")

	done := make(chan struct{})
	go func() {
		s.ServeClient(c)
		close(done)
	}()

	s.StreamFrame(testFrame(640, 480))

	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sub client received nothing")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestHeaders(t *testing.T) {
	s := New(80, 640, 360)
	h := s.Headers()
	if ct := h.Get("Content-Type"); ct != "multipart/x-mixed-replace; boundary=frame" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if cc := h.Get("Cache-Control"); cc != "no-cache, no-store, must-revalidate" {
		t.Fatalf("Cache-Control = %q", cc)
	}
}

func TestCheckTCPPort(t *testing.T) {
	// 닫힌 포트는 false.
	if CheckTCPPort("127.0.0.1", 1, 300*time.Millisecond) {
		t.Fatal("port 1 should be closed")
	}
}
