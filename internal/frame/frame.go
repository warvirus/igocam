// 참조 카운팅 프레임 래퍼 - 비동기 소비자 간 안전한 Mat 공유
package frame

import (
	"sync/atomic"

	"gocv.io/x/gocv"
)

// Frame 불변 아웃바운드 프레임. IPyCam의 "outbound immutability contract"를
// Go에서 구현한다: 생산자는 프레임마다 새 Mat을 만들고 1회 Retain, 각 소비자는
// 비동기 처리 전 Retain 후 Release. 마지막 Release가 Mat을 Close한다.
//
// 생산자 규칙: NewFrame(mat)로 생성 (refs=1, 생산자 소유). 소비자 큐에
// enqueue하기 전 Retain(), enqueue 직후 Release() (생산자 몫 반납).
// 소비자 규칙: 워커가 처리를 마치면 반드시 Release().
type Frame struct {
	Mat  *gocv.Mat
	refs atomic.Int64
}

// NewFrame 새 프레임 생성 (refs=1, 생산자 소유).
func NewFrame(mat *gocv.Mat) *Frame {
	f := &Frame{Mat: mat}
	f.refs.Store(1)
	return f
}

// Retain 참조 카운트 증가. 비동기 큐에 전달하기 전에 호출.
func (f *Frame) Retain() {
	if f == nil {
		return
	}
	f.refs.Add(1)
}

// Release 참조 카운트 감소. 0이 되면 Mat을 닫는다.
func (f *Frame) Release() {
	if f == nil {
		return
	}
	if f.refs.Add(-1) <= 0 {
		if f.Mat != nil {
			f.Mat.Close()
			f.Mat = nil
		}
	}
}
