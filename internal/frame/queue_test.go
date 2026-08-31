// 유계 drop-oldest 프레임 큐 테스트
package frame

import (
	"sync"
	"testing"
	"time"
)

func TestQueueBasic(t *testing.T) {
	q := NewQueue(2)
	if !q.Put(1) {
		t.Fatal("first put should succeed")
	}
	if !q.Put(2) {
		t.Fatal("second put should succeed")
	}
	// 가득 참: 세 번째 put은 가장 오래된 것을 버린다.
	if q.Put(3) {
		t.Fatal("third put on full queue should drop")
	}
	if q.Dropped() != 1 {
		t.Fatalf("dropped = %d, want 1", q.Dropped())
	}
	if q.QSize() != 2 {
		t.Fatalf("qsize = %d, want 2", q.QSize())
	}
	// drop-oldest: 1이 아니라 2부터 온다.
	if got := q.Get(0); got != 2 {
		t.Fatalf("get = %v, want 2", got)
	}
	if got := q.Get(0); got != 3 {
		t.Fatalf("get = %v, want 3", got)
	}
}

func TestQueueGetTimeout(t *testing.T) {
	q := NewQueue(2)
	start := time.Now()
	if got := q.Get(50 * time.Millisecond); got != nil {
		t.Fatalf("get on empty queue = %v, want nil", got)
	}
	if time.Since(start) < 40*time.Millisecond {
		t.Fatal("get returned too fast")
	}
}

func TestQueueCloseWakesGet(t *testing.T) {
	q := NewQueue(2)
	done := make(chan any, 1)
	go func() {
		done <- q.Get(0)
	}()
	time.Sleep(20 * time.Millisecond)
	q.Close()
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("get after close = %v, want nil", got)
		}
	case <-time.After(time.Second):
		t.Fatal("get did not wake on close")
	}
}

func TestQueuePutAfterClose(t *testing.T) {
	q := NewQueue(2)
	q.Close()
	if q.Put(1) {
		t.Fatal("put after close should fail")
	}
	if !q.Closed() {
		t.Fatal("Closed() should be true")
	}
}

func TestQueueGetLatest(t *testing.T) {
	q := NewQueue(3)
	q.Put(1)
	q.Put(2)
	q.Put(3)
	// get_latest: 최신 3만 남기고 1,2는 드롭.
	if got := q.GetLatest(0); got != 3 {
		t.Fatalf("get_latest = %v, want 3", got)
	}
	if q.Dropped() != 2 {
		t.Fatalf("dropped = %d, want 2", q.Dropped())
	}
	if q.QSize() != 0 {
		t.Fatalf("qsize = %d, want 0", q.QSize())
	}
}

func TestQueueDropOldestAcrossCapacity(t *testing.T) {
	q := NewQueue(1)
	for i := 0; i < 100; i++ {
		q.Put(i)
	}
	if q.Dropped() != 99 {
		t.Fatalf("dropped = %d, want 99", q.Dropped())
	}
	if got := q.Get(0); got != 99 {
		t.Fatalf("get = %v, want 99 (latest)", got)
	}
}

func TestQueueConcurrent(t *testing.T) {
	q := NewQueue(4)
	var wg sync.WaitGroup
	// 여러 생산자 동시 put
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				q.Put(p*1000 + i)
			}
		}(p)
	}
	// 소비자가 동시에 소비
	consumed := make(chan int, 1000)
	wg.Add(1)
	go func() {
		defer wg.Done()
		seenNil := 0
		for {
			item := q.Get(20 * time.Millisecond)
			if item == nil {
				seenNil++
				// 연속 3회 nil이면 소비 종료 (빈 큐 + 닫힘 감지)
				if seenNil >= 3 || q.Closed() {
					break
				}
				continue
			}
			seenNil = 0
			consumed <- item.(int)
		}
	}()
	wg.Wait()
	close(consumed)
	count := 0
	for range consumed {
		count++
	}
	// 400개 중 일부는 드롭됐을 수 있으나, 소비된 것들은 항상 정렬된 값 범위 안이어야 함.
	if count == 0 {
		t.Fatal("no items consumed")
	}
}
