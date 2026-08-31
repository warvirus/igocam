// PTZ 자동 데모 예제 - IPyCam ptz_demo.py의 Go 변환
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

func imagePt(x, y int) image.Point { return image.Pt(x, y) }

func generateCircleFrame(width, height, cycle int) *gocv.Mat {
	img := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	// 파란 배경에 원점 표시.
	img.SetTo(gocv.NewScalar(100, 100, 50, 255))
	// 움직이는 원.
	cx := int(float64(width)*0.5 + math.Sin(float64(cycle)*0.05)*float64(width)*0.3)
	cy := int(float64(height)*0.5 + math.Cos(float64(cycle)*0.05)*float64(height)*0.3)
	gocv.Circle(&img, imagePt(cx, cy), 30, color.RGBA{200, 0, 0, 255}, 2)
	return &img
}

func main() {
	cfg := config.DefaultConfig()
	cfg.Name = "PTZ Demo Camera"
	cfg.MainWidth = 640
	cfg.MainHeight = 480
	cfg.MainFPS = 30

	cam, err := runner.NewSingleCamera(cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start camera: %v\n", err)
		os.Exit(1)
	}
	defer cam.Stop()

	fmt.Printf("Web UI: http://%s:%d/\n", cfg.LocalIP, cfg.WebPort)
	fmt.Println("PTZ auto-demo: zoom in/out cycle every 5s")
	fmt.Println("Press Ctrl+C to stop")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	lastZoom := time.Now()
	zoomDir := 1.0 // 1 = zoom in, -1 = zoom out

	for cam.IsRunning() {
		select {
		case <-sigCh:
			return
		default:
		}
		// 5초마다 줌 방향 전환.
		if time.Since(lastZoom) > 5*time.Second {
			zoomDir = -zoomDir
			lastZoom = time.Now()
			// PTZ 연속 줌 이동.
			cam.PTZ.ContinuousMove(0, 0, zoomDir*0.5)
		}
		frame := generateCircleFrame(cfg.MainWidth, cfg.MainHeight, int(time.Since(lastZoom).Seconds()))
		cam.Stream(frame)
		frame.Close()
	}
}