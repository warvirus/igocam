// 카메라 설정 데이터 모델 - 로드/검증/원자적 저장/멀티 카메라
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// DefaultConfigPath 기본 설정 파일 경로.
const DefaultConfigPath = "camera_config.json"

// 버전 문자열은 내부에서 관리 (cmd/igocam에서 주입).
var Version = "1.3.0"

// EDITABLE_FIELDS 웹 UI / 클라이언트가 apply_updates()로 수정 가능한 필드.
// identity/network 필드(local_ip, firmware_version, serial_number, ports)는
// POST body로 덮어쓸 수 없도록 의도적으로 제외한다.
var EditableFields = map[string]bool{
	"main_width": true, "main_height": true, "main_fps": true, "main_bitrate": true,
	"main_keyframe_interval": true, "main_options": true,
	"main_stream_name": true, "sub_width": true, "sub_height": true, "sub_fps": true,
	"sub_bitrate": true, "sub_keyframe_interval": true, "sub_options": true,
	"sub_stream_name": true, "hw_accel": true, "bypass": true,
	"name": true, "manufacturer": true, "model": true,
	"show_timestamp": true, "timestamp_format": true, "timestamp_position": true,
	"flip": true, "mirror": true, "rotation": true,
	// recording_path는 의도적으로 제외: 파일시스템 위치이므로 웹 API 업데이트 경로를
	// 통한 path-traversal/임의 쓰기 방지. 설정 파일로만 설정한다.
	"recording_enabled": true, "recording_format": true,
	"recording_max_file_mb": true, "recording_pre_seconds": true,
}

// RESTART_FIELDS 이 필드가 변경되면 비디오 스트림 재시작이 필요하다.
// rotation은 90/270이 너비/높이를 바꿔 인코더 해상도에 영향을 주므로 항상 재시작.
// flip/mirror는 프레임 크기를 바꾸지 않아 재시작 불필요.
var RestartFields = map[string]bool{
	"main_width": true, "main_height": true, "main_fps": true, "main_bitrate": true,
	"main_keyframe_interval": true, "main_options": true,
	"sub_width": true, "sub_height": true, "sub_bitrate": true,
	"sub_keyframe_interval": true, "sub_options": true,
	"hw_accel": true, "bypass": true,
	"rotation": true,
}

// ValidHwAccel 유효한 하드웨어 가속 값.
var ValidHwAccel = map[string]bool{"auto": true, "nvenc": true, "qsv": true, "videotoolbox": true, "cpu": true}

// ValidTimestampPositions 유효한 타임스탬프 위치.
var ValidTimestampPositions = map[string]bool{
	"top-left": true, "top-right": true, "bottom-left": true, "bottom-right": true,
}

// ValidRotations 유효한 회전 각도.
var ValidRotations = map[int]bool{0: true, 90: true, 180: true, 270: true}

// ValidRecordingFormats 유효한 녹화 포맷.
var ValidRecordingFormats = map[string]bool{"mp4": true, "avi": true}

var bitrateRE = regexp.MustCompile(`^\d+[KMG]?$`)

// 범위 상수.
const (
	MaxRecordingPreSeconds = 30
	MinRecordingMaxMB      = 1
	MaxRecordingMaxMB      = 1024 * 1024
	MaxWidth               = 7680
	MaxHeight              = 4320
	MinFPS                 = 1
	MaxFPS                 = 120
)

