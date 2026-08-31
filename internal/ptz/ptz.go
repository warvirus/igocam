// 디지털 PTZ (Pan-Tilt-Zoom) 컨트롤러 - 크롭/스케일 기반 ePTZ + 하드웨어 핸들러
package ptz

import (
	"encoding/json"
	"image"
	"os"
	"sync"
	"time"

	"gocv.io/x/gocv"
)

// State 현재 PTZ 위치 상태.
type State struct {
	Pan  float64 `json:"pan"`  // -1.0 ~ 1.0 (좌우)
	Tilt float64 `json:"tilt"` // -1.0 ~ 1.0 (상하)
	Zoom float64 `json:"zoom"` // 0.0 ~ 1.0 (wide~tele)
}

// Velocity 현재 PTZ 이동 속도.
type Velocity struct {
	PanSpeed  float64 `json:"pan_speed"`
	TiltSpeed float64 `json:"tilt_speed"`
	ZoomSpeed float64 `json:"zoom_speed"`
}

// Preset 저장된 PTZ 프리셋 위치.
type Preset struct {
	Token string  `json:"token"`
	Name  string  `json:"name"`
	Pan   float64 `json:"pan"`
	Tilt  float64 `json:"tilt"`
	Zoom  float64 `json:"zoom"`
}

// HardwareHandler 외부 하드웨어 PTZ 컨트롤러 인터페이스.
// 모든 메서드는 정규화된 값을 받는다:
// - pan: -1.0(왼쪽) ~ 1.0(오른쪽)
// - tilt: -1.0(아래) ~ 1.0(위)
// - zoom: 0.0(wide) ~ 1.0(tele)
type HardwareHandler interface {
	OnContinuousMove(panSpeed, tiltSpeed, zoomSpeed float64)
	OnStop()
	OnAbsoluteMove(pan, tilt, zoom *float64)
	OnRelativeMove(panDelta, tiltDelta, zoomDelta float64)
	OnGotoPreset(token string, pan, tilt, zoom float64)
	OnGotoHome()
}

