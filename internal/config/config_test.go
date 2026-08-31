// 카메라 설정 테스트
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Name != "Virtual Camera" {
		t.Fatalf("Name = %q, want Virtual Camera", cfg.Name)
	}
	if cfg.MainWidth != 1920 || cfg.MainHeight != 1080 {
		t.Fatalf("res = %dx%d, want 1920x1080", cfg.MainWidth, cfg.MainHeight)
	}
	if cfg.MainStreamName != "video_main" {
		t.Fatalf("MainStreamName = %q", cfg.MainStreamName)
	}
}

func TestAuthEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AuthEnabled() {
		t.Fatal("default should not have auth")
	}
	cfg.Username = "admin"
	cfg.Password = "pass123"
	if !cfg.AuthEnabled() {
		t.Fatal("should have auth")
	}
	cfg.Password = ""
	if cfg.AuthEnabled() {
		t.Fatal("partial creds should not enable auth")
	}
}

func TestSetCredentials(t *testing.T) {
	cfg := DefaultConfig()
	ok, err := cfg.SetCredentials("admin", "pass123")
	if !ok || err != "" {
		t.Fatalf("set creds failed: %s", err)
	}
	if cfg.Username != "admin" || cfg.Password != "pass123" {
		t.Fatal("creds not set")
	}
	// clear
	ok, err = cfg.SetCredentials("", "")
	if !ok || err != "" {
		t.Fatalf("clear creds failed: %s", err)
	}
	if cfg.AuthEnabled() {
		t.Fatal("creds should be cleared")
	}
	// partial
	ok, err = cfg.SetCredentials("admin", "")
	if ok || err == "" {
		t.Fatal("partial creds should fail")
	}
}

func TestURLs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LocalIP = "192.168.1.10"
	cfg.RTSPPort = 8554
	cfg.RTMPPort = 1935
	cfg.OnvifPort = 8080
	cfg.Go2rtcAPIPort = 1984
	cfg.MainStreamName = "video_main"

	rtmp := cfg.MainStreamRTMP()
	if rtmp != "rtmp://127.0.0.1:1935/video_main" {
		t.Fatalf("RTMP URL = %q", rtmp)
	}
	rtsp := cfg.MainStreamRTSP()
	if rtsp != "rtsp://192.168.1.10:8554/video_main" {
		t.Fatalf("RTSP URL = %q", rtsp)
	}
	onvif := cfg.OnvifURL()
	if onvif != "http://192.168.1.10:8080/onvif/device_service" {
		t.Fatalf("ONVIF URL = %q", onvif)
	}
}

func TestValidateUpdate(t *testing.T) {
	cfg := DefaultConfig()
	ok, val := cfg.ValidateUpdate("main_width", "1920")
	if !ok || val != 1920 {
		t.Fatal("main_width validation failed")
	}
	ok, _ = cfg.ValidateUpdate("main_width", "99999")
	if ok {
		t.Fatal("oversized width should be rejected")
	}
	ok, val = cfg.ValidateUpdate("main_bitrate", "8M")
	if !ok || val != "8M" {
		t.Fatal("bitrate 8M should be valid")
	}
	ok, _ = cfg.ValidateUpdate("main_bitrate", "invalid")
	if ok {
		t.Fatal("invalid bitrate should be rejected")
	}
	ok, val = cfg.ValidateUpdate("rotation", 90)
	if !ok || val != 90 {
		t.Fatal("rotation 90 should be valid")
	}
	ok, _ = cfg.ValidateUpdate("rotation", 45)
	if ok {
		t.Fatal("rotation 45 should be rejected")
	}
	ok, val = cfg.ValidateUpdate("hw_accel", "qsv")
	if !ok || val != "qsv" {
		t.Fatal("hw_accel qsv should be valid")
	}
	ok, val = cfg.ValidateUpdate("show_timestamp", "false")
	if !ok || val != false {
		t.Fatal("show_timestamp false should be valid")
	}
	ok, _ = cfg.ValidateUpdate("nonexistent", "value")
	if ok {
		t.Fatal("nonexistent field should be rejected")
	}
}

