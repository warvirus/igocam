// HTTP 서버 테스트
package httpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gocv.io/x/gocv"

	"igocam/internal/camera"
	"igocam/internal/config"
)

func setupServer(t *testing.T, withAuth bool) (*Server, *camera.Camera) {
	cfg := config.DefaultConfig()
	cfg.Name = "TestCam"
	cfg.MainWidth = 320
	cfg.MainHeight = 240
	cfg.MainFPS = 15
	cfg.ShowTimestamp = false
	cfg.WebPort = 0
	if withAuth {
		cfg.SetCredentials("admin", "pass123")
	}
	cam := camera.New(cfg)
	cam.Start(false)
	return New(cam), cam
}

func getRequest(server *Server, path string, withAuth bool) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if withAuth {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:pass123")))
	}
	return req
}

func postJSON(server *Server, path string, body any, withAuth bool) *http.Request {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req := httptest.NewRequest(http.MethodPost, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if withAuth {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("admin:pass123")))
	}
	return req
}

func TestAuthRequired(t *testing.T) {
	server, cam := setupServer(t, true)
	defer cam.Stop()

	// 인증 없으면 401.
	req := getRequest(server, "/", false)
	w := httptest.NewRecorder()
	server.handleRoot(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("missing WWW-Authenticate header")
	}

	// 인증 있으면 200.
	req = getRequest(server, "/", true)
	w = httptest.NewRecorder()
	server.handleRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestNoAuthOpenMode(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/", false)
	w := httptest.NewRecorder()
	server.handleRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (open mode)", w.Code)
	}
}

func TestServeConfig(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/api/config", false)
	w := httptest.NewRecorder()
	server.handleConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var cfg map[string]any
	json.Unmarshal(w.Body.Bytes(), &cfg)
	if cfg["name"] != "TestCam" {
		t.Fatalf("name = %v", cfg["name"])
	}
	// password는 절대 노출되지 않아야 함.
	if _, ok := cfg["password"]; ok {
		t.Fatal("password must not be exposed")
	}
}

