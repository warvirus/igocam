// PTZ 컨트롤러 테스트
package ptz

import (
	"testing"

	"gocv.io/x/gocv"
)

func TestNewController(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	if c.OutputWidth != 1920 || c.OutputHeight != 1080 {
		t.Fatalf("size = %dx%d", c.OutputWidth, c.OutputHeight)
	}
	if c.MaxZoom != 4.0 {
		t.Fatalf("MaxZoom = %v", c.MaxZoom)
	}
	// 기본 home 프리셋이 있어야 함.
	presets := c.GetPresets()
	if _, ok := presets["home"]; !ok {
		t.Fatal("home preset missing")
	}
}

func TestApplyPTZDefault(t *testing.T) {
	c := New(100, 100, 4.0, "")
	defer c.Stop()
	src := gocv.NewMatWithSize(100, 100, gocv.MatTypeCV8UC3)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	c.ApplyPTZ(&src, &dst)
	// 기본 위치에서는 원본 크기 그대로.
	if dst.Cols() != 100 || dst.Rows() != 100 {
		t.Fatalf("default PTZ size = %dx%d", dst.Cols(), dst.Rows())
	}
}

func TestApplyPTZZoom(t *testing.T) {
	c := New(50, 50, 2.0, "")
	defer c.Stop()
	src := gocv.NewMatWithSize(200, 200, gocv.MatTypeCV8UC3)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	// 줌 1.0 (max zoom 2x) -> 크롭 100x100 -> 리사이즈 50x50.
	zoom := 1.0
	c.AbsoluteMove(nil, nil, &zoom)
	c.ApplyPTZ(&src, &dst)
	if dst.Cols() != 50 || dst.Rows() != 50 {
		t.Fatalf("zoom PTZ size = %dx%d, want 50x50", dst.Cols(), dst.Rows())
	}
}

func TestAbsoluteMoveClamp(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	pan, tilt, zoom := 5.0, -5.0, 2.0
	c.AbsoluteMove(&pan, &tilt, &zoom)
	status := c.GetStatus()
	if status["pan"].(float64) != 1.0 {
		t.Fatalf("pan = %v, want 1.0", status["pan"])
	}
	if status["tilt"].(float64) != -1.0 {
		t.Fatalf("tilt = %v, want -1.0", status["tilt"])
	}
	if status["zoom"].(float64) != 1.0 {
		t.Fatalf("zoom = %v, want 1.0", status["zoom"])
	}
	if status["moving"].(bool) {
		t.Fatal("should not be moving after absolute move")
	}
}

func TestContinuousMoveAndStop(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	c.ContinuousMove(0.5, 0, 0)
	status := c.GetStatus()
	if !status["moving"].(bool) {
		t.Fatal("should be moving")
	}
	c.StopMovement(true, true)
	status = c.GetStatus()
	if status["moving"].(bool) {
		t.Fatal("should stop moving")
	}
}

func TestRelativeMove(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	c.RelativeMove(0.1, 0.1, 0.1)
	status := c.GetStatus()
	if status["pan"].(float64) < 0.09 || status["pan"].(float64) > 0.11 {
		t.Fatalf("pan = %v, want ~0.1", status["pan"])
	}
}

func TestGotoHome(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	c.RelativeMove(0.5, 0.5, 0.5)
	c.GotoHome()
	status := c.GetStatus()
	if status["pan"].(float64) != 0 || status["tilt"].(float64) != 0 || status["zoom"].(float64) != 0 {
		t.Fatalf("home status = %v", status)
	}
}

func TestPresetLifecycle(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	c.RelativeMove(0.3, -0.2, 0.4)
	c.SetPreset("test1", "Test 1")

	if !c.GotoPreset("test1") {
		t.Fatal("goto preset failed")
	}
	if c.GotoPreset("nonexistent") {
		t.Fatal("nonexistent preset should fail")
	}

	presets := c.GetPresets()
	p, ok := presets["test1"]
	if !ok {
		t.Fatal("preset not saved")
	}
	if p.Name != "Test 1" {
		t.Fatalf("preset name = %q", p.Name)
	}

	if !c.RemovePreset("test1") {
		t.Fatal("remove preset failed")
	}
	if c.RemovePreset("test1") {
		t.Fatal("removing nonexistent preset should fail")
	}
}

type mockHardwareHandler struct {
	continuousCalls int
	stopCalls       int
	absoluteCalls   int
	lastPan         *float64
}

func (m *mockHardwareHandler) OnContinuousMove(_, _, _ float64)  { m.continuousCalls++ }
func (m *mockHardwareHandler) OnStop()                            { m.stopCalls++ }
func (m *mockHardwareHandler) OnAbsoluteMove(p, _, _ *float64)    { m.absoluteCalls++; m.lastPan = p }
func (m *mockHardwareHandler) OnRelativeMove(_, _, _ float64)     {}
func (m *mockHardwareHandler) OnGotoPreset(_ string, _, _, _ float64)    {}
func (m *mockHardwareHandler) OnGotoHome()                        {}

func TestHardwareHandler(t *testing.T) {
	c := New(1920, 1080, 4.0, "")
	defer c.Stop()
	m := &mockHardwareHandler{}
	c.AddHardwareHandler(m)

	c.ContinuousMove(0.1, 0, 0)
	if m.continuousCalls != 1 {
		t.Fatalf("continuous calls = %d", m.continuousCalls)
	}
	c.StopMovement(true, true)
	if m.stopCalls != 1 {
		t.Fatalf("stop calls = %d", m.stopCalls)
	}
	pan := 0.5
	c.AbsoluteMove(&pan, nil, nil)
	if m.absoluteCalls != 1 {
		t.Fatalf("absolute calls = %d", m.absoluteCalls)
	}
	if m.lastPan == nil || *m.lastPan != 0.5 {
		t.Fatalf("lastPan = %v", m.lastPan)
	}

	if !c.RemoveHardwareHandler(m) {
		t.Fatal("remove handler failed")
	}
	c.ContinuousMove(0.1, 0, 0)
	if m.continuousCalls != 1 {
		t.Fatal("handler should not be called after removal")
	}
}