// CameraConfig 카메라 완전 설정.
type CameraConfig struct {
	// Identity
	ID              string `json:"id"`
	Name            string `json:"name"`
	Manufacturer    string `json:"manufacturer"`
	Model           string `json:"model"`
	SerialNumber    string `json:"serial_number"`
	FirmwareVersion string `json:"firmware_version"`

	// Network
	LocalIP       string `json:"local_ip,omitempty"`
	OnvifPort     int    `json:"onvif_port"`
	RTSPPort      int    `json:"rtsp_port"`
	RTMPPort      int    `json:"rtmp_port"`
	WebPort       int    `json:"web_port"`
	Go2rtcAPIPort int    `json:"go2rtc_api_port"`
	WebRTCPort    int    `json:"webrtc_port"`

	// Authentication (선택). 둘 다 비어 있으면 인증 비활성.
	// EDITABLE_FIELDS에 의도적으로 없음: 전용 /api/credentials 경로로 변경.
	Username string `json:"username"`
	Password string `json:"password"`

	// Main stream
	MainWidth            int    `json:"main_width"`
	MainHeight           int    `json:"main_height"`
	MainFPS              int    `json:"main_fps"`
	MainBitrate          string `json:"main_bitrate"`
	MainStreamName       string `json:"main_stream_name"`
	MainKeyframeInterval int    `json:"main_keyframe_interval"`
	MainOptions          string `json:"main_options"`

	// Sub stream
	SubWidth            int    `json:"sub_width"`
	SubHeight           int    `json:"sub_height"`
	SubFPS              int    `json:"sub_fps"`
	SubBitrate          string `json:"sub_bitrate"`
	SubStreamName       string `json:"sub_stream_name"`
	SubKeyframeInterval int    `json:"sub_keyframe_interval"`
	SubOptions          string `json:"sub_options"`

	// MJPEG fallback
	MjpegURL    string `json:"mjpeg_url"`
	SnapshotURL string `json:"snapshot_url"`

	// HW accel
	HWAccel string `json:"hw_accel"`

	// Bypass: true면 소스가 이미 H.264(파일/RTSP)일 때 go2rtc가 소스를 직접
	// 읽어 트랜스코딩 없이 전송한다. 카메라 디바이스/업로드 모드에는 적용 불가.
	Bypass bool `json:"bypass"`

	// Overlay
	ShowTimestamp     bool   `json:"show_timestamp"`
	TimestampFormat   string `json:"timestamp_format"`
	TimestampPosition string `json:"timestamp_position"` // top-left, top-right, bottom-left, bottom-right

	// Display transforms (프레임 파이프라인에서 적용). 기본값은 모두 no-op.
	Flip     bool `json:"flip"`     // 세로 뒤집기
	Mirror   bool `json:"mirror"`   // 가로 뒤집기
	Rotation int  `json:"rotation"` // 시계방향 회전 (도): 0, 90, 180, 270

	// Recording
	RecordingEnabled   bool   `json:"recording_enabled"`
	RecordingFormat    string `json:"recording_format"`
	RecordingPath      string `json:"recording_path"`
	RecordingMaxFileMB int    `json:"recording_max_file_mb"`
	RecordingPreSec    int    `json:"recording_pre_seconds"`

	// Source info (UI 표시용)
	SourceType string `json:"source_type"`
	SourceInfo string `json:"source_info"`

	// Capture source: 디바이스 인덱스("0"), 비디오 파일 경로, URL.
	Source string `json:"source"`

	// 설정 파일 경로 (JSON으로 직렬화하지 않는 내부 필드).
	ConfigPath string `json:"-"`
}

// DefaultConfig 기본값으로 카메라 설정 생성.
func DefaultConfig() *CameraConfig {
	return &CameraConfig{
		Name:               "Virtual Camera",
		Manufacturer:       "GoCam",
		Model:              "VirtualCam-1",
		SerialNumber:       "GO-000001",
		FirmwareVersion:    Version,
		OnvifPort:          8080,
		RTSPPort:           8554,
		RTMPPort:           1935,
		WebPort:            8081,
		Go2rtcAPIPort:      1984,
		WebRTCPort:         8555,
		MainWidth:          1920,
		MainHeight:         1080,
		MainFPS:            30,
		MainBitrate:        "8M",
		MainStreamName:     "video_main",
		SubWidth:           640,
		SubHeight:          360,
		SubFPS:             30,
		SubBitrate:         "1M",
		SubStreamName:      "video_sub",
		MjpegURL:           "stream.mjpeg",
		SnapshotURL:        "snapshot.jpg",
		HWAccel:            "auto",
		ShowTimestamp:      true,
		TimestampFormat:    "%Y-%m-%d %H:%M:%S",
		TimestampPosition:  "bottom-left",
		RecordingFormat:    "mp4",
		RecordingPath:      "recordings",
		RecordingMaxFileMB: 1024,
		SourceType:         "unknown",
		Source:             "0",
	}
}

// GetLocalIP 로컬 IP 주소 탐색.
func GetLocalIP() string {
	conn, err := net.Dial("udp", "10.255.255.255:1")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return addr.IP.String()
	}
	return "127.0.0.1"
}

// AuthEnabled 사용자 이름과 비밀번호가 모두 설정됐는지 여부.
func (c *CameraConfig) AuthEnabled() bool {
	return c.Username != "" && c.Password != ""
}

