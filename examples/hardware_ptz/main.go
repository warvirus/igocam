// 하드웨어 PTZ 예제 - 외부 모터/서보 컨트롤러에 PTZ 명령 연결
package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gocv.io/x/gocv"

	"igocam/examples/runner"
	"igocam/internal/config"
)

// PrintPTZHandler 모든 PTZ 명령을 출력하는 핸들러.
type PrintPTZHandler struct{}

func (h *PrintPTZHandler) OnContinuousMove(pan, tilt, zoom float64) {
	fmt.Printf("[HW] Continuous move: pan=%+.2f tilt=%+.2f zoom=%+.2f\n", pan, tilt, zoom)
}
func (h *PrintPTZHandler) OnStop() {
	fmt.Println("[HW] Stop all movement")
}
func (h *PrintPTZHandler) OnAbsoluteMove(pan, tilt, zoom *float64) {
	fmt.Printf("[HW] Absolute move: pan=%v tilt=%v zoom=%v\n", deref(pan), deref(tilt), deref(zoom))
}
func (h *PrintPTZHandler) OnRelativeMove(pan, tilt, zoom float64) {
	fmt.Printf("[HW] Relative move: pan=%+.2f tilt=%+.2f zoom=%+.2f\n", pan, tilt, zoom)
}
func (h *PrintPTZHandler) OnGotoPreset(token string, pan, tilt, zoom float64) {
	fmt.Printf("[HW] Goto preset '%s': pan=%.2f tilt=%.2f zoom=%.2f\n", token, pan, tilt, zoom)
}
func (h *PrintPTZHandler) OnGotoHome() {
	fmt.Println("[HW] Goto home")
}

// ServoPTZHandler 서보 각도(0~180°) 시뮬레이션 핸들러.
type ServoPTZHandler struct {
	panAngle  int
	tiltAngle int
}

func (s *ServoPTZHandler) OnContinuousMove(pan, tilt, _ float64) {
	fmt.Printf("[Servo] Would move: pan=%+.2f tilt=%+.2f\n", pan, tilt)
}
func (s *ServoPTZHandler) OnStop() {
	fmt.Println("[Servo] Motors stopped")
}
func (s *ServoPTZHandler) OnAbsoluteMove(pan, tilt, _ *float64) {
	if pan != nil {
		s.panAngle = int((*pan + 1) * 90)
		fmt.Printf("[Servo] Pan servo -> %d°\n", s.panAngle)
	}
	if tilt != nil {
		s.tiltAngle = int((*tilt + 1) * 90)
		fmt.Printf("[Servo] Tilt servo -> %d°\n", s.tiltAngle)
	}
}
func (s *ServoPTZHandler) OnRelativeMove(pan, tilt, _ float64) {
	if pan != 0 {
		s.panAngle = clampAngle(s.panAngle + int(pan*90))
		fmt.Printf("[Servo] Pan servo -> %d°\n", s.panAngle)
	}
	if tilt != 0 {
		s.tiltAngle = clampAngle(s.tiltAngle + int(tilt*90))
		fmt.Printf("[Servo] Tilt servo -> %d°\n", s.tiltAngle)
	}
}
func (s *ServoPTZHandler) OnGotoPreset(_ string, pan, tilt, _ float64) {
	s.panAngle = clampAngle(int((pan + 1) * 90))
	s.tiltAngle = clampAngle(int((tilt + 1) * 90))
	fmt.Printf("[Servo] Preset -> pan=%d° tilt=%d°\n", s.panAngle, s.tiltAngle)
}
func (s *ServoPTZHandler) OnGotoHome() {
	s.panAngle = 90
	s.tiltAngle = 90
	fmt.Println("[Servo] Servos centered (home)")
}

func deref(v *float64) any {
	if v == nil {
		return "nil"
	}
	return *v
}

func clampAngle(a int) int {
	if a < 0 {
		return 0
	}
	if a > 180 {
		return 180
	}
	return a
}

func main() {
	cfg := config.DefaultConfig()
	cfg.Name = "Hardware PTZ Camera"
	cfg.MainWidth = 640
	cfg.MainHeight = 360
	cfg.MainFPS = 15
	cfg.SourceType = "camera"
	cfg.SourceInfo = "Camera Index 0 (Hardware PTZ)"

	cam, err := runner.NewSingleCamera(cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start camera: %v\n", err)
		os.Exit(1)
	}
	defer cam.Stop()

	// 외부 하드웨어 핸들러 등록 (여러 개 가능 — 모든 핸들러에 명령 전달).
	cam.PTZ.AddHardwareHandler(&PrintPTZHandler{})
	cam.PTZ.AddHardwareHandler(&ServoPTZHandler{panAngle: 90, tiltAngle: 90})

	fmt.Printf("Web UI: http://%s:%d/\n", cfg.LocalIP, cfg.WebPort)
	fmt.Println("Hardware PTZ handlers registered. Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 자동 PTZ 데모: 4초마다 명령 전송.
	lastCmd := time.Now()
	step := 0

	for cam.IsRunning() {
		select {
		case <-sigCh:
			return
		default:
		}
		if time.Since(lastCmd) > 4*time.Second {
			switch step % 3 {
			case 0:
				cam.PTZ.AbsoluteMove(f64(0.5), f64(-0.3), f64(0.2))
			case 1:
				cam.PTZ.RelativeMove(0.1, 0.1, 0.0)
			case 2:
				cam.PTZ.GotoHome()
			}
			step++
			lastCmd = time.Now()
		}
		frame := circleFrame(cfg.MainWidth, cfg.MainHeight, int(time.Since(lastCmd).Milliseconds()/200))
		cam.Stream(frame)
		frame.Close()
	}
}

func circleFrame(w, h, cycle int) *gocv.Mat {
	img := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)
	img.SetTo(gocv.NewScalar(50, 50, 100, 255))
	cx := int(float64(w)*0.5 + math.Sin(float64(cycle)*0.1)*float64(w)*0.3)
	cy := int(float64(h)*0.5 + math.Cos(float64(cycle)*0.1)*float64(h)*0.3)
	gocv.Circle(&img, image.Pt(cx, cy), 25, color.RGBA{200, 200, 0, 255}, 2)
	return &img
}

func f64(v float64) *float64 { return &v }