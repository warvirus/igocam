// HTTP 서버 - Web UI + REST API + ONVIF 엔드포인트
package httpserver

import (
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gocv.io/x/gocv"

	"igocam/internal/camera"
	"igocam/internal/config"
)

const maxUploadBytes = 2000 * 1024 * 1024 // 2GB

//go:embed templates/index.html
var webUIHTML string

//go:embed static
var staticFS embed.FS

// Server HTTP 서버.
type Server struct {
	camera *camera.Camera
	config *config.CameraConfig
	http   *http.Server
	listen string
}

// New HTTP 서버 생성.
func New(cam *camera.Camera) *Server {
	return &Server{
		camera: cam,
		config: cam.Config,
	}
}

// Start 서버 시작.
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.config.OnvifPort)
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/index.html", s.handleRoot)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/ptz", s.handlePTZ)
	mux.HandleFunc("/api/recording/", s.handleRecording)
	mux.HandleFunc("/api/credentials", s.handleCredentials)
	mux.HandleFunc("/api/restart", s.handleRestart)
	mux.HandleFunc("/api/video/", s.handleVideo)
	mux.HandleFunc("/onvif/", s.handleONVIF)
	mux.HandleFunc("/static/", s.handleStatic)
	mux.HandleFunc("/snapshot.jpg", s.handleSnapshot)
	mux.HandleFunc("/stream.mjpeg", s.handleMJPEG)

	s.http = &http.Server{
		Addr:    addr,
		Handler: mux,
		// 좀비 keep-alive 연결 방지. MJPEG 스트리밍은 지속적으로 응답을 쓰므로
		// WriteTimeout을 설정하지 않아도 IdleTimeout에 걸리지 않는다.
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

// basicAuth HTTP Basic 인증 (상수 시간 비교). 인증 비활성화 시 항상 true.
func (s *Server) basicAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.config.AuthEnabled() {
		return true
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Basic ") {
		s.unauthorized(w)
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth[6:]))
	if err != nil {
		s.unauthorized(w)
		return false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		s.unauthorized(w)
		return false
	}
	user := parts[0]
	pw := parts[1]
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.config.Username))
	pwOK := subtle.ConstantTimeCompare([]byte(pw), []byte(s.config.Password))
	if userOK != 1 || pwOK != 1 {
		s.unauthorized(w)
		return false
	}
	return true
}

func (s *Server) unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="iGoCam"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

func (s *Server) jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"success": false, "error": msg})
}

func (s *Server) jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// handleRoot
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !s.basicAuth(w, r) {
		return
	}
	html := s.renderWebUI()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// renderWebUI
func (s *Server) renderWebUI() string {
	ip := s.config.LocalIP
	if ip == "" {
		ip = config.GetLocalIP()
	}
	previewURL := fmt.Sprintf("http://%s:%d/stream.html?src=%s", ip, s.config.Go2rtcAPIPort, s.config.MainStreamName)
	mjpegURL := fmt.Sprintf("http://%s:%d/%s", ip, s.config.WebPort, s.config.MjpegURL)

	repl := map[string]string{
		"{{camera_name}}":       s.config.Name,
		"{{preview_url}}":       previewURL,
		"{{main_rtsp}}":         s.config.MainStreamRTSP(),
		"{{sub_rtsp}}":          s.config.SubStreamRTSP(),
		"{{onvif_url}}":         s.config.OnvifURL(),
		"{{webrtc_url}}":        s.config.WebRTCURL(),
		"{{mjpeg_url}}":         mjpegURL,
		"{{main_stream_name}}":  s.config.MainStreamName,
		"{{sub_stream_name}}":   s.config.SubStreamName,
		"{{source_icon}}":       "❓",
		"{{source_type_label}}": "Unknown Source",
		"{{source_info}}":       s.config.SourceInfo,
		"{{version}}":           config.Version,
	}
	html := webUIHTML
	for k, v := range repl {
		html = strings.ReplaceAll(html, k, htmlEscape(v))
	}
	return html
}

func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// handleConfig
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.basicAuth(w, r) {
			return
		}
		s.serveConfig(w)
	case http.MethodPost:
		if !s.basicAuth(w, r) {
			return
		}
		s.updateConfig(w, r)
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