// Controller 디지털 PTZ 컨트롤러.
type Controller struct {
	OutputWidth  int
	OutputHeight int
	MaxZoom      float64
	EnableDigital bool
	WrapPan      bool
	PresetsFile  string

	mu       sync.Mutex
	state    State
	velocity Velocity
	presets  map[string]Preset
	isDefault bool

	handlers []HardwareHandler
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// New 컨트롤러 생성. 프리셋을 로드하고 이동 스레드를 시작한다.
func New(outputWidth, outputHeight int, maxZoom float64, presetsFile string) *Controller {
	c := &Controller{
		OutputWidth:   outputWidth,
		OutputHeight:  outputHeight,
		MaxZoom:       maxZoom,
		EnableDigital: true,
		PresetsFile:   presetsFile,
		presets:       map[string]Preset{},
		isDefault:     true,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	if maxZoom <= 0 {
		c.MaxZoom = 4.0
	}
	c.loadPresets()
	go c.movementLoop()
	return c
}

// Stop 컨트롤러를 정지한다.
func (c *Controller) Stop() {
	select {
	case <-c.stopCh:
		return
	default:
		close(c.stopCh)
	}
	select {
	case <-c.doneCh:
	case <-time.After(time.Second):
	}
}

// AddHardwareHandler 외부 하드웨어 컨트롤러를 등록한다.
func (c *Controller) AddHardwareHandler(h HardwareHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers = append(c.handlers, h)
}

// RemoveHardwareHandler 하드웨어 컨트롤러를 제거한다.
func (c *Controller) RemoveHardwareHandler(h HardwareHandler) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, hh := range c.handlers {
		if hh == h {
			c.handlers = append(c.handlers[:i], c.handlers[i+1:]...)
			return true
		}
	}
	return false
}

func (c *Controller) notifyHardware(method string, args ...any) {
	c.mu.Lock()
	handlers := append([]HardwareHandler{}, c.handlers...)
	c.mu.Unlock()
	for _, h := range handlers {
		switch method {
		case "continuous":
			h.OnContinuousMove(args[0].(float64), args[1].(float64), args[2].(float64))
		case "stop":
			h.OnStop()
		case "absolute":
			h.OnAbsoluteMove(args[0].(*float64), args[1].(*float64), args[2].(*float64))
		case "relative":
			h.OnRelativeMove(args[0].(float64), args[1].(float64), args[2].(float64))
		case "goto_preset":
			h.OnGotoPreset(args[0].(string), args[1].(float64), args[2].(float64), args[3].(float64))
		case "goto_home":
			h.OnGotoHome()
		}
	}
}

// ApplyPTZ 입력 프레임에 PTZ 변환을 적용한다. 결과를 dst에 쓴다.
// 입력은 최소한 OutputWidth x OutputHeight 이상이어야 한다.
// 디지털 PTZ가 비활성이거나 기본 위치이면 원본을 그대로 반환한다.
func (c *Controller) ApplyPTZ(src *gocv.Mat, dst *gocv.Mat) *gocv.Mat {
	if !c.EnableDigital {
		src.CopyTo(dst)
		return dst
	}
	c.mu.Lock()
	isDefault := c.isDefault
	pan, tilt, zoom := c.state.Pan, c.state.Tilt, c.state.Zoom
	c.mu.Unlock()

	if isDefault {
		src.CopyTo(dst)
		return dst
	}

	srcH, srcW := src.Rows(), src.Cols()
	zoomFactor := 1.0 + zoom*(c.MaxZoom-1.0)
	cropW := int(float64(srcW) / zoomFactor)
	cropH := int(float64(srcH) / zoomFactor)
	if cropW > srcW {
		cropW = srcW
	}
	if cropH > srcH {
		cropH = srcH
	}

	maxOffsetX := (srcW - cropW) / 2
	maxOffsetY := (srcH - cropH) / 2

	centerX := srcW/2 + int(pan*float64(maxOffsetX))
	centerY := srcH/2 - int(tilt*float64(maxOffsetY))

	x1 := max(0, centerX-cropW/2)
	y1 := max(0, centerY-cropH/2)
	x2 := min(srcW, x1+cropW)
	y2 := min(srcH, y1+cropH)

	roi := src.Region(image.Rect(x1, y1, x2, y2))
	defer roi.Close()

	if roi.Cols() != c.OutputWidth || roi.Rows() != c.OutputHeight {
		gocv.Resize(roi, dst, image.Pt(c.OutputWidth, c.OutputHeight), 0, 0, gocv.InterpolationLinear)
	} else {
		roi.CopyTo(dst)
	}
	return dst
}

// movementLoop 연속 이동을 처리하는 백그라운드 루프.
func (c *Controller) movementLoop() {
	defer close(c.doneCh)
	lastTime := time.Now()
	ticker := time.NewTicker(16 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
		}
		now := time.Now()
		dt := now.Sub(lastTime).Seconds()
		lastTime = now

		c.mu.Lock()
		hasMovement := abs(c.velocity.PanSpeed) >= 0.001 ||
			abs(c.velocity.TiltSpeed) >= 0.001 ||
			abs(c.velocity.ZoomSpeed) >= 0.001
		if hasMovement {
			c.state.Pan += c.velocity.PanSpeed * dt
			c.state.Tilt += c.velocity.TiltSpeed * dt
			c.state.Zoom += c.velocity.ZoomSpeed * dt
			if !c.WrapPan {
				c.state.Pan = clamp(c.state.Pan, -1, 1)
			}
			c.state.Tilt = clamp(c.state.Tilt, -1, 1)
			c.state.Zoom = clamp(c.state.Zoom, 0, 1)
			c.isDefault = abs(c.state.Pan) < 0.001 && abs(c.state.Tilt) < 0.001 && abs(c.state.Zoom) < 0.001
		}
		c.mu.Unlock()
	}
}

// ContinuousMove 지정 속도로 연속 이동을 시작한다.
func (c *Controller) ContinuousMove(panSpeed, tiltSpeed, zoomSpeed float64) {
	c.mu.Lock()
	c.velocity.PanSpeed = clamp(panSpeed, -1, 1)
	c.velocity.TiltSpeed = clamp(tiltSpeed, -1, 1)
	c.velocity.ZoomSpeed = clamp(zoomSpeed, -1, 1)
	c.mu.Unlock()
	c.notifyHardware("continuous", panSpeed, tiltSpeed, zoomSpeed)
}

// StopMovement 이동을 정지한다.
func (c *Controller) StopMovement(panTilt, zoom bool) {
	c.mu.Lock()
	if panTilt {
		c.velocity.PanSpeed = 0
		c.velocity.TiltSpeed = 0
	}
	if zoom {
		c.velocity.ZoomSpeed = 0
	}
	c.mu.Unlock()
	c.notifyHardware("stop")
}

