// ONVIF SOAP 서비스 - Device/Media/PTZ 액션 핸들러 + WS-Security 인증
package onvif

import (
	"crypto/sha1"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"igocam/internal/config"
	"igocam/internal/ptz"
)

const maxSkewSeconds = 300

//go:embed soap/*.xml
var soapFS embed.FS

// Service ONVIF Device/Media/PTZ 서비스 핸들러.
type Service struct {
	config     *config.CameraConfig
	ptz        *ptz.Controller
	deviceUUID string
	templates  map[string]string
}

// New ONVIF 서비스 생성.
func New(cfg *config.CameraConfig, ptzCtrl *ptz.Controller) *Service {
	s := &Service{
		config:     cfg,
		ptz:        ptzCtrl,
		deviceUUID: fmt.Sprintf("urn:uuid:%d", time.Now().UnixNano()),
		templates:  make(map[string]string),
	}
	s.loadTemplates()
	return s
}

// loadTemplates go:embed된 SOAP 템플릿을 로드한다.
func (s *Service) loadTemplates() {
	entries, err := soapFS.ReadDir("soap")
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if len(name) < 4 || name[len(name)-4:] != ".xml" {
			continue
		}
		data, err := soapFS.ReadFile("soap/" + name)
		if err != nil {
			continue
		}
		s.templates[name[:len(name)-4]] = string(data)
	}
}

// SetTemplates 외부에서 SOAP 템플릿을 주입한다.
func (s *Service) SetTemplates(templates map[string]string) {
	s.templates = templates
}

// render 템플릿 변수 치환.
func (s *Service) render(templateName string, vars map[string]string) string {
	tpl, ok := s.templates[templateName]
	if !ok {
		return ""
	}
	for k, v := range vars {
		tpl = strings.ReplaceAll(tpl, "{{"+k+"}}", v)
	}
	return tpl
}

// wrapEnvelope SOAP envelope으로 감싼다.
func (s *Service) wrapEnvelope(body string) string {
	return s.render("envelope", map[string]string{"body": body})
}

// Fault SOAP 에러 응답. code는 SOAP 1.2 Fault 코드 (예: "s:Sender", "s:Receiver").
func (s *Service) Fault(reason string) string {
	return s.render("fault", map[string]string{"code": "s:Receiver", "reason": reason})
}

// FaultWithCode 지정된 Fault 코드로 에러 응답을 생성한다.
// 인증 실패(401) 등 클라이언트 측 오류는 s:Sender를 사용해야 한다.
func (s *Service) FaultWithCode(code, reason string) string {
	return s.render("fault", map[string]string{"code": code, "reason": reason})
}

// DeviceUUID 장치 UUID 반환.
func (s *Service) DeviceUUID() string { return s.deviceUUID }

// CameraName 카메라 이름 반환.
func (s *Service) CameraName() string { return s.config.Name }

// OnvifURL ONVIF 엔드포인트 URL 반환.
func (s *Service) OnvifURL() string { return s.config.OnvifURL() }

// VerifyUsernameToken WS-Security UsernameToken 검증.
// 인증 비활성화 시 항상 true.
func (s *Service) VerifyUsernameToken(soapBody string) bool {
	if !s.config.AuthEnabled() {
		return true
	}
	return verifyWSUsernameToken(soapBody, s.config.Username, s.config.Password)
}

// verifyWSUsernameToken WS-Security UsernameToken 검증 (순수 함수).
// PasswordDigest (기본)와 PasswordText fallback 지원.
// 상수 시간 비교 사용.
func verifyWSUsernameToken(soapBody, username, password string) bool {
	tokenUser := extractXMLValue(soapBody, "Username")
	tokenPass := extractXMLValue(soapBody, "Password")
	if tokenUser == "" || tokenPass == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(tokenUser), []byte(username)) == 1

	pwType := extractXMLAttr(soapBody, "Password", "Type")
	if strings.Contains(pwType, "PasswordText") {
		passOK := subtle.ConstantTimeCompare([]byte(tokenPass), []byte(password)) == 1
		return userOK && passOK
	}

	// PasswordDigest (기본).
	nonce := extractXMLValue(soapBody, "Nonce")
	created := extractXMLValue(soapBody, "Created")
	if nonce == "" || created == "" {
		return false
	}
	if !createdWithinSkew(created) {
		return false
	}
	expected := computePasswordDigest(nonce, created, password)
	passOK := subtle.ConstantTimeCompare([]byte(expected), []byte(tokenPass)) == 1
	return userOK && passOK
}

