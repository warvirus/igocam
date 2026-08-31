// 프레임 소스 인터페이스 및 구현 - 캡처 장치/비디오 파일/RTSP URL
package capture

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"gocv.io/x/gocv"
)

// VIDEO_EXTENSIONS is a set of recognized video file extensions
var VIDEO_EXTENSIONS = map[string]bool{
	".mp4": true, ".avi": true, ".mkv": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
	".mpeg": true, ".mpg": true, ".3gp": true,
}

// SourceType describes the kind of video source.
type SourceType string

const (
	SourceCamera    SourceType = "camera"
	SourceVideoFile SourceType = "video_file"
	SourceRTSP      SourceType = "rtsp"
	SourceCustom    SourceType = "custom"
	SourceUpload    SourceType = "video_file" // upload mode
)

// FrameSource is the interface for reading frames from any source.
type FrameSource interface {
	// Read reads the next frame. Returns false on EOF or error.
	Read() (gocv.Mat, bool)
	// Close releases the source.
	Close()
	// SourceType returns the type of this source.
	SourceType() SourceType
	// Configure sets source parameters (width, height, fps). No-op for most sources.
	Configure(width, height, fps int)
	// FPS returns the source's native frame rate, or 0 if unknown.
	FPS() float64
}

// InferSourceType determines the source type from a source string.
func InferSourceType(sourceArg string) (SourceType, string) {
	if strings.ToLower(sourceArg) == "video" {
		return SourceVideoFile, "Waiting for upload..."
	}
	if idx, err := strconv.Atoi(sourceArg); err == nil {
		return SourceCamera, fmt.Sprintf("Camera Index %d", idx)
	}
	lower := strings.ToLower(sourceArg)
	if strings.HasPrefix(lower, "rtsp://") || strings.HasPrefix(lower, "rtmp://") ||
		strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return SourceRTSP, sourceArg
	}
	if _, err := os.Stat(sourceArg); err == nil {
		return SourceVideoFile, filepath.Base(sourceArg)
	}
	ext := strings.ToLower(filepath.Ext(sourceArg))
	if VIDEO_EXTENSIONS[ext] {
		return SourceVideoFile, filepath.Base(sourceArg)
	}
	return SourceCustom, sourceArg
}

// OpenSource opens a source by string. Returns the source and its type info.
func OpenSource(sourceArg string) (FrameSource, SourceType, string, error) {
	srcType, srcInfo := InferSourceType(sourceArg)
	var src FrameSource
	var err error

	switch srcType {
	case SourceCamera:
		idx, _ := strconv.Atoi(sourceArg)
		src, err = NewCameraSource(idx)
	case SourceVideoFile, SourceCustom:
		src, err = NewVideoFileSource(sourceArg)
	case SourceRTSP:
		src, err = NewVideoFileSource(sourceArg) // gocv handles URLs via VideoCapture
	}
	if err != nil {
		return nil, srcType, srcInfo, err
	}
	return src, srcType, srcInfo, nil
}

// CameraSource opens a local camera device.
type CameraSource struct {
	capture *gocv.VideoCapture
}

// ProbeFPS 비디오 파일/URL의 원본 FPS를 조회한다. 실패 시 0 반환.
func ProbeFPS(path string) float64 {
	src, err := NewVideoFileSource(path)
	if err != nil {
		return 0
	}
	defer src.Close()
	return src.FPS()
}
// NewCameraSource opens a camera device by index with platform-optimized backend.
func NewCameraSource(index int) (FrameSource, error) {
	backends := []gocv.VideoCaptureAPI{}

	switch runtime.GOOS {
	case "windows":
		backends = append(backends, gocv.VideoCaptureMSMF, gocv.VideoCaptureDshow)
	case "linux":
		backends = append(backends, gocv.VideoCaptureV4L2)
	default:
		backends = append(backends, gocv.VideoCaptureAny)
	}

	var cap *gocv.VideoCapture
	var err error
	for _, backend := range backends {
		cap, err = gocv.VideoCaptureDeviceWithAPI(index, backend)
		if err == nil && cap.IsOpened() {
			cap.Set(gocv.VideoCaptureBufferSize, 1)
			cap.Set(gocv.VideoCaptureFOURCC, float64(fourcc("MJPG")))
			return &CameraSource{capture: cap}, nil
		}
		if cap != nil {
			cap.Close()
		}
	}

	// fallback: no backend
	cap, err = gocv.OpenVideoCapture(index)
	if err != nil || !cap.IsOpened() {
		if cap != nil {
			cap.Close()
		}
		return nil, fmt.Errorf("could not open camera %d: %w", index, err)
	}
	return &CameraSource{capture: cap}, nil
}