// MainStreamRTMP 메인 스트림 RTMP push URL.
func (c *CameraConfig) MainStreamRTMP() string {
	return fmt.Sprintf("rtmp://127.0.0.1:%d/%s", c.RTMPPort, c.MainStreamName)
}

// SubStreamRTMP 서브 스트림 RTMP push URL.
func (c *CameraConfig) SubStreamRTMP() string {
	return fmt.Sprintf("rtmp://127.0.0.1:%d/%s", c.RTMPPort, c.SubStreamName)
}

// MainStreamRTSP 메인 스트림 RTSP 재생 URL.
func (c *CameraConfig) MainStreamRTSP() string {
	ip := c.LocalIP
	if ip == "" {
		ip = GetLocalIP()
	}
	return fmt.Sprintf("rtsp://%s:%d/%s", ip, c.RTSPPort, c.MainStreamName)
}

// SubStreamRTSP 서브 스트림 RTSP 재생 URL.
func (c *CameraConfig) SubStreamRTSP() string {
	ip := c.LocalIP
	if ip == "" {
		ip = GetLocalIP()
	}
	return fmt.Sprintf("rtsp://%s:%d/%s", ip, c.RTSPPort, c.SubStreamName)
}

// OnvifURL ONVIF device service URL.
func (c *CameraConfig) OnvifURL() string {
	ip := c.LocalIP
	if ip == "" {
		ip = GetLocalIP()
	}
	return fmt.Sprintf("http://%s:%d/onvif/device_service", ip, c.OnvifPort)
}

// WebRTCURL go2rtc API URL.
func (c *CameraConfig) WebRTCURL() string {
	ip := c.LocalIP
	if ip == "" {
		ip = GetLocalIP()
	}
	return fmt.Sprintf("http://%s:%d", ip, c.Go2rtcAPIPort)
}

// SetCredentials 사용자 이름/비밀번호 검증 및 설정.
// 둘 다 빈 경우 인증 해제. 한쪽만 있으면 거부.
func (c *CameraConfig) SetCredentials(username, password string) (bool, string) {
	u := strings.TrimSpace(username)
	p := password
	if u == "" && p == "" {
		c.Username = ""
		c.Password = ""
		return true, ""
	}
	if u == "" {
		return false, "Username is required"
	}
	if p == "" {
		return false, "Password is required"
	}
	c.Username = u
	c.Password = p
	return true, ""
}

// CoerceBool 일반적인 truthy/falsy 표현을 bool로 변환. (ok, value)
func CoerceBool(v any) (bool, bool) {
	switch val := v.(type) {
	case bool:
		return true, val
	case float64:
		return true, val != 0
	case int:
		return true, val != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(val))
		switch s {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return true, false
		}
	}
	return false, false
}

// ValidateUpdate 단일 편집 가능 필드 검증. (ok, 변환된 값)
func (c *CameraConfig) ValidateUpdate(key string, value any) (bool, any) {
	switch key {
	case "main_width", "sub_width":
		v, err := toInt(value)
		return err == nil && v >= 1 && v <= MaxWidth, v
	case "main_height", "sub_height":
		v, err := toInt(value)
		return err == nil && v >= 1 && v <= MaxHeight, v
	case "main_fps", "sub_fps":
		v, err := toInt(value)
		return err == nil && v >= MinFPS && v <= MaxFPS, v
	case "main_keyframe_interval", "sub_keyframe_interval":
		v, err := toInt(value)
		return err == nil && v >= 0, v
	case "main_bitrate", "sub_bitrate":
		v := strings.TrimSpace(fmt.Sprint(value))
		return bitrateRE.MatchString(v), v
	case "main_options", "sub_options":
		v := fmt.Sprint(value)
		return true, v
	case "hw_accel":
		v := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		return ValidHwAccel[v], v
	case "timestamp_position":
		v := strings.TrimSpace(fmt.Sprint(value))
		return ValidTimestampPositions[v], v
	case "show_timestamp", "flip", "mirror", "recording_enabled", "bypass":
		return CoerceBool(value)
	case "rotation":
		v, err := toInt(value)
		return err == nil && ValidRotations[v], v
	case "recording_format":
		v := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
		return ValidRecordingFormats[v], v
	case "recording_max_file_mb":
		v, err := toInt(value)
		return err == nil && v >= MinRecordingMaxMB && v <= MaxRecordingMaxMB, v
	case "recording_pre_seconds":
		v, err := toInt(value)
		return err == nil && v >= 0 && v <= MaxRecordingPreSeconds, v
	case "main_stream_name", "sub_stream_name":
		v := strings.TrimSpace(fmt.Sprint(value))
		return v != "", v
	case "name", "manufacturer", "model", "timestamp_format":
		v := fmt.Sprint(value)
		return strings.TrimSpace(v) != "", v
	}
	return false, nil
}

