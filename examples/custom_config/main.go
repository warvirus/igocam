// 설정 커스터마이징 예제 - IPyCam custom_config.py의 Go 변환
package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"os/signal"
	"syscall"

	"gocv.io/x/gocv"

	"igocam/internal/config"
	"igocam/examples/runner"
)

func imagePt(x, y int) image.Point     { return image.Pt(x, y) }
func colorRGBA(r, g, b, a uint8) color.RGBA {
	return color.RGBA{r, g, b, a}
}

func main() {
	// CameraConfig 직접 생성 (기본값 → 커스텀 값으로 변경).
	cfg := config.DefaultConfig()
	cfg.Name = "My Custom Camera"
	cfg.Manufacturer = "GoCam"
	cfg.Model = "CustomCam-Pro"
	cfg.SerialNumber = "CC-12345"
	cfg.MainWidth = 1280
	cfg.MainHeight = 720
	cfg.MainFPS = 30
	cfg.MainBitrate = "2M"
	cfg.SubWidth = 640
	cfg.SubHeight = 360
	cfg.SubBitrate = "512K"
	cfg.HWAccel = "cpu"

	// 설정 파일로 저장.
	if err := cfg.Save("my_camera_config.json"); err != nil {
		fmt.Fprintf(os.Stderr, "Save failed: %v\n", err)
	} else {
		fmt.Println("Saved configuration to my_camera_config.json")
	}

	// 저장된 설정 로드 (주석 해제):
	// cfg = config.Load("my_camera_config.json")
	// fmt.Printf("Loaded: %s %dx%d\n", cfg.Name, cfg.MainWidth, cfg.MainHeight)

	cam, err := runner.NewSingleCamera(cfg, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start camera: %v\n", err)
		os.Exit(1)
	}
	defer cam.Stop()

	fmt.Printf("Camera: %s\n", cfg.Name)
	fmt.Printf("Resolution: %dx%d @ %dfps\n", cfg.MainWidth, cfg.MainHeight, cfg.MainFPS)
	fmt.Printf("Bitrate: %s\n", cfg.MainBitrate)
	fmt.Printf("Web UI: http://%s:%d/\n", cfg.LocalIP, cfg.WebPort)
	fmt.Printf("RTSP: %s\n", cfg.MainStreamRTSP())
	fmt.Println("Press Ctrl+C to stop")

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
			continue
		}
		// 커스텀 오버레이 (OpenCV 고정 폰트로 텍스트 표시).
		gocv.PutText(&img, cfg.Name, imagePt(10, 30), gocv.FontHersheySimplex, 1.0, colorRGBA(0, 255, 0, 255), 2)
		cam.Stream(&img)
	}
}