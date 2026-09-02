// go2rtc 관리자 테스트
package gostream

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func freeTCPPort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func TestWriteGo2rtcConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go2rtc.yaml")
	cam := CameraStream{
		MainStream:  "video_main",
		SubStream:   "video_sub",
		APIPort:     1984,
		RTSPPort:    8554,
		RTMPPort:    1935,
		WebRTCPort:  8555,
	}
	if err := WriteGo2rtcConfig(path, cam); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"video_main: null",
		"video_sub: null",
		"listen: \":1984\"",
		"listen: \":8554\"",
		"listen: \":1935\"",
		"listen: \":8555\"",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("config missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "candidates") {
		t.Fatalf("config must not hardcode WebRTC candidates:\n%s", content)
	}
}

func TestNewDefaults(t *testing.T) {
	m := New(Config{})
	if m.cfg.APIPort != 1984 {
		t.Fatalf("APIPort = %d", m.cfg.APIPort)
	}
	if m.cfg.RTSPPort != 8554 {
		t.Fatalf("RTSPPort = %d", m.cfg.RTSPPort)
	}
	if m.cfg.RTMPPort != 1935 {
		t.Fatalf("RTMPPort = %d", m.cfg.RTMPPort)
	}
	if m.cfg.WebRTCPort != 8555 {
		t.Fatalf("WebRTCPort = %d", m.cfg.WebRTCPort)
	}
	if m.cfg.MainStream != "video_main" {
		t.Fatalf("MainStream = %q", m.cfg.MainStream)
	}
	if m.cfg.Go2rtcBin != "go2rtc" {
		t.Fatalf("Go2rtcBin = %q", m.cfg.Go2rtcBin)
	}
}

func TestManagerNotRunning(t *testing.T) {
	m := New(Config{})
	if m.Running() {
		t.Fatal("should not be running before Start")
	}
	// 빈 포트(닫힘)에 대한 TCP 연결 확인은 false여야 함.
	if checkTCPPort(freeTCPPort(t)) {
		t.Fatal("RTSP should not be reachable on free port")
	}
	// 중복 Stop은 no-op.
	m.Stop()
}