// ApplyUpdates 업데이트 맵을 검증/적용. (applied, rejected, restartKeys)
func (c *CameraConfig) ApplyUpdates(updates map[string]any) ([]string, []string, []string) {
	var applied, rejected, restartKeys []string
	for key, value := range updates {
		if !EditableFields[key] {
			rejected = append(rejected, key)
			continue
		}
		ok, coerced := c.ValidateUpdate(key, value)
		if !ok {
			rejected = append(rejected, key)
			continue
		}
		applied = append(applied, key)
		if applyField(c, key, coerced) {
			if RestartFields[key] {
				restartKeys = append(restartKeys, key)
			}
		}
	}
	return applied, rejected, restartKeys
}

func applyField(c *CameraConfig, key string, value any) bool {
	switch key {
	case "main_width":
		return c.setInt(&c.MainWidth, value)
	case "main_height":
		return c.setInt(&c.MainHeight, value)
	case "main_fps":
		return c.setInt(&c.MainFPS, value)
	case "main_bitrate":
		return c.setStr(&c.MainBitrate, value)
	case "main_keyframe_interval":
		return c.setInt(&c.MainKeyframeInterval, value)
	case "main_options":
		return c.setStr(&c.MainOptions, value)
	case "main_stream_name":
		return c.setStr(&c.MainStreamName, value)
	case "sub_width":
		return c.setInt(&c.SubWidth, value)
	case "sub_height":
		return c.setInt(&c.SubHeight, value)
	case "sub_fps":
		return c.setInt(&c.SubFPS, value)
	case "sub_bitrate":
		return c.setStr(&c.SubBitrate, value)
	case "sub_keyframe_interval":
		return c.setInt(&c.SubKeyframeInterval, value)
	case "sub_options":
		return c.setStr(&c.SubOptions, value)
	case "sub_stream_name":
		return c.setStr(&c.SubStreamName, value)
	case "hw_accel":
		return c.setStr(&c.HWAccel, value)
	case "name":
		return c.setStr(&c.Name, value)
	case "manufacturer":
		return c.setStr(&c.Manufacturer, value)
	case "model":
		return c.setStr(&c.Model, value)
	case "show_timestamp":
		return c.setBool(&c.ShowTimestamp, value)
	case "timestamp_format":
		return c.setStr(&c.TimestampFormat, value)
	case "timestamp_position":
		return c.setStr(&c.TimestampPosition, value)
	case "flip":
		return c.setBool(&c.Flip, value)
	case "mirror":
		return c.setBool(&c.Mirror, value)
	case "rotation":
		return c.setInt(&c.Rotation, value)
	case "recording_enabled":
		return c.setBool(&c.RecordingEnabled, value)
	case "bypass":
		return c.setBool(&c.Bypass, value)
	case "recording_format":
		return c.setStr(&c.RecordingFormat, value)
	case "recording_max_file_mb":
		return c.setInt(&c.RecordingMaxFileMB, value)
	case "recording_pre_seconds":
		return c.setInt(&c.RecordingPreSec, value)
	}
	return false
}

func (c *CameraConfig) setInt(field *int, value any) bool {
	v, err := toInt(value)
	if err != nil {
		return false
	}
	if *field != v {
		*field = v
		return true
	}
	return false
}

func (c *CameraConfig) setStr(field *string, value any) bool {
	v := fmt.Sprint(value)
	if *field != v {
		*field = v
		return true
	}
	return false
}

func (c *CameraConfig) setBool(field *bool, value any) bool {
	_, v := CoerceBool(value)
	if *field != v {
		*field = v
		return true
	}
	return false
}

func toInt(value any) (int, error) {
	switch v := value.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case string:
		return strconv.Atoi(strings.TrimSpace(v))
	}
	return 0, fmt.Errorf("invalid int: %v", value)
}

