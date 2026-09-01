// 스트리머 테스트
package streamer

import (
	"testing"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/frame"
)

func TestDefaults(t *testing.T) {
	s := New(nil)
	w, h := s.FrameSize()
	if w != 1920 || h != 1080 {
		t.Fatalf("size = %dx%d", w, h)
	}
	if s.ExpectedFrameBytes() != 1920*1080*3 {
		t.Fatalf("frame bytes = %d", s.ExpectedFrameBytes())
	}
	if s.IsRunning() {
		t.Fatal("should not be running initially")
	}
}

func TestCustomConfig(t *testing.T) {
	s := New(&Config{
		Width:  640,
		Height: 480,
		FPS:    15,
	})
	w, h := s.FrameSize()
	if w != 640 || h != 480 {
		t.Fatalf("size = %dx%d", w, h)
	}
	if s.ExpectedFrameBytes() != 640*480*3 {
		t.Fatalf("frame bytes = %d", s.ExpectedFrameBytes())
	}
}

func TestStats(t *testing.T) {
	stats := NewStats()
	if stats.ElapsedTime() < 0 {
		t.Fatal("negative elapsed time")
	}
	if stats.ActualFPS() != 0 {
		t.Fatal("FPS should be 0 with no frames")
	}
	stats.RecordFrame(time.Now())
	time.Sleep(10 * time.Millisecond)
	stats.RecordFrame(time.Now())
	if stats.ActualFPS() <= 0 {
		t.Fatal("FPS should be > 0 with 2 frames")
	}
	stats.FramesSent = 100
	stats.BytesSent = 1000
	if stats.BitrateMbps() <= 0 {
		t.Fatal("bitrate should be > 0")
	}
}

func TestStopWithoutStart(t *testing.T) {
	s := New(nil)
	s.Stop() // should not panic
}

func TestStreamOnStopped(t *testing.T) {
	s := New(nil)
	img := gocv.NewMatWithSize(1, 1, gocv.MatTypeCV8UC3)
	f := frame.NewFrame(&img)
	if s.Stream(f) {
		t.Fatal("stream should return false when not running")
	}
	f.Release() // owned by test, release here
}

func TestConfigOrder(t *testing.T) {
	// AUTO -> NVENC -> QSV -> CPU 순서 (Phase 0 스파이크에서 검증).
	// CPU는 항상 사용 가능.
	if !checkHWEncoderAvailable(HWCPU) {
		t.Fatal("CPU encoder should always be available")
	}
}
