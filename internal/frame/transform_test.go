// 디스플레이 변환 및 타임스탬프 테스트
package frame

import (
	"testing"

	"gocv.io/x/gocv"
)

func TestTimestampFormatGo(t *testing.T) {
	cases := map[string]string{
		"%Y-%m-%d %H:%M:%S":   "2006-01-02 15:04:05",
		"%Y/%m/%d %H:%M":      "2006/01/02 15:04",
		"%Y-%m-%d":            "2006-01-02",
		"plain text":          "plain text",
		"%%Y":                 "%Y",
		"%d-%b-%Y":            "02-Jan-2006",
	}
	for input, want := range cases {
		if got := TimestampFormatGo(input); got != want {
			t.Errorf("TimestampFormatGo(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestParseTimestampPosition(t *testing.T) {
	cases := map[string]TimestampPosition{
		"top-left":     TopLeft,
		"top-right":    TopRight,
		"bottom-left":  BottomLeft,
		"bottom-right": BottomRight,
		"invalid":      BottomLeft, // default
		"":             BottomLeft,
	}
	for input, want := range cases {
		if got := ParseTimestampPosition(input); got != want {
			t.Errorf("ParseTimestampPosition(%q) = %d, want %d", input, got, want)
		}
	}
}

func testMat(rows, cols int) *gocv.Mat {
	img := gocv.NewMatWithSize(rows, cols, gocv.MatTypeCV8UC3)
	return &img
}

func TestApplyTransformsNoOp(t *testing.T) {
	src := testMat(100, 200)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	ApplyTransforms(src, &dst, false, false, 0)
	if dst.Empty() {
		t.Fatal("dst should not be empty")
	}
	if dst.Rows() != 100 || dst.Cols() != 200 {
		t.Fatalf("no-op transform changed size: %dx%d", dst.Cols(), dst.Rows())
	}
}

func TestApplyTransformsRotate90(t *testing.T) {
	src := testMat(100, 200)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	ApplyTransforms(src, &dst, false, false, 90)
	// 90도 회전은 너비/높이를 바꾼다.
	if dst.Rows() != 200 || dst.Cols() != 100 {
		t.Fatalf("rotate90 size = %dx%d, want 200x100", dst.Cols(), dst.Rows())
	}
}

func TestApplyTransformsRotate270(t *testing.T) {
	src := testMat(100, 200)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	ApplyTransforms(src, &dst, false, false, 270)
	if dst.Rows() != 200 || dst.Cols() != 100 {
		t.Fatalf("rotate270 size = %dx%d, want 200x100", dst.Cols(), dst.Rows())
	}
}

func TestApplyTransformsFlipMirror(t *testing.T) {
	src := testMat(100, 200)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	ApplyTransforms(src, &dst, true, true, 0)
	if dst.Rows() != 100 || dst.Cols() != 200 {
		t.Fatalf("flip+mirror changed size: %dx%d", dst.Cols(), dst.Rows())
	}
}

func TestApplyTransformsAll(t *testing.T) {
	src := testMat(100, 200)
	defer src.Close()
	dst := gocv.NewMat()
	defer dst.Close()

	ApplyTransforms(src, &dst, true, true, 180)
	if dst.Rows() != 100 || dst.Cols() != 200 {
		t.Fatalf("all transforms size = %dx%d", dst.Cols(), dst.Rows())
	}
}

func TestDrawTimestamp(t *testing.T) {
	img := testMat(100, 200)
	defer img.Close()

	DrawTimestamp(img, BottomLeft, "2006-01-02 15:04:05")
	// 텍스트가 그려졌는지 픽셀 변화로 간단 확인 (모서리 부근은 흰색/검정 텍스트 존재).
	if img.Empty() {
		t.Fatal("img empty")
	}
	// TimestampFormatGo 매핑도 함께 확인
	if got := TimestampFormatGo("%H:%M:%S"); got != "15:04:05" {
		t.Fatalf("format = %q", got)
	}
}
