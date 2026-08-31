// 레코더 테스트 - 실제 파일 기록 검증
package recorder

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/config"
	"igocam/internal/frame"
)

func testConfig(t *testing.T) *config.CameraConfig {
	cfg := config.DefaultConfig()
	cfg.Name = "Test Camera"
	cfg.MainWidth = 320
	cfg.MainHeight = 240
	cfg.MainFPS = 15
	cfg.RecordingFormat = "avi"
	cfg.RecordingPath = filepath.Join(t.TempDir(), "rec")
	cfg.RecordingMaxFileMB = 1024
	cfg.RecordingPreSec = 0
	return cfg
}

func testFrame(w, h int) *frame.Frame {
	img := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)
	return frame.NewFrame(&img)
}

func TestRecorderLifecycle(t *testing.T) {
	cfg := testConfig(t)
	r := New(cfg)
	if r.WorkerRunning() {
		t.Fatal("worker should not be running initially")
	}
	if r.WantsFrames() {
		t.Fatal("should not want frames initially")
	}
	r.Start()
	if !r.WorkerRunning() {
		t.Fatal("worker should be running after start")
	}
	// pre_seconds=0이므로 아직 프레임 불필요.
	if r.WantsFrames() {
		t.Fatal("should not want frames with pre_seconds=0 and not recording")
	}
	r.Stop()
	if r.WorkerRunning() {
		t.Fatal("worker should stop")
	}
}

func TestStartStopRecording(t *testing.T) {
	cfg := testConfig(t)
	r := New(cfg)
	if !r.StartRecording() {
		t.Fatal("start recording failed")
	}
	if !r.IsRecording() {
		t.Fatal("should be recording")
	}
	// 몇 프레임 기록.
	for i := 0; i < 10; i++ {
		r.Submit(testFrame(320, 240))
	}
	time.Sleep(200 * time.Millisecond)
	segments := r.StopRecording()
	if len(segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(segments))
	}
	if _, err := os.Stat(segments[0]); err != nil {
		t.Fatalf("segment file missing: %v", err)
	}
	r.Stop()
}

func TestPreRecordBuffer(t *testing.T) {
	cfg := testConfig(t)
	cfg.RecordingPreSec = 1 // 15fps * 1s = 15 frames ring
	r := New(cfg)
	r.Start()
	defer r.Stop()

	if !r.WantsFrames() {
		t.Fatal("should want frames with pre_seconds > 0")
	}

	// 링버퍼를 채우고.
	for i := 0; i < 20; i++ {
		r.Submit(testFrame(320, 240))
	}
	time.Sleep(300 * time.Millisecond)

	if !r.StartRecording() {
		t.Fatal("start recording failed")
	}
	// 링버퍼 flush로 프레임이 즉시 기록됐어야 함.
	time.Sleep(200 * time.Millisecond)
	stats := r.Stats()
	if frames := stats["frames_written"].(int); frames < 1 {
		t.Fatalf("frames_written = %d, want >= 1 (ring flush)", frames)
	}
	r.StopRecording()
}

func TestSegmentRotation(t *testing.T) {
	cfg := testConfig(t)
	cfg.RecordingFormat = "avi"
	cfg.RecordingMaxFileMB = 1 // 1MB로 작게 설정
	r := New(cfg)
	if !r.StartRecording() {
		t.Fatal("start recording failed")
	}
	// 충분히 많은 프레임을 기록해 회전 트리거.
	for i := 0; i < 200; i++ {
		r.Submit(testFrame(640, 480))
	}
	time.Sleep(500 * time.Millisecond)
	segments := r.StopRecording()
	r.Stop()
	// AVI MJPG 640x480은 프레임당 수십KB이므로 여러 세그먼트가 나와야 함.
	if len(segments) < 1 {
		t.Fatal("no segments")
	}
}

func TestRecordingDisabledByDefault(t *testing.T) {
	cfg := testConfig(t)
	r := New(cfg)
	// recording_enabled=false면 Start()가 워커를 시작하지 않지만,
	// explicit StartRecording()은 워커를 지연 시작한다.
	if r.WorkerRunning() {
		t.Fatal("worker should not run without start")
	}
	if !r.StartRecording() {
		t.Fatal("explicit start_recording should work")
	}
	r.StopRecording()
	r.Stop()
}

func TestWantsFramesWhenRecording(t *testing.T) {
	cfg := testConfig(t)
	r := New(cfg)
	r.Start()
	defer r.Stop()
	if r.WantsFrames() {
		t.Fatal("should not want frames when not recording and pre_seconds=0")
	}
	r.StartRecording()
	if !r.WantsFrames() {
		t.Fatal("should want frames when recording")
	}
	r.StopRecording()
	if r.WantsFrames() {
		t.Fatal("should not want frames after stop")
	}
}
