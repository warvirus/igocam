// WS-Discovery 테스트
package discovery

import (
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"igocam/internal/config"
	"igocam/internal/onvif"
	"igocam/internal/ptz"
)

func freeUDPPort(t *testing.T) int {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}

func testService(t *testing.T) *onvif.Service {
	cfg := config.DefaultConfig()
	cfg.Name = "TestCam"
	cfg.LocalIP = "127.0.0.1"
	cfg.OnvifPort = 8080
	ptzCtrl := ptz.New(cfg.MainWidth, cfg.MainHeight, 4.0, "")
	t.Cleanup(ptzCtrl.Stop)
	return onvif.New(cfg, ptzCtrl)
}

func TestIsProbeRequest(t *testing.T) {
	s := New(nil, 0)
	// 진짜 Probe 요청.
	probe := `<?xml version="1.0"?><Envelope><Header><Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</Action></Header></Envelope>`
	if !s.isProbeRequest(probe) {
		t.Fatal("Probe action should be detected")
	}
	// ProbeMatch 응답은 제외.
	probeMatch := `<ProbeMatch><EndpointReference>...</EndpointReference></ProbeMatch>`
	if s.isProbeRequest(probeMatch) {
		t.Fatal("ProbeMatch should not be treated as probe")
	}
	// <Probe> 요소로 감지.
	probeElem := `<d:Probe><d:Types>tds:Device</d:Types></d:Probe>`
	if !s.isProbeRequest(probeElem) {
		t.Fatal("<Probe> element should be detected")
	}
	// 관련 없는 메시지.
	if s.isProbeRequest("<random>hello world</random>") {
		t.Fatal("unrelated message should not be a probe")
	}
}

func TestLoopbackProbeMatch(t *testing.T) {
	port := freeUDPPort(t)
	svc := testService(t)
	server := New([]*onvif.Service{svc}, port)
	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer server.Stop()

	time.Sleep(200 * time.Millisecond)
	replies := SendProbe("127.0.0.1", port, 2*time.Second)
	if len(replies) == 0 {
		t.Fatal("no ProbeMatch received over loopback")
	}
	if !strings.Contains(replies[0], "ProbeMatch") {
		t.Fatalf("reply is not a ProbeMatch: %s", replies[0][:100])
	}
}

func TestMultiCameraProbeMatch(t *testing.T) {
	port := freeUDPPort(t)
	svc1 := testService(t)
	svc2 := testService(t)
	server := New([]*onvif.Service{svc1, svc2}, port)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	time.Sleep(200 * time.Millisecond)
	replies := SendProbe("127.0.0.1", port, 2*time.Second)
	if len(replies) != 2 {
		t.Fatalf("got %d ProbeMatch replies, want 2", len(replies))
	}
}

func TestBuildAnnouncement(t *testing.T) {
	svc := testService(t)
	msg := buildAnnouncement("Hello", svc)
	if !strings.Contains(msg, "<d:Hello>") {
		t.Fatal("missing Hello element")
	}
	if !strings.Contains(msg, "TestCam") {
		t.Fatal("missing camera name in scopes")
	}
	if !strings.Contains(msg, "http://127.0.0.1:8080/onvif/device_service") {
		t.Fatal("missing XAddrs")
	}
}

func TestStopIdempotent(t *testing.T) {
	port := freeUDPPort(t)
	svc := testService(t)
	server := New([]*onvif.Service{svc}, port)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	server.Stop()
	// 두 번 호출해도 크래시 없어야 함.
	server.Stop()
}

var _ = fmt.Sprint
