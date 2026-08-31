// 생성 콘텐츠 예제 - IPyCam generated_content.py의 Go 변환
package main

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"syscall"

	"gocv.io/x/gocv"

	"igocam/examples/runner"
	"igocam/internal/config"
)

// generateGradientFrame 움직이는 그라데이션 프레임 생성 (IPyCam과 동일 로직).
func generateGradientFrame(width, height, offset int) *gocv.Mat {
	img := gocv.NewMatWithSize(height, width, gocv.MatTypeCV8UC3)
	t := float64(offset) * 0.02
	for y := 0; y < height; y++ {
		yy := float64(y) / float64(height)
		for x := 0; x < width; x++ {
			xx := float64(x) / float64(width)
			r := uint8(math.Sin(xx*3+t)*127 + 128)
			g := uint8(math.Sin(yy*3+t+2)*127 + 128)
			b := uint8(math.Sin((xx+yy)*2+t+4)*127 + 128)
			// OpenCV BGR 순서.
			img.SetUCharAt(y, x*3+0, b)
			img.SetUCharAt(y, x*3+1, g)
			img.SetUCharAt(y, x*3+2, r)
		}
	}
	return &img
}

func main() {
	cfg := config.DefaultConfig()
	cfg.Name = "Generated Content Camera"
	cfg.MainWidth = 1280
	cfg.MainHeight = 720
	cfg.MainFPS = 30
	cfg.SourceType = "generated"
	cfg.SourceInfo = "Animated Gradient"

	cam, err := runner.NewSingleCamera(cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start camera: %v\n", err)
		os.Exit(1)
	}
	defer cam.Stop()

	fmt.Printf("Web UI: http://%s:%d/\n", cfg.LocalIP, cfg.WebPort)
	fmt.Printf("RTSP:  %s\n", cfg.MainStreamRTSP())
	fmt.Println("Press Ctrl+C to stop")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	frameCount := 0
	for cam.IsRunning() {
		select {
		case <-sigCh:
			return
		default:
		}
		frame := generateGradientFrame(cfg.MainWidth, cfg.MainHeight, frameCount)
		cam.Stream(frame)
		frame.Close()
		frameCount++
	}
}