func (s *Server) serveConfig(w http.ResponseWriter) {
	ip := s.config.LocalIP
	if ip == "" {
		ip = config.GetLocalIP()
	}
	cfg := map[string]any{
		"name": s.config.Name, "manufacturer": s.config.Manufacturer,
		"model": s.config.Model, "serial_number": s.config.SerialNumber,
		"firmware_version": s.config.FirmwareVersion,
		"onvif_port":       s.config.OnvifPort, "rtsp_port": s.config.RTSPPort,
		"rtmp_port": s.config.RTMPPort, "web_port": s.config.WebPort,
		"go2rtc_api_port": s.config.Go2rtcAPIPort,
		"auth_enabled":    s.config.AuthEnabled(),
		"main_width":      s.config.MainWidth, "main_height": s.config.MainHeight,
		"main_fps": s.config.MainFPS, "main_bitrate": s.config.MainBitrate,
		"main_keyframe_interval": s.config.MainKeyframeInterval,
		"main_options":           s.config.MainOptions,
		"main_stream_name":       s.config.MainStreamName,
		"sub_width":              s.config.SubWidth, "sub_height": s.config.SubHeight,
		"sub_fps": s.config.SubFPS, "sub_bitrate": s.config.SubBitrate,
		"sub_keyframe_interval": s.config.SubKeyframeInterval,
		"sub_options":           s.config.SubOptions,
		"sub_stream_name":       s.config.SubStreamName,
		"hw_accel":              s.config.HWAccel,
		"bypass":                s.config.Bypass,
		"show_timestamp":        s.config.ShowTimestamp,
		"timestamp_format":      s.config.TimestampFormat,
		"timestamp_position":    s.config.TimestampPosition,
		"flip":                  s.config.Flip, "mirror": s.config.Mirror, "rotation": s.config.Rotation,
		"recording_enabled":     s.config.RecordingEnabled,
		"recording_format":      s.config.RecordingFormat,
		"recording_path":        s.config.RecordingPath,
		"recording_max_file_mb": s.config.RecordingMaxFileMB,
		"recording_pre_seconds": s.config.RecordingPreSec,
		"source_type":           s.config.SourceType, "source_info": s.config.SourceInfo,
		"main_stream_rtsp":  s.config.MainStreamRTSP(),
		"sub_stream_rtsp":   s.config.SubStreamRTSP(),
		"webrtc_url":        s.config.WebRTCURL(),
		"mjpeg_url":         fmt.Sprintf("http://%s:%d/%s", ip, s.config.WebPort, s.config.MjpegURL),
		"streaming_mode":    "go2rtc",
		"video_upload_mode": s.camera.VideoUploadModeValue(),
		"current_video":     filepath.Base(s.camera.GetCurrentVideoPath()),
		"video_error":       s.camera.GetVideoError(),
	}
	s.jsonOK(w, cfg)
}

func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
	var updates map[string]any
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		s.jsonError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	applied, rejected, restartKeys := s.config.ApplyUpdates(updates)
	restartNeeded := len(restartKeys) > 0

	// recording_* 변경은 스트림 재시작 없이 레코더에 직접 반영.
	for _, k := range applied {
		if strings.HasPrefix(k, "recording_") {
			s.camera.ApplyRecordingConfig()
			break
		}
	}

	restarted := false
	if restartNeeded && s.camera.IsRunning() {
		restarted = s.camera.RestartStream()
	}
	saved := s.config.Save("") == nil

	s.jsonOK(w, map[string]any{
		"success": true, "applied": applied, "rejected": rejected,
		"restart_needed": restartNeeded, "restarted": restarted, "saved": saved,
	})
}

// handleCredentials
func (s *Server) handleCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !s.basicAuth(w, r) {
		return
	}
	var data struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		s.jsonError(w, http.StatusBadRequest, "Invalid request")
		return
	}
	ok, errMsg := s.config.SetCredentials(data.Username, data.Password)
	if !ok {
		s.jsonError(w, http.StatusBadRequest, errMsg)
		return
	}
	saved := s.config.Save("") == nil
	s.jsonOK(w, map[string]any{
		"success": true, "auth_enabled": s.config.AuthEnabled(), "saved": saved,
	})
}

// handleStats
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !s.basicAuth(w, r) {
		return
	}
	mjpeg := s.camera.MJPEG
	stats := map[string]any{
		"streaming_mode":     "go2rtc",
		"is_streaming":       true,
		"mjpeg_frames_sent":  mjpeg.FramesSent(),
		"mjpeg_fps":          round1(mjpeg.ActualFPS()),
		"mjpeg_elapsed_time": round1(mjpeg.ElapsedTime()),
		"mjpeg_clients":      mjpeg.ClientCount(),
		"frames_sent":        mjpeg.FramesSent(),
		"actual_fps":         round1(mjpeg.ActualFPS()),
		"elapsed_time":       round1(mjpeg.ElapsedTime()),
		"dropped_frames":     mjpeg.FramesDropped(),
	}
	rec := s.camera.RecordingStats()
	stats["recording"] = map[string]any{
		"recording":      rec["recording"],
		"file":           filepath.Base(fmt.Sprint(rec["file"])),
		"segments":       rec["segments"],
		"frames_written": rec["frames_written"],
		"dropped":        rec["dropped"],
		"bytes":          rec["bytes"],
	}
	s.jsonOK(w, stats)
}

