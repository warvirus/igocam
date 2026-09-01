// igocam CLI 엔트리포인트 - 다중 카메라 ONVIF/PTZ/스트리밍 서버
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"igocam/internal/admin"
	"igocam/internal/config"
	"igocam/internal/manager"
)

func main() {
	// .env 파일에서 환경변수 로드 (실제 환경변수가 우선).
	loadDotEnv(".env")

	var (
		configPath string
		logLevel   string
		adminPort  int
		adminUser  string
		adminPass  string
	)
	flag.StringVar(&configPath, "config", envOr("IGOCAM_CONFIG", config.DefaultConfigPath), "path to camera config JSON file")
	flag.StringVar(&logLevel, "log-level", envOr("IGOCAM_LOG_LEVEL", "INFO"), "log level: DEBUG/INFO/WARNING/ERROR")
	flag.IntVar(&adminPort, "port", envIntOr("IGOCAM_PORT", 8080), "admin server port")
	flag.StringVar(&adminUser, "admin-user", envOr("IGOCAM_ADMIN_USER", "admin"), "admin basic auth username (optional)")
	flag.StringVar(&adminPass, "admin-pass", envOr("IGOCAM_ADMIN_PASS", "pass"), "admin basic auth password (optional)")
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

// loadDotEnv path의 .env 파일을 읽어 환경변수로 설정한다.
// 이미 실제 환경변수가 있으면 .env 값으로 덮어쓰지 않는다 (실제 환경변수 우선).
// .env 파일이 없거나 읽기 실패하면 조용히 무시한다.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// export PREFIX 제거 (셸 스타일 호환).
		line = strings.TrimPrefix(line, "export ")
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// 따옴표 제거.
		if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' ||
			val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
}

// envOr 환경변수 값이 있으면 반환, 없으면 fallback 반환.
func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// envIntOr 환경변수 값이 유효한 정수면 반환, 없거나 오류면 fallback 반환.
func envIntOr(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}