// computePasswordDigest PasswordDigest = Base64(SHA1(Base64Decode(Nonce) + Created + Password))
func computePasswordDigest(nonceB64, created, password string) string {
	nonce, err := base64.StdEncoding.DecodeString(nonceB64)
	if err != nil {
		return ""
	}
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(password))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// createdWithinSkew Created 타임스탬프가 최대 허용 편차 이내인지 확인.
func createdWithinSkew(created string) bool {
	ts := strings.TrimSpace(created)
	ts = strings.Replace(ts, "Z", "+00:00", 1)
	dt, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		// 여러 포맷 시도.
		formats := []string{
			time.RFC3339,
			"2006-01-02T15:04:05+00:00",
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05",
		}
		for _, f := range formats {
			dt, err = time.Parse(f, ts)
			if err == nil {
				break
			}
		}
		if err != nil {
			return true // 파싱 불가 시 검증 스킵 (digest는 여전히 일치해야 함)
		}
	}
	now := time.Now().UTC()
	diff := now.Sub(dt)
	if diff < 0 {
		diff = -diff
	}
	return diff.Seconds() <= maxSkewSeconds
}

// HandleAction SOAP 액션을 라우팅한다.
func (s *Service) HandleAction(action, body string) string {
	now := time.Now().UTC()
	switch action {
	case "GetSystemDateAndTime":
		return s.wrapEnvelope(s.render("get_system_date_time", map[string]string{
			"hour": fmt.Sprint(now.Hour()), "minute": fmt.Sprint(now.Minute()), "second": fmt.Sprint(now.Second()),
			"year": fmt.Sprint(now.Year()), "month": fmt.Sprint(int(now.Month())), "day": fmt.Sprint(now.Day()),
		}))
	case "GetDeviceInformation":
		return s.wrapEnvelope(s.render("get_device_information", map[string]string{
			"manufacturer": xmlEscape(s.config.Manufacturer),
			"model":        xmlEscape(s.config.Model),
			"firmware_version": xmlEscape(s.config.FirmwareVersion),
			"serial_number": xmlEscape(s.config.SerialNumber),
		}))
	case "GetCapabilities":
		return s.wrapEnvelope(s.render("get_capabilities", map[string]string{
			"device_url": s.config.OnvifURL(),
			"media_url":  fmt.Sprintf("http://%s:%d/onvif/media_service", s.config.LocalIP, s.config.OnvifPort),
			"ptz_url":    fmt.Sprintf("http://%s:%d/onvif/ptz_service", s.config.LocalIP, s.config.OnvifPort),
		}))
	case "GetServices":
		return s.wrapEnvelope(s.render("get_services", map[string]string{
			"device_url": s.config.OnvifURL(),
			"media_url":  fmt.Sprintf("http://%s:%d/onvif/media_service", s.config.LocalIP, s.config.OnvifPort),
			"ptz_url":    fmt.Sprintf("http://%s:%d/onvif/ptz_service", s.config.LocalIP, s.config.OnvifPort),
		}))
	case "GetScopes":
		return s.wrapEnvelope(s.render("get_scopes", map[string]string{
			"camera_name": xmlEscape(s.config.Name),
		}))
	case "GetUsers":
		username := s.config.Username
		if username == "" {
			username = "admin"
		}
		return s.wrapEnvelope(s.render("get_users", map[string]string{
			"username": xmlEscape(username),
		}))
	case "GetProfiles":
		return s.wrapEnvelope(s.render("get_profiles", map[string]string{
			"main_width": fmt.Sprint(s.config.MainWidth),
			"main_height": fmt.Sprint(s.config.MainHeight),
			"main_fps": fmt.Sprint(s.config.MainFPS),
			"main_bitrate_kbps": fmt.Sprint(bitrateToKbps(s.config.MainBitrate)),
			"sub_width": fmt.Sprint(s.config.SubWidth),
			"sub_height": fmt.Sprint(s.config.SubHeight),
			"sub_fps": fmt.Sprint(s.config.SubFPS),
			"sub_bitrate_kbps": fmt.Sprint(bitrateToKbps(s.config.SubBitrate)),
		}))
	case "GetVideoEncoderConfiguration":
		return s.wrapEnvelope(s.render("get_video_encoder_configuration", map[string]string{
			"main_width": fmt.Sprint(s.config.MainWidth),
			"main_height": fmt.Sprint(s.config.MainHeight),
			"main_fps": fmt.Sprint(s.config.MainFPS),
			"main_bitrate_kbps": fmt.Sprint(bitrateToKbps(s.config.MainBitrate)),
		}))
	case "GetVideoSourceConfiguration":
		return s.wrapEnvelope(s.render("get_video_source_configuration", map[string]string{
			"main_width": fmt.Sprint(s.config.MainWidth),
			"main_height": fmt.Sprint(s.config.MainHeight),
		}))
	case "GetAudioDecoderConfigurations":
		return s.wrapEnvelope(s.render("get_audio_decoder_configurations", nil))
	case "GetStreamUri":
		return s.handleGetStreamURI(body)
	case "GetSnapshotUri":
		return s.handleGetSnapshotURI(body)
	// PTZ
	case "GetNodes":
		return s.wrapEnvelope(s.render("ptz_get_nodes", nil))
	case "GetNode":
		return s.wrapEnvelope(s.render("ptz_get_node", nil))
	case "GetConfigurations", "GetConfiguration":
		return s.wrapEnvelope(s.render("ptz_get_configurations", nil))
	case "GetServiceCapabilities":
		return s.wrapEnvelope(s.render("ptz_get_service_capabilities", nil))
	case "GetStatus":
		return s.handlePTZGetStatus(body)
	case "ContinuousMove":
		return s.handlePTZContinuousMove(body)
	case "Stop":
		return s.handlePTZStop(body)
	case "AbsoluteMove":
		return s.handlePTZAbsoluteMove(body)
	case "RelativeMove":
		return s.handlePTZRelativeMove(body)
	case "GotoHomePosition":
		return s.handlePTZGotoHome(body)
	case "GetPresets":
		return s.handlePTZGetPresets(body)
	case "SetPreset":
		return s.handlePTZSetPreset(body)
	case "GotoPreset":
		return s.handlePTZGotoPreset(body)
	}
	return s.Fault(fmt.Sprintf("Action not supported: %s", action))
}