// handlePTZ
func (s *Server) handlePTZ(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !s.basicAuth(w, r) {
			return
		}
		s.jsonOK(w, s.camera.PTZ.GetStatus())
	case http.MethodPost:
		if !s.basicAuth(w, r) {
			return
		}
		var data struct {
			Action string  `json:"action"`
			Pan    float64 `json:"pan"`
			Tilt   float64 `json:"tilt"`
			Zoom   float64 `json:"zoom"`
			Delta  float64 `json:"delta"`
			Value  float64 `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			s.jsonError(w, http.StatusBadRequest, "Invalid request")
			return
		}
		ptz := s.camera.PTZ
		switch data.Action {
		case "zoom":
			ptz.RelativeMove(0, 0, data.Delta)
		case "zoom_to":
			ptz.AbsoluteMove(nil, nil, &data.Value)
		case "home":
			ptz.GotoHome()
		case "move":
			ptz.ContinuousMove(data.Pan, data.Tilt, data.Zoom)
		case "stop":
			ptz.StopMovement(true, true)
		}
		s.jsonOK(w, ptz.GetStatus())
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

// handleRecording
func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	if !s.basicAuth(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status"):
		stats := s.camera.RecordingStats()
		s.jsonOK(w, stats)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start"):
		ok := s.camera.StartRecording()
		stats := s.camera.RecordingStats()
		if ok {
			s.jsonOK(w, map[string]any{"success": true, "recording": stats["recording"], "file": stats["file"]})
		} else {
			s.jsonError(w, http.StatusInternalServerError, "Failed to start recording")
		}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/stop"):
		files := s.camera.StopRecording()
		s.jsonOK(w, map[string]any{"success": true, "recording": s.camera.IsRecording(), "files": files})
	default:
		http.Error(w, "", http.StatusMethodNotAllowed)
	}
}

// handleRestart
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !s.basicAuth(w, r) {
		return
	}
	ok := s.camera.RestartStream()
	s.jsonOK(w, map[string]any{"success": ok})
}

// handleVideo
func (s *Server) handleVideo(w http.ResponseWriter, r *http.Request) {
	if !s.basicAuth(w, r) {
		return
	}
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/status"):
		s.jsonOK(w, map[string]any{
			"video_upload_mode":  s.camera.VideoUploadModeValue(),
			"current_video":      filepath.Base(s.camera.GetCurrentVideoPath()),
			"current_video_path": s.camera.GetCurrentVideoPath(),
			"video_error":        s.camera.GetVideoError(),
			"source_type":        s.config.SourceType,
			"source_info":        s.config.SourceInfo,
		})
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/upload"):
		s.handleVideoUpload(w, r)
	}
}

func (s *Server) handleVideoUpload(w http.ResponseWriter, r *http.Request) {
	if !s.camera.VideoUploadModeValue() {
		s.jsonError(w, http.StatusBadRequest, "Video upload mode is not enabled")
		return
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		s.jsonError(w, http.StatusBadRequest, "Expected multipart/form-data")
		return
	}
	cl := r.Header.Get("Content-Length")
	size, err := strconv.ParseInt(cl, 10, 64)
	if err != nil || size > maxUploadBytes {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false, "error": fmt.Sprintf("Upload too large (max %d MB)", maxUploadBytes/(1024*1024)),
		})
		return
	}

	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		s.jsonError(w, http.StatusBadRequest, "Failed to parse multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.jsonError(w, http.StatusBadRequest, "No video file found in upload")
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	videoExts := map[string]bool{".mp4": true, ".avi": true, ".mkv": true, ".mov": true,
		".wmv": true, ".flv": true, ".webm": true, ".m4v": true, ".mpeg": true, ".mpg": true, ".3gp": true}
	if !videoExts[ext] {
		s.jsonError(w, http.StatusBadRequest, fmt.Sprintf("Invalid video format: %s", ext))
		return
	}

	videosDir := filepath.Join(".", "videos")
	os.MkdirAll(videosDir, 0o755)
	safeName := fmt.Sprintf("%d_%s", time.Now().Unix(), sanitizeFilename(header.Filename))
	filePath := filepath.Join(videosDir, safeName)

	dst, err := os.Create(filePath)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "Failed to save video")
		return
	}
	defer dst.Close()
	written, _ := io.Copy(dst, file)

	previous := s.camera.GetCurrentVideoPath()
	s.camera.SetCurrentVideoPath(filePath)

	s.jsonOK(w, map[string]any{
		"success": true, "filename": safeName, "size": written,
		"path": filePath, "previous_video": filepath.Base(previous),
	})
}

// handleSnapshot
func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !s.basicAuth(w, r) {
		return
	}
	frame := s.camera.GetSnapshotFrame()
	if frame == nil {
		http.Error(w, "No frame available", http.StatusServiceUnavailable)
		return
	}
	defer frame.Close()
	buf, err := gocv.IMEncodeWithParams(gocv.JPEGFileExt, *frame, []int{gocv.IMWriteJpegQuality, 90})
	if err != nil || buf == nil {
		http.Error(w, "Failed to encode snapshot", http.StatusInternalServerError)
		return
	}
	defer buf.Close()
	jpeg := buf.GetBytes()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(jpeg)))
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(jpeg)
}

// handleMJPEG
func (s *Server) handleMJPEG(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	if !s.basicAuth(w, r) {
		return
	}
	stream := r.URL.Query().Get("stream")
	if stream != "sub" {
		stream = "main"
	}
	mjpeg := s.camera.MJPEG
	if mjpeg == nil {
		http.Error(w, "MJPEG not available", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	for k, v := range mjpeg.Headers() {
		w.Header()[k] = v
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	client := mjpeg.AddClient(func(data []byte) error {
		_, err := w.Write(data)
		if err == nil {
			flusher.Flush()
		}
		return err
	}, stream)
	mjpeg.ServeClient(client)
}

// handleStatic
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	serveStaticFile(w, r)
}

// handleONVIF ONVIF SOAP 요청 처리.
func (s *Server) handleONVIF(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "", http.StatusMethodNotAllowed)
		return
	}
	onvifSvc := s.camera.Onvif
	if onvifSvc == nil {
		http.Error(w, "ONVIF not available", http.StatusInternalServerError)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		s.jsonError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	body := string(bodyBytes)

	soapAction := strings.Trim(r.Header.Get("SOAPAction"), `"`)

	// WS-Security UsernameToken 인증 (인증 비활성화 시 no-op).
	if !onvifSvc.VerifyUsernameToken(body) {
		// SOAP 1.2 규격: 클라이언트 측 오류(인증 실패)는 s:Sender 사용.
		fault := onvifSvc.FaultWithCode("s:Sender", "Sender not authorized")
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(fault))
		return
	}

	// 헤더에 SOAPAction이 없으면 body에서 액션 감지.
	if soapAction == "" {
		for _, action := range []string{
			"GetDeviceInformation", "GetSystemDateAndTime", "GetCapabilities",
			"GetServices", "GetServiceCapabilities", "GetProfiles", "GetStreamUri",
			"GetSnapshotUri", "GetVideoEncoderConfiguration", "GetVideoSourceConfiguration",
			"GetAudioDecoderConfigurations", "GetScopes", "GetUsers",
			"GetNodes", "GetNode", "GetConfigurations", "GetConfiguration",
			"GetStatus", "ContinuousMove", "Stop", "AbsoluteMove", "RelativeMove",
			"GotoHomePosition", "GetPresets", "SetPreset", "GotoPreset",
		} {
			if strings.Contains(body, action) {
				soapAction = action
				break
			}
		}
	}

	// SOAPAction 헤더는 전체 URI (예: http://.../GetDeviceInformation)일 수 있으므로
	// 마지막 경로 세그먼트로 액션 이름을 추출한다.
	if strings.Contains(soapAction, "/") {
		idx := strings.LastIndex(soapAction, "/")
		if idx >= 0 && idx+1 < len(soapAction) {
			soapAction = soapAction[idx+1:]
		}
	}

	response := onvifSvc.HandleAction(soapAction, body)
	if response != "" {
		w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	} else {
		http.Error(w, "Not Implemented", http.StatusNotImplemented)
	}
}

