// 비디오 파일 재생 예제 - IPyCam video_file.py의 Go 변환
package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"gocv.io/x/gocv"

	"igocam/examples/runner"
	"igocam/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: video_file <path_to_video_file>")
		fmt.Println("Example: video_file sample.mp4")
		os.Exit(1)
	}
	videoPath := os.Args[1]

	cfg := config.DefaultConfig()
	cfg.Name = "Video File Camera"
	cfg.SourceType = "video_file"
	cfg.SourceInfo = filepath.Base(videoPath)

	cam, err := runner.NewSingleCamera(cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start camera: %v\n", err)
		os.Exit(1)
	}
	defer cam.Stop()

	fmt.Printf("Streaming video file: %s\n", videoPath)
	fmt.Printf("Web UI: http://%s:%d/\n", cfg.LocalIP, cfg.WebPort)
	fmt.Printf("RTSP:  %s\n", cfg.MainStreamRTSP())
	fmt.Println("Press Ctrl+C to stop")

	cap, err := gocv.VideoCaptureFile(videoPath)
	if err != nil || !cap.IsOpened() {
		fmt.Printf("Error: Could not open video file: %s\n", videoPath)
		return
	}
	defer cap.Close()

	fps := cap.Get(gocv.VideoCaptureFPS)
	if fps < 1 {
		fps = 30
	}

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
			// 파일 끝 -> 처음부터 반복.
			cap.Set(gocv.VideoCapturePosFrames, 0)
			fmt.Println("Looping video...")
			continue
		}
		cam.Stream(&img)
	}
}
