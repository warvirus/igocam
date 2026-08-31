// ONVIF 서비스 테스트
package onvif

import (
	"crypto/sha1"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"igocam/internal/config"
	"igocam/internal/ptz"
)

func TestComputePasswordDigest(t *testing.T) {
	nonce := base64.StdEncoding.EncodeToString([]byte("testnonce123"))
	created := "2024-01-01T00:00:00Z"
	password := "secret123"

	expected := computePasswordDigest(nonce, created, password)
	if expected == "" {
		t.Fatal("empty digest")
	}

	// Verify: SHA1(nonce_bytes + created + password) then base64
	nonceBytes, _ := base64.StdEncoding.DecodeString(nonce)
	h := sha1.New()
	h.Write(nonceBytes)
	h.Write([]byte(created))
	h.Write([]byte(password))
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if expected != want {
		t.Fatalf("digest mismatch: got %q, want %q", expected, want)
	}
}

func TestComputePasswordDigestInvalidNonce(t *testing.T) {
	if d := computePasswordDigest("invalid-base64!!!", "now", "pass"); d != "" {
		t.Fatal("should return empty for invalid base64")
	}
}

func TestCreatedWithinSkew(t *testing.T) {
	// 현재 시각은 스큐 이내여야 함.
	if !createdWithinSkew(time.Now().UTC().Format(time.RFC3339)) {
		t.Fatal("recent time should be within skew")
	}
	// 과거 시각은 스큐 밖이어야 함 (300초 넘게 차이).
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if createdWithinSkew(old) {
		t.Fatal("old time should be outside skew")
	}
	// 파싱 불가 시각은 스큐 검증을 건너뛰므로 true.
	if !createdWithinSkew("not-a-timestamp") {
		t.Fatal("unparseable timestamp should return true")
	}
}

func TestVerifyUsernameTokenMissingFields(t *testing.T) {
	// No UsernameToken elements -> verify fails
	if verifyWSUsernameToken("<soap><Body></Body></soap>", "user", "pass") {
		t.Fatal("should fail with no token")
	}
}

func TestVerifyUsernameTokenPasswordText(t *testing.T) {
	soap := `<soap:Header>
		<wsse:Security>
			<wsse:UsernameToken>
				<wsse:Username>admin</wsse:Username>
				<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText">pass123</wsse:Password>
			</wsse:UsernameToken>
		</wsse:Security>
	</soap:Header>`
	if !verifyWSUsernameToken(soap, "admin", "pass123") {
		t.Fatal("PasswordText verification failed")
	}
	if verifyWSUsernameToken(soap, "admin", "wrong") {
		t.Fatal("wrong password should fail")
	}
	if verifyWSUsernameToken(soap, "wrong", "pass123") {
		t.Fatal("wrong username should fail")
	}
}