// serveStaticFile static 파일 서빙 (traversal 방어).
func serveStaticFile(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// Strip /static/ prefix
	requested := strings.TrimPrefix(path, "/static/")
	if requested == "" || requested[0] == '/' || filepath.IsAbs(requested) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	// 경로 정규화 및 URL 디코딩 (percent-encoded traversal 방어).
	requested = strings.ReplaceAll(requested, "\\", "/")
	clean := pathClean(requested)
	if strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	data, err := staticFS.ReadFile("static/" + clean)
	if err != nil {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	ext := strings.ToLower(filepath.Ext(clean))
	contentTypes := map[string]string{
		".css": "text/css", ".js": "application/javascript",
		".html": "text/html", ".png": "image/png",
		".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".ico": "image/x-icon", ".xml": "application/xml",
		".svg": "image/svg+xml", ".json": "application/json",
	}
	ct := contentTypes[ext]
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

// pathClean URL 경로를 안전하게 정규화한다.
func pathClean(p string) string {
	parts := strings.Split(p, "/")
	var out []string
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, part)
		}
	}
	return strings.Join(out, "/")
}

// util
func round1(v float64) float64 {
	return float64(int(v*10)) / 10
}

var unsafeRE = regexp.MustCompile(`[^\w\-_\.]`)

func sanitizeFilename(name string) string {
	return unsafeRE.ReplaceAllString(name, "_")
}
