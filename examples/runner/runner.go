// 예제 공통 헬퍼 - 단일 카메라에 go2rtc + HTTP 서버 배선
package runner

import (
	"fmt"
	"path/filepath"

	"igocam/internal/camera"
	"igocam/internal/config"
	"igocam/internal/gostream"
	"igocam/internal/httpserver"
)

// NewSingleCamera go2rtc + HTTP 서버가 배선된 단일 카메라 생성.
// go2rtcBin이 비어 있으면 PATH에서 찾는다.
func NewSingleCamera(cfg *config.CameraConfig, go2rtcBin string) (*camera.Camera, error) {
	cam := camera.New(cfg)

	// go2rtc 설정 파일 생성 및 시작.
	cfgPath := filepath.Join("data", fmt.Sprintf("go2rtc_%s.yaml", cfg.Name))
	if err := gostream.WriteGo2rtcConfig(cfgPath, gostream.CameraStream{
		MainStream:  cfg.MainStreamName,
		SubStream:   cfg.SubStreamName,
		APIPort:     cfg.Go2rtcAPIPort,
		RTSPPort:    cfg.RTSPPort,
		RTMPPort:    cfg.RTMPPort,
		WebRTCPort:  8555,
	}); err != nil {
		return nil, err
	}
	goStream := gostream.New(gostream.Config{
		Go2rtcBin:  go2rtcBin,
		ConfigPath: cfgPath,
		APIPort:    cfg.Go2rtcAPIPort,
		RTSPPort:   cfg.RTSPPort,
		RTMPPort:   cfg.RTMPPort,
		WebRTCPort: 8555,
	})
	if err := goStream.Start(); err != nil {
		return nil, fmt.Errorf("go2rtc start: %w", err)
	}
	cam.SetGoStream(goStream)

	// HTTP 서버 배선.
	cam.SetHTTPServer(httpserver.New(cam))

	if !cam.Start(false) {
		goStream.Stop()
		return nil, fmt.Errorf("camera start failed")
	}
	return cam, nil
}