// Save 설정을 JSON 파일로 원자적으로 저장.
// 임시 파일에 쓴 뒤 os.Rename으로 교체해 크래시 중단에도 설정이 손상되지 않는다.
func (c *CameraConfig) Save(filePath string) error {
	if filePath == "" {
		filePath = c.ConfigPath
	}
	if filePath == "" {
		filePath = DefaultConfigPath
	}
	type exportConfig CameraConfig
	cfg := exportConfig(*c)
	cfg.LocalIP = "" // local_ip는 자동 감지이므로 저장하지 않는다.

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".camera_config_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filePath)
}

// GenerateID 고유 카메라 ID 생성 (cam_ + 8바이트 랜덤 hex).
func GenerateID() string {
	const charset = "0123456789abcdef"
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return fmt.Sprintf("cam_%x", b)
		}
		b[i] = charset[n.Int64()]
	}
	return "cam_" + string(b)
}

// EnsureID ID가 없으면 생성하여 설정한다.
func (c *CameraConfig) EnsureID() {
	if c.ID == "" {
		c.ID = GenerateID()
	}
}

// SaveAll config 배열 전체를 JSON 파일로 원자적으로 저장한다.
func SaveAll(filePath string, configs []*CameraConfig) error {
	if filePath == "" {
		filePath = DefaultConfigPath
	}
	type exportConfig CameraConfig
	exported := make([]exportConfig, len(configs))
	for i, cfg := range configs {
		exported[i] = exportConfig(*cfg)
		exported[i].LocalIP = ""
	}
	data, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".camera_config_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filePath)
}

// Load 설정 파일에서 로드. 없으면 기본값 반환.
// 반환된 config는 저장 시 같은 파일로 되돌아가도록 filePath를 기억한다.
func Load(filePath string) *CameraConfig {
	if filePath == "" {
		filePath = DefaultConfigPath
	}
	cfg := DefaultConfig()
	cfg.ConfigPath = filePath

	data, err := os.ReadFile(filePath)
	if err != nil {
		return cfg
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return cfg
	}
	if cfg.LocalIP == "" {
		cfg.LocalIP = GetLocalIP()
	}
	return cfg
}

// LoadAll JSON 배열 형식의 멀티 카메라 설정 로드.
func LoadAll(filePath string) ([]*CameraConfig, error) {
	if filePath == "" {
		filePath = DefaultConfigPath
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, errors.New(filePath + " must be a JSON array of camera objects")
	}
	if len(raw) == 0 {
		return nil, errors.New(filePath + " contains no cameras")
	}

	var configs []*CameraConfig
	for i, entry := range raw {
		entryJSON, err := json.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("camera #%d in %s: %w", i, filePath, err)
		}
		cfg := DefaultConfig()
		if err := json.Unmarshal(entryJSON, cfg); err != nil {
			return nil, fmt.Errorf("camera #%d in %s: %w", i, filePath, err)
		}
		cfg.ConfigPath = filePath
		cfg.EnsureID()
		if cfg.LocalIP == "" {
			cfg.LocalIP = GetLocalIP()
		}
		configs = append(configs, cfg)
	}
	return configs, nil
}

// UniquePortFields 각 카메라가 배타적으로 바인딩하는 포트 필드.
var UniquePortFields = []string{"onvif_port", "rtsp_port", "rtmp_port", "go2rtc_api_port", "web_port", "webrtc_port"}

// ValidateCameraConfigs 설정 집합이 한 프로세스에서 함께 실행 가능한지 검증.
// 빈 집합과 포트 충돌을 거부한다.
func ValidateCameraConfigs(configs []*CameraConfig) error {
	if len(configs) == 0 {
		return errors.New("no cameras to start")
	}
	var collisions []string
	for _, field := range UniquePortFields {
		seen := map[int]string{}
		for _, cfg := range configs {
			port := configFieldInt(cfg, field)
			if name, ok := seen[port]; ok {
				collisions = append(collisions,
					fmt.Sprintf("%s %d used by both '%s' and '%s'", field, port, name, cfg.Name))
			} else {
				seen[port] = cfg.Name
			}
		}
	}
	if len(collisions) > 0 {
		return errors.New("camera port conflict: " + strings.Join(collisions, "; "))
	}
	return nil
}

func configFieldInt(c *CameraConfig, field string) int {
	switch field {
	case "onvif_port":
		return c.OnvifPort
	case "rtsp_port":
		return c.RTSPPort
	case "rtmp_port":
		return c.RTMPPort
	case "go2rtc_api_port":
		return c.Go2rtcAPIPort
	case "web_port":
		return c.WebPort
	case "webrtc_port":
		return c.WebRTCPort
	}
	return 0
}
