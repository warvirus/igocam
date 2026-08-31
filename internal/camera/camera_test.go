// 카메라 코어 파이프라인 테스트
package camera

import (
	"testing"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/config"
)

func testCamera(t *testing.T) *Camera {
	cfg := config.DefaultConfig()
	cfg.Name = "TestCam"
	cfg.MainWidth = 320
	cfg.MainHeight = 240
	cfg.MainFPS = 15
	cfg.ShowTimestamp = false
	c := New(cfg)
	return c
}

func testFrame(w, h int) *gocv.Mat {
	img := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)
	return &img
}

func TestCameraStartStop(t *testing.T) {
	c := testCamera(t)
	if c.IsRunning() {
		t.Fatal("should not be running initially")
	}
	if !c.Start(false) {
		t.Fatal("start failed")
	}
	if !c.IsRunning() {
		t.Fatal("should be running")
	}
	c.Stop()
	if c.IsRunning() {
		t.Fatal("should stop running")
	}
}

func TestCameraStreamAndSnapshot(t *testing.T) {
	c := testCamera(t)
	c.Start(false)
	defer c.Stop()

	c.Stream(testFrame(320, 240))

	// 스냅샷은 독립 복사본이어야 함.
	snap := c.GetSnapshotFrame()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	defer snap.Close()
	if snap.Cols() != 320 || snap.Rows() != 240 {
		t.Fatalf("snapshot size = %dx%d", snap.Cols(), snap.Rows())
	}
}

func TestCameraStreamTransforms(t *testing.T) {
	c := testCamera(t)
	c.Config.Rotation = 90
	c.Start(false)
	defer c.Stop()

	c.Stream(testFrame(200, 100))
	snap := c.GetSnapshotFrame()
	if snap == nil {
		t.Fatal("snapshot is nil")
	}
	defer snap.Close()
	// 90도 회전 -> 200x100이 100x200으로.
	if snap.Cols() != 100 || snap.Rows() != 200 {
		t.Fatalf("rotated snapshot size = %dx%d, want 100x200", snap.Cols(), snap.Rows())
	}
}

func TestCameraRecordingPassthrough(t *testing.T) {
	c := testCamera(t)
	c.Start(false)
	defer c.Stop()

	if !c.StartRecording() {
		t.Fatal("start recording failed")
	}
	if !c.IsRecording() {
		t.Fatal("should be recording")
	}
	c.Stream(testFrame(320, 240))
	time.Sleep(100 * time.Millisecond)
	segments := c.StopRecording()
	if len(segments) != 1 {
		t.Fatalf("segments = %d, want 1", len(segments))
	}
}

func TestCameraSnapshotNilBeforeStream(t *testing.T) {
	c := testCamera(t)
	c.Start(false)
	defer c.Stop()
	if c.GetSnapshotFrame() != nil {
		t.Fatal("snapshot should be nil before any frame")
	}
}