func (s *Service) handleGetStreamURI(body string) string {
	uri := s.config.MainStreamRTSP()
	if strings.Contains(body, "Sub") || strings.Contains(body, "Profile_2") {
		uri = s.config.SubStreamRTSP()
	}
	return s.wrapEnvelope(s.render("get_stream_uri", map[string]string{"stream_uri": uri}))
}

func (s *Service) handleGetSnapshotURI(body string) string {
	// 스냅샷은 메인 HTTP 서버(onvif_port)의 snapshot_url 경로로 제공된다.
	// device_service URL을 반환하면 ONVIF 클라이언트가 잘못된 주소로 요청한다.
	uri := fmt.Sprintf("http://%s:%d/%s", s.config.LocalIP, s.config.OnvifPort, s.config.SnapshotURL)
	return s.wrapEnvelope(s.render("get_snapshot_uri", map[string]string{"snapshot_uri": uri}))
}

func (s *Service) handlePTZGetStatus(body string) string {
	pan, tilt, zoom := 0.0, 0.0, 0.0
	moving := "IDLE"
	if s.ptz != nil {
		status := s.ptz.GetStatus()
		pan = status["pan"].(float64)
		tilt = status["tilt"].(float64)
		zoom = status["zoom"].(float64)
		if status["moving"].(bool) {
			moving = "MOVING"
		}
	}
	return s.wrapEnvelope(s.render("ptz_get_status", map[string]string{
		"pan":            fmt.Sprintf("%f", pan),
		"tilt":           fmt.Sprintf("%f", tilt),
		"zoom":           fmt.Sprintf("%f", zoom),
		"pan_tilt_status": moving,
		"zoom_status":    moving,
		"utc_time":       time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}))
}

