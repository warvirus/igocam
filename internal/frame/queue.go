// 유계 drop-oldest 프레임 큐 - 생산자/소비자 비동기 파이프라인 기본 원시 타입
package frame

import (
	"sync/atomic"
	"time"
)

// Queue 유계 drop-oldest 프레임 큐.
//
// Put은 절대 블로킹하지 않는다. 큐가 가득 차면 가장 오래된 항목을 제거하고
// 드롭 카운트를 올린다. Get은 항목이 생길 때까지 블로킹한다(timeout 지정 가능).
// "latest-wins" 의미를 제공해 라이브 비디오 파이프라인에서 정체된 소비자가
// 생산자에 역압력을 주지 못하게 한다.
type Queue struct {
	items   chan any
	dropped atomic.Int64
	closed  atomic.Bool
	// OnDrop은 항목이 드롭될 때 호출된다 (참조 카운팅 프레임의 Release 등).
	OnDrop func(any)
}

// NewQueue 최대 maxsize 항목을 담는 큐 생성 (maxsize >= 1).
func NewQueue(maxsize int) *Queue {
	if maxsize < 1 {
		maxsize = 2
	}
	return &Queue{items: make(chan any, maxsize)}
}

// Put 항목을 블로킹 없이 추가. 가득 차면 가장 오래된 항목을 제거한다.
// 닫힌 큐에는 저장하지 않고, 항목이 소비자에게 넘어가지 않으므로 OnDrop을
// 호출해 호출자가 보유한 참조(예: frame.Frame의 Retain)가 누수되지 않게 한다.
// 반환값: 저장했으면 true, 항목을 버렸으면 false.
func (q *Queue) Put(item any) bool {
	if q.closed.Load() {
		q.dropped.Add(1)
		if q.OnDrop != nil {
			q.OnDrop(item)
		}
		return false
	}
	select {
	case q.items <- item:
		return true
	default:
		// 가득 참: 가장 오래된 항목을 버려 공간을 만들고 드롭을 센다.
		select {
		case evicted := <-q.items:
			q.dropped.Add(1)
			if q.OnDrop != nil {
				q.OnDrop(evicted)
			}
		default:
		}
		select {
		case q.items <- item:
		default:
			q.dropped.Add(1)
			if q.OnDrop != nil {
				q.OnDrop(item)
			}
			return false
		}
		return false
	}
}

// Get 항목이 준비될 때까지 블로킹 후 가장 오래된 항목 반환.
// timeout이 0이면 무기한 대기. 닫힌 큐가 비어 있으면 nil 반환.
func (q *Queue) Get(timeout time.Duration) any {
	if timeout <= 0 {
		item, ok := <-q.items
		if !ok {
			return nil
		}
		return item
	}
	select {
	case item, ok := <-q.items:
		if !ok {
			return nil
		}
		return item
	case <-time.After(timeout):
		return nil
	}
}

// GetLatest Get과 같지만 버퍼링된 항목 중 가장 최신 것만 반환하고
// 그 앞의 백로그는 드롭으로 간주해 버린다.
func (q *Queue) GetLatest(timeout time.Duration) any {
	var newest any
	received := 0
	for {
		// 항목이 즉시 있으면 계속 따라간다.
		select {
		case item, ok := <-q.items:
			if !ok {
				return q.finishLatest(newest, received)
			}
			newest = item
			received++
			continue
		default:
		}
		// 즉시 사용할 수 있는 항목이 더 이상 없음.
		if received > 0 {
			return q.finishLatest(newest, received)
		}
		// 아직 한 개도 못 받음: 블로킹 대기.
		if timeout <= 0 {
			item, ok := <-q.items
			if !ok {
				return nil
			}
			newest = item
			received++
			continue
		}
		select {
		case item, ok := <-q.items:
			if !ok {
				return q.finishLatest(newest, received)
			}
			newest = item
			received++
		case <-time.After(timeout):
			return q.finishLatest(newest, received)
		}
	}
}

// finishLatest 최신 항목 하나를 제외한 나머지를 드롭으로 세고 반환한다.
func (q *Queue) finishLatest(newest any, received int) any {
	if received == 0 {
		return nil
	}
	if received > 1 {
		q.dropped.Add(int64(received - 1))
	}
	return newest
}

// Close 큐를 닫고 대기 중인 모든 소비자를 깨운다.
// 닫힌 후 Put은 항목을 버리고 false를 반환한다.
func (q *Queue) Close() {
	if q.closed.CompareAndSwap(false, true) {
		close(q.items)
	}
}

// Closed 큐가 닫혔는지 여부.
func (q *Queue) Closed() bool {
	return q.closed.Load()
}

// Dropped 큐가 가득 차서 버린 총 항목 수.
func (q *Queue) Dropped() int64 {
	return q.dropped.Load()
}

// QSize 현재 버퍼링된 항목 수(동시성 하에서는 근사치).
func (q *Queue) QSize() int {
	return len(q.items)
}
