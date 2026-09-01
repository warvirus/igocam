// 카메라 관리 서버 — 카메라 목록·CRUD·서비스 제어·스냅샷 프록시
package admin

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"igocam/internal/config"
	"igocam/internal/manager"
)

// Server 관리 HTTP 서버.
type Server struct {
	port      int
	mgr       *manager.Manager
	http      *http.Server
	adminUser string
	adminPass string
}

// New 관리 서버 생성.
func New(port int, mgr *manager.Manager, adminUser, adminPass string) *Server {
	return &Server{
		port:      port,
		mgr:       mgr,
		adminUser: adminUser,
		adminPass: adminPass,
	}
}

// Start 서버 시작.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("/api/cameras", s.handleCameras)
	mux.HandleFunc("/api/cameras/", s.handleCameraByID)
	mux.HandleFunc("/api/reload", s.handleReload)
	mux.HandleFunc("/api/start-all", s.handleStartAll)
	mux.HandleFunc("/api/stop-all", s.handleStopAll)
	mux.HandleFunc("/api/pause-streams", s.handlePauseStreams)
	mux.HandleFunc("/api/resume-streams", s.handleResumeStreams)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/ports/available", s.handleAvailablePorts)

	s.http = &http.Server{
		Addr:              addr,
		Handler:           s.authMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go s.http.Serve(ln)
	return nil
}

// Stop 서버 정지.
func (s *Server) Stop() error {
	if s.http != nil {
		return s.http.Close()
	}
	return nil
}