func (s *Service) handlePTZContinuousMove(body string) string {
	pan, tilt, zoom := extractVelocity(body)
	if s.ptz != nil {
		s.ptz.ContinuousMove(pan, tilt, zoom)
	}
	return s.wrapEnvelope(s.render("ptz_continuous_move", nil))
}

func (s *Service) handlePTZStop(body string) string {
	panTilt := true
	zoom := true
	pt := extractXMLValue(body, "PanTilt")
	if strings.ToLower(pt) == "false" {
		panTilt = false
	}
	z := extractXMLValue(body, "Zoom")
	if strings.ToLower(z) == "false" {
		zoom = false
	}
	if s.ptz != nil {
		s.ptz.StopMovement(panTilt, zoom)
	}
	return s.wrapEnvelope(s.render("ptz_stop", nil))
}

func (s *Service) handlePTZAbsoluteMove(body string) string {
	pan, tilt, zoom := extractPosition(body)
	if s.ptz != nil {
		s.ptz.AbsoluteMove(pan, tilt, zoom)
	}
	return s.wrapEnvelope(s.render("ptz_absolute_move", nil))
}

func (s *Service) handlePTZRelativeMove(body string) string {
	pan, tilt, zoom := extractTranslation(body)
	if s.ptz != nil {
		s.ptz.RelativeMove(pan, tilt, zoom)
	}
	return s.wrapEnvelope(s.render("ptz_relative_move", nil))
}

func (s *Service) handlePTZGotoHome(body string) string {
	if s.ptz != nil {
		s.ptz.GotoHome()
	}
	return s.wrapEnvelope(s.render("ptz_goto_home", nil))
}

func (s *Service) handlePTZGetPresets(body string) string {
	presetItems := ""
	if s.ptz != nil {
		presets := s.ptz.GetPresets()
		for _, p := range presets {
			// Token/Name은 클라이언트 SetPreset 요청에서 유래하므로 XML 이스케이프.
			safeToken := xmlEscape(p.Token)
			safeName := xmlEscape(p.Name)
			presetItems += fmt.Sprintf(`
      <tptz:Preset token="%s">
        <tt:Name>%s</tt:Name>
        <tt:PTZPosition>
          <tt:PanTilt x="%f" y="%f"/>
          <tt:Zoom x="%f"/>
        </tt:PTZPosition>
      </tptz:Preset>`, safeToken, safeName, p.Pan, p.Tilt, p.Zoom)
		}
	}
	return s.wrapEnvelope(s.render("ptz_get_presets", map[string]string{"presets": presetItems}))
}

func (s *Service) handlePTZSetPreset(body string) string {
	presetName := extractXMLValue(body, "PresetName")
	if presetName == "" {
		presetName = "Preset"
	}
	presetToken := extractXMLAttr(body, "SetPreset", "PresetToken")
	if presetToken == "" {
		presetToken = fmt.Sprintf("preset_%x", time.Now().UnixNano())
	}
	if s.ptz != nil {
		s.ptz.SetPreset(presetToken, presetName)
	}
	// 토큰은 클라이언트가 제공할 수 있으므로 이스케이프.
	return s.wrapEnvelope(s.render("ptz_set_preset", map[string]string{"preset_token": xmlEscape(presetToken)}))
}

func (s *Service) handlePTZGotoPreset(body string) string {
	presetToken := extractXMLValue(body, "PresetToken")
	if s.ptz != nil && presetToken != "" {
		s.ptz.GotoPreset(presetToken)
	}
	return s.wrapEnvelope(s.render("ptz_goto_preset", nil))
}

// CreateProbeMatch WS-Discovery ProbeMatch 응답 생성.
func (s *Service) CreateProbeMatch(relatesTo string) string {
	return s.render("probe_match", map[string]string{
		"message_id":  fmt.Sprintf("urn:uuid:%x", time.Now().UnixNano()),
		"relates_to":  relatesTo,
		"device_uuid": s.deviceUUID,
		"camera_name": xmlEscape(s.config.Name),
		"onvif_url":   xmlEscape(s.config.OnvifURL()),
	})
}

