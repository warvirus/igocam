// 기본 웹캠 예제 - IPyCam basic_webcam.py의 Go 변환
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"gocv.io/x/gocv"

	"igocam/examples/runner"
	"igocam/internal/config"
)

func main() {
	cfg := config.DefaultConfig()
	cfg.Name = "Webcam Camera"
	cfg.SourceType = "camera"
	cfg.SourceInfo = "Camera Index 0"

	cam, err := runner.NewSingleCamera(cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start camera: %v\n", err)
		os.Exit(1)
	}
	defer cam.Stop()

	fmt.Printf("Web UI: http://%s:%d/\n", cfg.LocalIP, cfg.WebPort)
	fmt.Printf("RTSP:  %s\n", cfg.MainStreamRTSP())
	fmt.Println("Press Ctrl+C to stop")

	// 웹캠 열기.
	cap, err := gocv.OpenVideoCapture(0)
	if err != nil || !cap.IsOpened() {
		fmt.Println("Error: Could not open webcam")
		return
	}
	defer cap.Close()
	cap.Set(gocv.VideoCaptureFrameWidth, float64(cfg.MainWidth))
	cap.Set(gocv.VideoCaptureFrameHeight, float64(cfg.MainHeight))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	img := gocv.NewMat()
	defer img.Close()

	for cam.IsRunning() {
		select {
		case <-sigCh:
			return
		default:
		}
		if ok := cap.Read(&img); !ok {
			fmt.Println("Error: Failed to read frame")
			return
		}
		cam.Stream(&img)
	}
}
