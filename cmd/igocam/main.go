// igocam CLI 엔트리포인트 - 다중 카메라 ONVIF/PTZ/스트리밍 서버
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"igocam/internal/admin"
	"igocam/internal/config"
	"igocam/internal/manager"
)

func main() {
	var (
		configPath string
		logLevel   string
		adminPort  int
		adminUser  string
		adminPass  string
	)
	flag.StringVar(&configPath, "config", config.DefaultConfigPath, "path to camera config JSON file")
	flag.StringVar(&logLevel, "log-level", "INFO", "log level: DEBUG/INFO/WARNING/ERROR")
	flag.IntVar(&adminPort, "port", 8080, "admin server port")
	flag.StringVar(&adminUser, "admin-user", "", "admin basic auth username (optional)")
	flag.StringVar(&adminPass, "admin-pass", "", "admin basic auth password (optional)")
	flag.Parse()

	configs, err := config.LoadAll(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}
	if err := config.ValidateCameraConfigs(configs); err != nil {
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	// go2rtc 바이너리 탐색 (동일 디렉터리 우선, PATH fallback).
	go2rtcBin := findGo2rtc()

	mgr := manager.New(configs)
	mgr.SetGo2rtcBin(go2rtcBin)
	mgr.SetAdminPort(adminPort)

	if !mgr.Start() {
		fmt.Fprintln(os.Stderr, "Failed to start cameras")
		os.Exit(1)
	}

	// 관리 서버 시작.
	adm := admin.New(adminPort, mgr, adminUser, adminPass)
	if err := adm.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start admin server: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Admin UI:  http://127.0.0.1:%d/\n", adminPort)

	// URL 배너 출력.
	for _, cam := range mgr.Cameras() {
		cfg := cam.Config
		fmt.Printf("  Camera: %s\n", cfg.Name)
		fmt.Printf("    Web UI:    http://%s:%d/\n", cfg.LocalIP, cfg.OnvifPort)
		fmt.Printf("    ONVIF:     %s\n", cfg.OnvifURL())
		fmt.Printf("    RTSP:      %s\n", cfg.MainStreamRTSP())
		fmt.Printf("    MJPEG:     http://%s:%d/%s\n", cfg.LocalIP, cfg.OnvifPort, cfg.MjpegURL)
	}

	os.Exit(mgr.RunForever())
}

// findGo2rtc go2rtc 바이너리 경로 탐색.
func findGo2rtc() string {
	// 실행 파일과 같은 디렉터리 우선.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates := []string{
			filepath.Join(dir, "go2rtc"),
			"go2rtc",
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return "go2rtc"
}