func TestVerifyUsernameTokenPasswordDigest(t *testing.T) {
	nonce := base64.StdEncoding.EncodeToString([]byte("randomnonce"))
	created := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	digest := computePasswordDigest(nonce, created, "mypassword")

	soap := `<soap:Header>
		<wsse:Security>
			<wsse:UsernameToken>
				<wsse:Username>admin</wsse:Username>
				<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + digest + `</wsse:Password>
				<wsse:Nonce>` + nonce + `</wsse:Nonce>
				<wsu:Created>` + created + `</wsu:Created>
			</wsse:UsernameToken>
		</wsse:Security>
	</soap:Header>`
	if !verifyWSUsernameToken(soap, "admin", "mypassword") {
		t.Fatal("PasswordDigest verification failed")
	}
	if verifyWSUsernameToken(soap, "admin", "wrong") {
		t.Fatal("wrong password should fail")
	}
}

func TestExtractXMLValue(t *testing.T) {
	body := `<root><ns:ProfileToken>profile1</ns:ProfileToken></root>`
	v := extractXMLValue(body, "ProfileToken")
	if v != "profile1" {
		t.Fatalf("got %q, want profile1", v)
	}
}

func TestExtractXMLAttr(t *testing.T) {
	body := `<root><ns:SetPreset PresetToken="token123"></ns:SetPreset></root>`
	v := extractXMLAttr(body, "SetPreset", "PresetToken")
	if v != "token123" {
		t.Fatalf("got %q, want token123", v)
	}
}

func TestExtractVelocity(t *testing.T) {
	body := `<ContinuousMove>
		<PanTilt x="0.5" y="-0.3"/>
		<Zoom x="0.8"/>
	</ContinuousMove>`
	pan, tilt, zoom := extractVelocity(body)
	if pan != 0.5 || tilt != -0.3 || zoom != 0.8 {
		t.Fatalf("velocity = (%f, %f, %f), want (0.5, -0.3, 0.8)", pan, tilt, zoom)
	}
}

func TestExtractVelocityNoZoom(t *testing.T) {
	body := `<ContinuousMove><PanTilt x="0.1" y="0.2"/></ContinuousMove>`
	pan, tilt, zoom := extractVelocity(body)
	if pan != 0.1 || tilt != 0.2 || zoom != 0.0 {
		t.Fatalf("velocity = (%f, %f, %f)", pan, tilt, zoom)
	}
}

func TestExtractPosition(t *testing.T) {
	body := `<AbsoluteMove>
		<Position>
			<PanTilt x="0.7" y="-0.5"/>
			<Zoom x="0.3"/>
		</Position>
	</AbsoluteMove>`
	pan, tilt, zoom := extractPosition(body)
	if pan == nil || tilt == nil || zoom == nil {
		t.Fatal("position should not be nil")
	}
	if *pan != 0.7 || *tilt != -0.5 || *zoom != 0.3 {
		t.Fatalf("position = (%f, %f, %f)", *pan, *tilt, *zoom)
	}
}

func TestExtractTranslation(t *testing.T) {
	body := `<RelativeMove>
		<Translation>
			<PanTilt x="0.1" y="-0.2"/>
			<Zoom x="0.05"/>
		</Translation>
	</RelativeMove>`
	pan, tilt, zoom := extractTranslation(body)
	if pan != 0.1 || tilt != -0.2 || zoom != 0.05 {
		t.Fatalf("translation = (%f, %f, %f)", pan, tilt, zoom)
	}
}

func TestParseFloat(t *testing.T) {
	if v := parseFloat("3.14"); v != 3.14 {
		t.Fatalf("got %f", v)
	}
	if v := parseFloat("invalid"); v != 0.0 {
		t.Fatalf("got %f for invalid", v)
	}
}
func TestEnvelopeHasPTZNamespaces(t *testing.T) {
	s := New(config.DefaultConfig(), nil)
	// envelope 템플릿이 PTZ 응답의 ptz/tptz 접두사를 선언하는지 확인.
	env := s.templates["envelope"]
	for _, ns := range []string{"xmlns:ptz=", "xmlns:tptz="} {
		if !strings.Contains(env, ns) {
			t.Fatalf("envelope missing %s", ns)
		}
	}
}

func TestPTZGetStatusRendersValues(t *testing.T) {
	cfg := config.DefaultConfig()
	ptzCtrl := ptz.New(1920, 1080, 4.0, "")
	defer ptzCtrl.Stop()
	ptzCtrl.AbsoluteMove(f64ptr(0.3), f64ptr(-0.2), f64ptr(0.5))
	s := New(cfg, ptzCtrl)
	resp := s.HandleAction("GetStatus", "")
	// 미치환 템플릿 잔여 없음 + 실제 값 포함.
	if strings.Contains(resp, "{{") {
		t.Fatalf("unrendered template vars in GetStatus: %s", resp)
	}
	if !strings.Contains(resp, "0.300000") {
		t.Fatalf("pan value not rendered: %s", resp)
	}
	if !strings.Contains(resp, "-0.200000") {
		t.Fatalf("tilt value not rendered: %s", resp)
	}
	if !strings.Contains(resp, "UtcTime") {
		t.Fatal("missing UtcTime")
	}
}

func TestGetSnapshotURI(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.LocalIP = "192.168.1.10"
	cfg.OnvifPort = 8080
	cfg.SnapshotURL = "snapshot.jpg"
	s := New(cfg, nil)
	resp := s.HandleAction("GetSnapshotUri", "")
	if !strings.Contains(resp, "http://192.168.1.10:8080/snapshot.jpg") {
		t.Fatalf("snapshot URI wrong: %s", resp)
	}
	if strings.Contains(resp, "device_service") {
		t.Fatal("snapshot URI must not be device_service URL")
	}
}

func TestFaultWithCode(t *testing.T) {
	s := New(config.DefaultConfig(), nil)
	resp := s.FaultWithCode("s:Sender", "not authorized")
	if !strings.Contains(resp, "s:Sender") {
		t.Fatalf("fault code not s:Sender: %s", resp)
	}
	if strings.Contains(resp, "s:Receiver") {
		t.Fatal("fault should not use Receiver for sender error")
	}
	if !strings.Contains(resp, "not authorized") {
		t.Fatal("reason missing")
	}
}

func TestPTZPresetXMLInjection(t *testing.T) {
	cfg := config.DefaultConfig()
	ptzCtrl := ptz.New(1920, 1080, 4.0, "")
	defer ptzCtrl.Stop()
	// 악의적인 프리셋 이름/토큰이 XML 주입되지 않아야 함 (이스케이프되어야 함).
	ptzCtrl.SetPreset(`x" onload="alert(1)`, `</tt:Name><x>`)
	s := New(cfg, ptzCtrl)
	resp := s.HandleAction("GetPresets", "")
	// 원시 인용부호/태그가 그대로 노출되면 주입. 이스케이프된 형태여야 안전.
	if strings.Contains(resp, `token="x" onload=`) {
		t.Fatalf("XML injection in preset token: %s", resp)
	}
	if strings.Contains(resp, `<x>`) {
		t.Fatalf("raw XML from preset name: %s", resp)
	}
	// 이스케이프된 형태가 존재해야 함.
	if !strings.Contains(resp, `onload=&quot;`) {
		t.Fatalf("preset token should be escaped: %s", resp)
	}
}

func f64ptr(v float64) *float64 { return &v }