func (s *CameraSource) Read() (gocv.Mat, bool) {
	img := gocv.NewMat()
	if ok := s.capture.Read(&img); !ok {
		img.Close()
		return img, false
	}
	return img, true
}

func (s *CameraSource) Close() {
	if s.capture != nil {
		s.capture.Close()
	}
}

func (s *CameraSource) SourceType() SourceType { return SourceCamera }

func (s *CameraSource) Configure(width, height, fps int) {
	if s.capture != nil {
		s.capture.Set(gocv.VideoCaptureBufferSize, 1)
		s.capture.Set(gocv.VideoCaptureFrameWidth, float64(width))
		s.capture.Set(gocv.VideoCaptureFrameHeight, float64(height))
		s.capture.Set(gocv.VideoCaptureFPS, float64(fps))
	}
}

func (s *CameraSource) FPS() float64 { return 0 }

// videoFileSource opens a video file or URL.
type videoFileSource struct {
	capture *gocv.VideoCapture
	path    string
}

// NewVideoFileSource opens a video file or RTSP URL.
func NewVideoFileSource(path string) (FrameSource, error) {
	cap, err := gocv.VideoCaptureFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not open %s: %w", path, err)
	}
	if !cap.IsOpened() {
		cap.Close()
		return nil, fmt.Errorf("could not open %s", path)
	}
	return &videoFileSource{capture: cap, path: path}, nil
}

func (s *videoFileSource) Read() (gocv.Mat, bool) {
	img := gocv.NewMat()
	if ok := s.capture.Read(&img); !ok {
		img.Close()
		return img, false
	}
	return img, true
}

func (s *videoFileSource) Close() {
	if s.capture != nil {
		s.capture.Close()
	}
}

func (s *videoFileSource) SourceType() SourceType { return SourceVideoFile }

func (s *videoFileSource) Configure(width, height, fps int) {
	// 파일 소스는 설정 불필요.
}

func (s *videoFileSource) FPS() float64 {
	if s.capture != nil {
		return s.capture.Get(gocv.VideoCaptureFPS)
	}
	return 0
}

// Path returns the source path.
func (s *videoFileSource) Path() string { return s.path }

// LoopSource restarts a finished file/URL source so it plays from start again.
// Returns (source, firstFrame, ok). On failure, sleeps ~1s to prevent busy-loop.
func LoopSource(src FrameSource, path string) (FrameSource, gocv.Mat, bool) {
	// 1. If it's a rewindable source, try seeking.
	if vs, ok := src.(*videoFileSource); ok {
		vs.capture.Set(gocv.VideoCapturePosFrames, 0)
		img := gocv.NewMat()
		if ok := vs.capture.Read(&img); ok {
			return vs, img, true
		}
		img.Close()
		// 2. Seek failed: reopen from scratch.
		vs.Close()
		newSrc, err := NewVideoFileSource(path)
		if err == nil {
			if nvs, ok := newSrc.(*videoFileSource); ok {
				img := gocv.NewMat()
				if ok := nvs.capture.Read(&img); ok {
					return nvs, img, true
				}
				img.Close()
			}
		}
	}
	// 3. Still no frame: back off.
	src.Close()
	time.Sleep(time.Second)
	empty := gocv.NewMat()
	empty.Close()
	return nil, gocv.Mat{}, false
}

// ResizeFrame 소스 프레임을 target width/height로 리사이즈한다.
// 항상 새 Mat을 반환한다 (src를 그대로 돌려주지 않음). src는 호출자가 Close하고,
// 반환된 Mat도 호출자가 Close해야 한다.
func ResizeFrame(src gocv.Mat, targetW, targetH int) gocv.Mat {
	dst := gocv.NewMat()
	if src.Cols() == targetW && src.Rows() == targetH {
		src.CopyTo(&dst)
		return dst
	}
	gocv.Resize(src, &dst, image.Pt(targetW, targetH), 0, 0, gocv.InterpolationArea)
	return dst
}

// fourcc converts a four-character code to int.
func fourcc(code string) int {
	if len(code) != 4 {
		return 0
	}
	return int(code[0]) | int(code[1])<<8 | int(code[2])<<16 | int(code[3])<<24
}