func TestUpdateConfig(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := postJSON(server, "/api/config", map[string]any{"main_width": 1280, "main_height": 720, "rotation": 90}, false)
	w := httptest.NewRecorder()
	server.handleConfig(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if cam.Config.MainWidth != 1280 || cam.Config.MainHeight != 720 {
		t.Fatalf("config not updated: %dx%d", cam.Config.MainWidth, cam.Config.MainHeight)
	}
	if cam.Config.Rotation != 90 {
		t.Fatalf("rotation = %d", cam.Config.Rotation)
	}
}

func TestUpdateConfigRejectsUnknown(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := postJSON(server, "/api/config", map[string]any{"nonexistent": 123, "onvif_port": 9999}, false)
	w := httptest.NewRecorder()
	server.handleConfig(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	rejected := resp["rejected"].([]any)
	if len(rejected) != 2 {
		t.Fatalf("rejected = %v, want 2", rejected)
	}
}

func TestUpdateConfigRejectsInvalid(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := postJSON(server, "/api/config", map[string]any{"main_width": 99999}, false)
	w := httptest.NewRecorder()
	server.handleConfig(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	rejected := resp["rejected"].([]any)
	if len(rejected) != 1 {
		t.Fatalf("rejected = %v, want 1", rejected)
	}
	if cam.Config.MainWidth == 99999 {
		t.Fatal("invalid width should not be applied")
	}
}

func TestCredentialsEndpoint(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	// 인증 비활성화 상태에서 첫 설정 (bootstrap).
	req := postJSON(server, "/api/credentials", map[string]any{"username": "newuser", "password": "newpass"}, false)
	w := httptest.NewRecorder()
	server.handleCredentials(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if !cam.Config.AuthEnabled() {
		t.Fatal("auth should be enabled")
	}
	// 응답에 password/username 없어야 함.
	body := w.Body.String()
	if strings.Contains(body, "newpass") || strings.Contains(body, "newuser") {
		t.Fatal("credentials must not be echoed back")
	}
}

func TestSnapshot(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	// 프레임 주입.
	cam.Stream(blankFrame(320, 240))

	req := getRequest(server, "/snapshot.jpg", false)
	w := httptest.NewRecorder()
	server.handleSnapshot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if ct != "image/jpeg" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if w.Body.Len() == 0 {
		t.Fatal("empty snapshot body")
	}
}

func TestSnapshotNoFrame(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/snapshot.jpg", false)
	w := httptest.NewRecorder()
	server.handleSnapshot(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}

func TestStaticTraversal(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()

	// traversal 시도들은 403/404여야 함.
	for _, p := range []string{
		"/static/../etc/passwd",
		"/static/%2e%2e/%2e%2e/etc/passwd",
		"/static/..%2f..%2fetc/passwd",
	} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		server.handleStatic(w, req)
		if w.Code == http.StatusOK {
			t.Fatalf("traversal %s returned 200", p)
		}
	}
}

func TestStaticValid(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := httptest.NewRequest(http.MethodGet, "/static/css/style.css", nil)
	w := httptest.NewRecorder()
	server.handleStatic(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "text/css") {
		t.Fatalf("Content-Type = %q", w.Header().Get("Content-Type"))
	}
}

func TestPTZStatus(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/api/ptz", false)
	w := httptest.NewRecorder()
	server.handlePTZ(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if _, ok := status["pan"]; !ok {
		t.Fatal("missing pan")
	}
}

func TestPTZMove(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := postJSON(server, "/api/ptz", map[string]any{"action": "move", "pan": 0.5, "tilt": 0, "zoom": 0}, false)
	w := httptest.NewRecorder()
	server.handlePTZ(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	// 연속 이동이면 moving=true.
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if !status["moving"].(bool) {
		t.Fatal("should be moving")
	}
}

func TestPTZStop(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	w := httptest.NewRecorder()
	req := postJSON(server, "/api/ptz", map[string]any{"action": "stop"}, false)
	server.handlePTZ(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var status map[string]any
	json.Unmarshal(w.Body.Bytes(), &status)
	if status["moving"].(bool) {
		t.Fatal("should not be moving after stop")
	}
}

func TestRecordingStatus(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/api/recording/status", false)
	w := httptest.NewRecorder()
	server.handleRecording(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestVideoStatus(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/api/video/status", false)
	w := httptest.NewRecorder()
	server.handleVideo(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestUnknownRoute(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	w := httptest.NewRecorder()
	server.handleRoot(w, req)
	// 루트 핸들러만 연결되므로 unknown은 index.html로 렌더링될 수 있으나,
	// 실제 mux에서는 404 처리. 여기서는 핸들러 동작만 확인.
	if w.Code == http.StatusInternalServerError {
		t.Fatal("unexpected 500")
	}
}

func TestWebUIHTML(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := getRequest(server, "/", false)
	w := httptest.NewRecorder()
	server.handleRoot(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "TestCam") {
		t.Fatal("camera name not injected")
	}
	if strings.Contains(body, "{{camera_name}}") {
		t.Fatal("template placeholder not replaced")
	}
}

func TestONVIFGetDeviceInformation(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()

	body := `<?xml version="1.0"?><soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope"><soap:Body><tds:GetDeviceInformation/></soap:Body></soap:Envelope>`
	req := httptest.NewRequest(http.MethodPost, "/onvif/device_service", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/soap+xml")
	req.Header.Set("SOAPAction", `"http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"`)
	w := httptest.NewRecorder()
	server.handleONVIF(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "GetDeviceInformationResponse") {
		t.Fatal("missing response element")
	}
}

func TestONVIFAuthRequired(t *testing.T) {
	server, cam := setupServer(t, true)
	defer cam.Stop()

	// WS-Security 토큰 없는 요청은 401.
	body := `<?xml version="1.0"?><soap:Envelope><soap:Body><tds:GetDeviceInformation/></soap:Body></soap:Envelope>`
	req := httptest.NewRequest(http.MethodPost, "/onvif/device_service", strings.NewReader(body))
	req.Header.Set("SOAPAction", `"http://www.onvif.org/ver10/device/wsdl/GetDeviceInformation"`)
	w := httptest.NewRecorder()
	server.handleONVIF(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestONVIFDetectActionFromBody(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()

	// SOAPAction 헤더 없이 body에서 액션 감지.
	body := `<?xml version="1.0"?><soap:Envelope><soap:Body><tds:GetSystemDateAndTime/></soap:Body></soap:Envelope>`
	req := httptest.NewRequest(http.MethodPost, "/onvif/device_service", strings.NewReader(body))
	w := httptest.NewRecorder()
	server.handleONVIF(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "GetSystemDateAndTimeResponse") {
		t.Fatal("missing response element")
	}
}

func TestConfigSaveAfterUpdate(t *testing.T) {
	server, cam := setupServer(t, false)
	defer cam.Stop()
	req := postJSON(server, "/api/config", map[string]any{"main_width": 640}, false)
	w := httptest.NewRecorder()
	server.handleConfig(w, req)
	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["saved"] != true {
		t.Fatalf("saved = %v", resp["saved"])
	}
}

func blankFrame(w, h int) *gocv.Mat {
	img := gocv.NewMatWithSize(h, w, gocv.MatTypeCV8UC3)
	return &img
}