// AbsoluteMove 절대 위치로 이동한다.
func (c *Controller) AbsoluteMove(pan, tilt, zoom *float64) {
	c.mu.Lock()
	if pan != nil {
		if !c.WrapPan {
			c.state.Pan = clamp(*pan, -1, 1)
		}
	}
	if tilt != nil {
		c.state.Tilt = clamp(*tilt, -1, 1)
	}
	if zoom != nil {
		c.state.Zoom = clamp(*zoom, 0, 1)
	}
	c.velocity = Velocity{}
	c.updateDefaultLocked()
	c.mu.Unlock()
	c.notifyHardware("absolute", pan, tilt, zoom)
}

// RelativeMove 현재 위치에서 상대 이동한다.
func (c *Controller) RelativeMove(panDelta, tiltDelta, zoomDelta float64) {
	c.mu.Lock()
	if !c.WrapPan {
		c.state.Pan = clamp(c.state.Pan+panDelta, -1, 1)
	}
	c.state.Tilt = clamp(c.state.Tilt+tiltDelta, -1, 1)
	c.state.Zoom = clamp(c.state.Zoom+zoomDelta, 0, 1)
	c.velocity = Velocity{}
	c.updateDefaultLocked()
	c.mu.Unlock()
	c.notifyHardware("relative", panDelta, tiltDelta, zoomDelta)
}

// GotoHome 홈 위치(중앙, 줌 없음)로 이동한다.
func (c *Controller) GotoHome() {
	zero := 0.0
	c.AbsoluteMove(&zero, &zero, &zero)
	c.notifyHardware("goto_home")
}

// GetStatus 현재 PTZ 상태를 반환한다.
func (c *Controller) GetStatus() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{
		"pan":   c.state.Pan,
		"tilt":  c.state.Tilt,
		"zoom":  c.state.Zoom,
		"moving": abs(c.velocity.PanSpeed) > 0.001 ||
			abs(c.velocity.TiltSpeed) > 0.001 ||
			abs(c.velocity.ZoomSpeed) > 0.001,
	}
}

// GetState 현재 상태를 반환한다.
func (c *Controller) GetState() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// SetPreset 현재 위치를 프리셋으로 저장한다.
func (c *Controller) SetPreset(token, name string) string {
	c.mu.Lock()
	c.presets[token] = Preset{
		Token: token, Name: name,
		Pan: c.state.Pan, Tilt: c.state.Tilt, Zoom: c.state.Zoom,
	}
	c.mu.Unlock()
	c.savePresets()
	return token
}

// GotoPreset 프리셋 위치로 이동한다.
func (c *Controller) GotoPreset(token string) bool {
	c.mu.Lock()
	p, ok := c.presets[token]
	if !ok {
		c.mu.Unlock()
		return false
	}
	c.state.Pan = p.Pan
	c.state.Tilt = p.Tilt
	c.state.Zoom = p.Zoom
	c.velocity = Velocity{}
	c.updateDefaultLocked()
	c.mu.Unlock()
	c.notifyHardware("goto_preset", token, p.Pan, p.Tilt, p.Zoom)
	return true
}

// RemovePreset 프리셋을 제거한다.
func (c *Controller) RemovePreset(token string) bool {
	c.mu.Lock()
	_, ok := c.presets[token]
	if ok {
		delete(c.presets, token)
	}
	c.mu.Unlock()
	if ok {
		c.savePresets()
	}
	return ok
}

// GetPresets 모든 프리셋을 반환한다.
func (c *Controller) GetPresets() map[string]Preset {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]Preset, len(c.presets))
	for k, v := range c.presets {
		out[k] = v
	}
	return out
}

func (c *Controller) updateDefaultLocked() {
	c.isDefault = abs(c.state.Pan) < 0.001 && abs(c.state.Tilt) < 0.001 && abs(c.state.Zoom) < 0.001
}

func (c *Controller) loadPresets() {
	file := c.PresetsFile
	if file == "" {
		file = "ptz_presets.json"
	}
	data, err := os.ReadFile(file)
	if err != nil {
		c.presets["home"] = Preset{Token: "home", Name: "Home", Pan: 0, Tilt: 0, Zoom: 0}
		return
	}
	var loaded map[string]Preset
	if err := json.Unmarshal(data, &loaded); err != nil {
		c.presets["home"] = Preset{Token: "home", Name: "Home", Pan: 0, Tilt: 0, Zoom: 0}
		return
	}
	c.presets = loaded
	if _, ok := c.presets["home"]; !ok {
		c.presets["home"] = Preset{Token: "home", Name: "Home", Pan: 0, Tilt: 0, Zoom: 0}
	}
}

func (c *Controller) savePresets() {
	file := c.PresetsFile
	if file == "" {
		file = "ptz_presets.json"
	}
	c.mu.Lock()
	data, err := json.MarshalIndent(c.presets, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return
	}
	_ = os.WriteFile(file, data, 0o644)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