func TestApplyUpdates(t *testing.T) {
	cfg := DefaultConfig()
	applied, rejected, restart := cfg.ApplyUpdates(map[string]any{
		"main_width": 1280,
		"main_height": 720,
		"flip":        true,
		"rotation":    90,
		"nonexistent": "x",
		"onvif_port":  9999, // EDITABLE_FIELDS에 없음
	})
	if len(rejected) != 2 {
		t.Fatalf("rejected = %v, want 2", rejected)
	}
	if len(applied) != 4 {
		t.Fatalf("applied = %v, want 4", applied)
	}
	if len(restart) != 3 {
		t.Fatalf("restart = %v, want 3 (main_width, main_height, rotation)", restart)
	}
	if cfg.MainWidth != 1280 || cfg.MainHeight != 720 {
		t.Fatal("config not updated")
	}
	if !cfg.Flip {
		t.Fatal("flip should be true")
	}
	if cfg.Rotation != 90 {
		t.Fatal("rotation should be 90")
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test_config.json")

	cfg := DefaultConfig()
	cfg.Name = "Test Camera"
	cfg.MainWidth = 640
	cfg.MainHeight = 480
	cfg.LocalIP = "192.168.1.1"
	cfg.RTSPPort = 8554
	cfg.RTMPPort = 1935
	cfg.OnvifPort = 8080
	cfg.Go2rtcAPIPort = 1984
	cfg.WebPort = 8081
	cfg.MainStreamName = "video_main"
	cfg.SubStreamName = "video_sub"
	cfg.HWAccel = "cpu"
	cfg.Source = "test.mp4"

	if err := cfg.Save(path); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded := Load(path)
	if loaded.Name != "Test Camera" {
		t.Fatalf("loaded Name = %q", loaded.Name)
	}
	if loaded.MainWidth != 640 || loaded.MainHeight != 480 {
		t.Fatalf("loaded res = %dx%d", loaded.MainWidth, loaded.MainHeight)
	}
	if loaded.HWAccel != "cpu" {
		t.Fatalf("loaded HWAccel = %q", loaded.HWAccel)
	}
	if loaded.Source != "test.mp4" {
		t.Fatalf("loaded Source = %q", loaded.Source)
	}
	// local_ip는 저장하지 않으므로 로드 후 비어 있어야 함 (기본값으로 대체)
	if loaded.LocalIP == "" {
		t.Fatal("local_ip should be auto-detected on load")
	}
}

func TestLoadAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "multi.json")
	jsonData := `[
		{"name": "Cam1", "onvif_port": 8080, "rtsp_port": 8554, "rtmp_port": 1935, "web_port": 8081, "go2rtc_api_port": 1984, "webrtc_port": 8555, "main_stream_name": "video_main", "sub_stream_name": "video_sub"},
		{"name": "Cam2", "onvif_port": 8090, "rtsp_port": 8564, "rtmp_port": 1945, "web_port": 8091, "go2rtc_api_port": 1994, "webrtc_port": 8556, "main_stream_name": "video_main", "sub_stream_name": "video_sub"}
	]`
	if err := os.WriteFile(path, []byte(jsonData), 0o644); err != nil {
		t.Fatal(err)
	}

	configs, err := LoadAll(path)
	if err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(configs))
	}
	if configs[0].Name != "Cam1" || configs[1].Name != "Cam2" {
		t.Fatal("wrong names")
	}
	if err := ValidateCameraConfigs(configs); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// port collision
	configs[1].OnvifPort = 8080
	if err := ValidateCameraConfigs(configs); err == nil {
		t.Fatal("port collision should be detected")
	}
}

func TestLoadNotExists(t *testing.T) {
	cfg := Load("/nonexistent/path.json")
	if cfg.Name != "Virtual Camera" {
		t.Fatal("should return defaults")
	}
}

func TestLoadAllNonArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte(`{"name": "Single"}`), 0o644)
	_, err := LoadAll(path)
	if err == nil {
		t.Fatal("non-array should fail")
	}
}

func TestCoerceBool(t *testing.T) {
	tests := []struct {
		input any
		want  bool
	}{
		{true, true},
		{false, false},
		{1, true},
		{0, false},
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{"yes", true},
		{"no", false},
		{"on", true},
		{"off", false},
		{"invalid", false},
	}
	for _, tt := range tests {
		ok, val := CoerceBool(tt.input)
		if tt.input == "invalid" {
			if ok {
				t.Errorf("CoerceBool(%v) should fail", tt.input)
			}
			continue
		}
		if !ok || val != tt.want {
			t.Errorf("CoerceBool(%v) = (%v, %v), want (true, %v)", tt.input, ok, val, tt.want)
		}
	}
}