// authMiddleware 선택적 Basic Auth 미들웨어.
// '/'와 '/api/auth/login'은 인증 없이 접근 가능 (로그인 화면 제공),
// 그 외 '/api/*'에만 인증 적용.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		isAuthEndpoint := r.URL.Path == "/api/auth/login"
		if s.adminUser != "" && isAPI && !isAuthEndpoint {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Basic ") {
				jsonResp(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
			if err != nil || string(decoded) != s.adminUser+":"+s.adminPass {
				jsonResp(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleAuthLogin POST /api/auth/login — Basic Auth 자격 증명 검증.
// body: {"username": "...", "password": "..."} 또는 Authorization header.
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResp(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	// admin 인증이 비활성화면 항상 성공.
	if s.adminUser == "" {
		jsonResp(w, http.StatusOK, map[string]string{"success": "logged_in"})
		return
	}
	// body에서 자격 증명 시도.
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil && body.Username != "" {
		if body.Username == s.adminUser && body.Password == s.adminPass {
			jsonResp(w, http.StatusOK, map[string]string{"success": "logged_in"})
			return
		}
		jsonResp(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	// Authorization header로 자격 증명 시도.
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Basic ") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
		if err == nil && string(decoded) == s.adminUser+":"+s.adminPass {
			jsonResp(w, http.StatusOK, map[string]string{"success": "logged_in"})
			return
		}
	}
	jsonResp(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
}

// handleAuthLogout POST /api/auth/logout — 클라이언트 측 로그아웃 (세션 없음, 확인용).
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonResp(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	jsonResp(w, http.StatusOK, map[string]string{"success": "logged_out"})
}

// json 응답 헬퍼.
func jsonResp(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// handleRoot 정적 페이지 (간단한 자체 포함 HTML).
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 브라우저 캐시 방지: HTML/JS 변경이 즉시 반영되도록 항상 최신본을 받는다.
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	io.WriteString(w, adminHTML)
}

// handleCameras GET/POST /api/cameras
func (s *Server) handleCameras(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listCameras(w, r)
	case http.MethodPost:
		s.addCamera(w, r)
	default:
		jsonResp(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

func (s *Server) listCameras(w http.ResponseWriter, _ *http.Request) {
	type camInfo struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Source        string `json:"source"`
		HWAccel       string `json:"hw_accel"`
		OnvifPort     int    `json:"onvif_port"`
		Status        string `json:"status"` // "running", "paused", "stopped"
		StreamingMode string `json:"streaming_mode"`
		SnapURL       string `json:"snapshot_url"`
		WebUIURL      string `json:"web_ui_url"`
	}
	var list []camInfo = []camInfo{} // JSON null 대신 [] 반환 (프론트엔드 .length 안전)
	for _, cam := range s.mgr.Cameras() {
		status := "running"
		if cam.Streamer.IsPaused() {
			status = "paused"
		}
		if !cam.IsRunning() {
			status = "stopped"
		}
		cfg := cam.Config
		list = append(list, camInfo{
			ID:            cfg.ID,
			Name:          cfg.Name,
			Source:        cfg.Source,
			HWAccel:       cfg.HWAccel,
			OnvifPort:     cfg.OnvifPort,
			Status:        status,
			StreamingMode: cam.StreamingMode(),
			SnapURL:       fmt.Sprintf("/api/cameras/%s/snapshot", cfg.ID),
			WebUIURL:      fmt.Sprintf("http://%s:%d/", cfg.LocalIP, cfg.OnvifPort),
		})
	}
	jsonResp(w, http.StatusOK, list)
}

func (s *Server) addCamera(w http.ResponseWriter, r *http.Request) {
	var cfg config.CameraConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	cfg.ConfigPath = s.mgr.ConfigPath()
	cam, err := s.mgr.AddCamera(&cfg)
	if err != nil {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	jsonResp(w, http.StatusCreated, map[string]any{
		"success": true, "id": cam.Config.ID, "name": cam.Config.Name,
		"onvif_port": cam.Config.OnvifPort,
	})
}

// handleCameraByID GET/PUT/DELETE /api/cameras/{id}
func (s *Server) handleCameraByID(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// 스냅샷 프록시 (/api/cameras/{id}/snapshot)
	if strings.HasSuffix(path, "/snapshot") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/cameras/"), "/snapshot")
		if id == "" || strings.Contains(id, "/") {
			jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid camera ID"})
			return
		}
		s.serveSnapshot(w, r, id)
		return
	}

	id := strings.TrimPrefix(path, "/api/cameras/")
	if id == "" || strings.Contains(id, "/") {
		jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid camera ID"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cam := s.mgr.CameraByID(id)
		if cam == nil {
			jsonResp(w, http.StatusNotFound, map[string]string{"error": "camera not found"})
			return
		}
		jsonResp(w, http.StatusOK, cam.Config)
		return
	case http.MethodPut:
		var updates map[string]any
		if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
			jsonResp(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.mgr.UpdateCamera(id, updates); err != nil {
			jsonResp(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonResp(w, http.StatusOK, map[string]string{"success": "updated"})
		return
	case http.MethodDelete:
		if err := s.mgr.RemoveCamera(id); err != nil {
			jsonResp(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		jsonResp(w, http.StatusOK, map[string]string{"success": "deleted"})
		return
	default:
		jsonResp(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	}
}

// serveSnapshot 카메라 스냅샷을 프록시한다.
func (s *Server) serveSnapshot(w http.ResponseWriter, _ *http.Request, id string) {
	cam := s.mgr.CameraByID(id)
	if cam == nil {
		http.NotFound(w, nil)
		return
	}
	port := cam.Config.OnvifPort
	snapURL := fmt.Sprintf("http://127.0.0.1:%d/snapshot.jpg", port)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(snapURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", "image/svg+xml")
		io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="320" height="180" fill="#333"/><text x="160" y="90" text-anchor="middle" fill="#888" font-size="14">No Snapshot</text></svg>`)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	io.Copy(w, resp.Body)
}

// handleReload POST /api/reload
func (s *Server) handleReload(w http.ResponseWriter, _ *http.Request) {
	added, updated, removed, err := s.mgr.ReloadFromConfig()
	if err != nil {
		jsonResp(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	jsonResp(w, http.StatusOK, map[string]any{
		"success": true, "added": added, "updated": updated, "removed": removed,
	})
}

// handleStartAll POST /api/start-all
func (s *Server) handleStartAll(w http.ResponseWriter, _ *http.Request) {
	s.mgr.StartAll()
	jsonResp(w, http.StatusOK, map[string]string{"success": "started"})
}

// handleStopAll POST /api/stop-all
func (s *Server) handleStopAll(w http.ResponseWriter, _ *http.Request) {
	s.mgr.StopAll()
	jsonResp(w, http.StatusOK, map[string]string{"success": "stopped"})
}

// handlePauseStreams POST /api/pause-streams
func (s *Server) handlePauseStreams(w http.ResponseWriter, _ *http.Request) {
	s.mgr.PauseStreams()
	jsonResp(w, http.StatusOK, map[string]string{"success": "paused"})
}

// handleResumeStreams POST /api/resume-streams
func (s *Server) handleResumeStreams(w http.ResponseWriter, _ *http.Request) {
	s.mgr.ResumeStreams()
	jsonResp(w, http.StatusOK, map[string]string{"success": "resumed"})
}

// handleStatus GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	type camStatus struct {
		ID     string  `json:"id"`
		Name   string  `json:"name"`
		Status string  `json:"status"` // "running", "paused"
		FPS    float64 `json:"fps"`
	}
	var list []camStatus = []camStatus{}
	for _, cam := range s.mgr.Cameras() {
		status := "running"
		if cam.Streamer.IsPaused() {
			status = "paused"
		}
		list = append(list, camStatus{
			ID:     cam.Config.ID,
			Name:   cam.Config.Name,
			Status: status,
			FPS:    cam.Streamer.Stats.ActualFPS(),
		})
	}
	jsonResp(w, http.StatusOK, list)
}

// handleAvailablePorts GET /api/ports/available
func (s *Server) handleAvailablePorts(w http.ResponseWriter, _ *http.Request) {
	ps := s.mgr.FindAvailablePorts()
	jsonResp(w, http.StatusOK, map[string]int{
		"onvif_port":      ps.OnvifPort,
		"rtsp_port":       ps.RTSPPort,
		"rtmp_port":       ps.RTMPPort,
		"go2rtc_api_port": ps.Go2rtcAPIPort,
		"web_port":        ps.WebPort,
		"webrtc_port":     ps.WebRTCPort,
	})
}