// XML 추출 함수들 (ns tolerant).
var (
	velocityRE  = regexp.MustCompile(`<(?:\w+:)?PanTilt[^>]*x="([^"]*)"[^>]*y="([^"]*)"`)
	zoomRE      = regexp.MustCompile(`<(?:\w+:)?Zoom[^>]*x="([^"]*)"`)
	positionRE  = regexp.MustCompile(`(?s)<(?:\w+:)?Position[^>]*>.*?<(?:\w+:)?PanTilt[^>]*x="([^"]*)"[^>]*y="([^"]*)"`)
	zoomPosRE   = regexp.MustCompile(`(?s)<(?:\w+:)?Position[^>]*>.*?<(?:\w+:)?Zoom[^>]*x="([^"]*)"`)
	translationRE = regexp.MustCompile(`(?s)<(?:\w+:)?Translation[^>]*>.*?<(?:\w+:)?PanTilt[^>]*x="([^"]*)"[^>]*y="([^"]*)"`)
	zoomTransRE = regexp.MustCompile(`(?s)<(?:\w+:)?Translation[^>]*>.*?<(?:\w+:)?Zoom[^>]*x="([^"]*)"`)
)

func extractXMLValue(body, tag string) string {
	re := regexp.MustCompile(fmt.Sprintf(`<(?:\w+:)?%s\b[^>]*>([^<]*)</(?:\w+:)?%s>`, tag, tag))
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func extractXMLAttr(body, tag, attr string) string {
	re := regexp.MustCompile(fmt.Sprintf(`<(?:\w+:)?%s\b[^>]*\b%s="([^"]*)"`, tag, attr))
	m := re.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func extractVelocity(body string) (float64, float64, float64) {
	pan, tilt := 0.0, 0.0
	zoom := 0.0
	if m := velocityRE.FindStringSubmatch(body); len(m) >= 3 {
		pan = parseFloat(m[1])
		tilt = parseFloat(m[2])
	}
	if m := zoomRE.FindStringSubmatch(body); len(m) >= 2 {
		zoom = parseFloat(m[1])
	}
	return pan, tilt, zoom
}

func extractPosition(body string) (*float64, *float64, *float64) {
	var pan, tilt, zoom *float64
	if m := positionRE.FindStringSubmatch(body); len(m) >= 3 {
		p := parseFloat(m[1])
		t := parseFloat(m[2])
		pan, tilt = &p, &t
	}
	if m := zoomPosRE.FindStringSubmatch(body); len(m) >= 2 {
		z := parseFloat(m[1])
		zoom = &z
	}
	return pan, tilt, zoom
}

func extractTranslation(body string) (float64, float64, float64) {
	pan, tilt := 0.0, 0.0
	zoom := 0.0
	if m := translationRE.FindStringSubmatch(body); len(m) >= 3 {
		pan = parseFloat(m[1])
		tilt = parseFloat(m[2])
	}
	if m := zoomTransRE.FindStringSubmatch(body); len(m) >= 2 {
		zoom = parseFloat(m[1])
	}
	return pan, tilt, zoom
}

func parseFloat(s string) float64 {
	var v float64
	fmt.Sscanf(s, "%f", &v)
	return v
}

// xmlEscape XML 특수 문자 이스케이프.
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// bitrateToKbps '4M'/'512K' 같은 비트레이트 문자열을 kbps로 변환.
func bitrateToKbps(bitrate string) int {
	bitrate = strings.TrimSpace(bitrate)
	if strings.HasSuffix(bitrate, "M") || strings.HasSuffix(bitrate, "m") {
		n, err := strconv.Atoi(bitrate[:len(bitrate)-1])
		if err == nil {
			return n * 1000
		}
	}
	if strings.HasSuffix(bitrate, "K") || strings.HasSuffix(bitrate, "k") {
		n, err := strconv.Atoi(bitrate[:len(bitrate)-1])
		if err == nil {
			return n
		}
	}
	n, _ := strconv.Atoi(bitrate)
	return n
}