// 디스플레이 변환 (flip/mirror/rotate) 및 타임스탬프 오버레이
package frame

import (
	"image"
	"image/color"
	"strings"
	"time"

	"gocv.io/x/gocv"
)

// TimestampFormatGo 파이썬 strftime 형식을 Go time 레이아웃으로 변환한다.
// IPyCam config의 timestamp_format (예: "%Y-%m-%d %H:%M:%S")을 그대로 사용할 수
// 있도록 공통 형식을 매핑한다. 알 수 없는 지시어는 리터럴로 유지한다.
func TimestampFormatGo(strftime string) string {
	repl := []struct{ py, go_ string }{
		{"%Y", "2006"},
		{"%y", "06"},
		{"%m", "01"},
		{"%d", "02"},
		{"%H", "15"},
		{"%M", "04"},
		{"%S", "05"},
		{"%I", "03"},
		{"%p", "PM"},
		{"%f", "000000"},
		{"%z", "-0700"},
		{"%Z", "MST"},
		{"%a", "Mon"},
		{"%A", "Monday"},
		{"%b", "Jan"},
		{"%B", "January"},
	}
	out := strings.ReplaceAll(strftime, "%%", "\x00")
	for _, r := range repl {
		out = strings.ReplaceAll(out, r.py, r.go_)
	}
	return strings.ReplaceAll(out, "\x00", "%")
}

// TimestampPosition 타임스탬프 위치.
type TimestampPosition int

const (
	TopLeft TimestampPosition = iota
	TopRight
	BottomLeft
	BottomRight
)

// ParseTimestampPosition 위치 문자열을 enum으로 변환한다.
func ParseTimestampPosition(s string) TimestampPosition {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "top-left":
		return TopLeft
	case "top-right":
		return TopRight
	case "bottom-right":
		return BottomRight
	default:
		return BottomLeft
	}
}

// DrawTimestamp 주어진 Mat에 현재 시각을 그린다. 입력은 BGR 3채널 8비트.
func DrawTimestamp(img *gocv.Mat, position TimestampPosition, format string) {
	if format == "" {
		format = "2006-01-02 15:04:05"
	}
	text := time.Now().Format(format)

	scale := 0.8
	thickness := 2
	font := gocv.FontHersheySimplex

	size := gocv.GetTextSize(text, font, scale, thickness)
	w := size.X
	h := size.Y

	rows, cols := img.Rows(), img.Cols()
	var x, y int
	switch position {
	case TopLeft:
		x, y = 10, h+10
	case TopRight:
		x, y = cols-w-10, h+10
	case BottomLeft:
		x, y = 10, rows-10
	case BottomRight:
		x, y = cols-w-10, rows-10
	}

	// 검정 그림자로 가독성 확보 후 흰색 텍스트.
	_ = gocv.PutText(img, text, image.Pt(x+1, y+1), font, scale,
		color.RGBA{0, 0, 0, 255}, thickness+2)
	_ = gocv.PutText(img, text, image.Pt(x, y), font, scale,
		color.RGBA{255, 255, 255, 255}, thickness)
}

// ApplyTransforms flip/mirror/rotation을 순서대로 적용한다.
// 원본은 변경하지 않고 결과를 dst에 쓴다.
func ApplyTransforms(src, dst *gocv.Mat, flip, mirror bool, rotation int) {
	// IPyCam 순서: flip -> mirror -> rotation (90/270이 차원을 바꿈).
	if flip {
		_ = gocv.Flip(*src, dst, 0) // vertical
		src = dst
	}
	if mirror {
		_ = gocv.Flip(*src, dst, 1) // horizontal
		src = dst
	}
	switch rotation {
	case 90:
		_ = gocv.Rotate(*src, dst, gocv.Rotate90Clockwise)
	case 180:
		_ = gocv.Rotate(*src, dst, gocv.Rotate180Clockwise)
	case 270:
		_ = gocv.Rotate(*src, dst, gocv.Rotate90CounterClockwise)
	default:
		if src != dst {
			src.CopyTo(dst)
		}
	}
}
