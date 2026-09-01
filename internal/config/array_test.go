// 카메라별 WebUI 저장 시 배열 구조 보존 회귀 테스트
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSavePreservesMultiCameraArray 카메라별 WebUI가 Save()로 저장해도
// camera_config.json 배열이 단일 객체로 깨지지 않는지 검증한다.
// (과거 버그: Save가 단일 객체로 덮어써 배열 파괴)
func TestSavePreservesMultiCameraArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arr.json")

	c1 := DefaultConfig()
	c1.ID = "cam_one"
	c1.Name = "Cam One"
	c1.OnvifPort = 8090
	c2 := DefaultConfig()
	c2.ID = "cam_two"
	c2.Name = "Cam Two"
	c2.OnvifPort = 8100
	if err := SaveAll(path, []*CameraConfig{c1, c2}); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	// cam_one 설정 변경 후 Save → 배열 유지 + cam_one 항목만 변경.
	c1.MainWidth = 1600
	c1.ConfigPath = path
	if err := c1.Save(""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := LoadAll(path)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("array broken: got %d cameras, want 2", len(loaded))
	}
	if loaded[0].MainWidth != 1600 {
		t.Fatalf("cam1 main_width = %d, want 1600", loaded[0].MainWidth)
	}
	if loaded[1].Name != "Cam Two" {
		t.Fatalf("cam2 corrupted: %q", loaded[1].Name)
	}
}

// TestSaveAppendsNewCamera 배열에 없는 카메라를 Save()하면 추가되는지 검증.
func TestSaveAppendsNewCamera(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arr.json")

	c1 := DefaultConfig()
	c1.ID = "cam_one"
	c1.Name = "Cam One"
	c1.OnvifPort = 8090
	if err := SaveAll(path, []*CameraConfig{c1}); err != nil {
		t.Fatalf("SaveAll: %v", err)
	}

	nc := DefaultConfig()
	nc.ID = "cam_new"
	nc.Name = "Cam New"
	nc.OnvifPort = 8190
	nc.ConfigPath = path
	if err := nc.Save(""); err != nil {
		t.Fatalf("Save new: %v", err)
	}
	loaded, err := LoadAll(path)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("want 2 cameras after append, got %d", len(loaded))
	}
	if loaded[1].Name != "Cam New" {
		t.Fatalf("new camera not appended: %q", loaded[1].Name)
	}
}

// TestSaveSingleLegacy 단일 카메라(레거시) 모드는 객체로 저장 유지 검증.
func TestSaveSingleLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "single.json")

	cfg := DefaultConfig()
	cfg.Name = "Legacy Cam"
	cfg.OnvifPort = 8090
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatalf("single save should be an object, got: %s", string(data))
	}
	if obj["name"] != "Legacy Cam" {
		t.Fatalf("name = %v", obj["name"])
	}
}
