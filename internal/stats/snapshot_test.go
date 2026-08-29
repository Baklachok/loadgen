package stats

import (
	"sync"
	"testing"
	"time"
)

// Первый параллельный читатель аккумулятора — эндпоинт /metrics. До него
// Add был единственным, кто трогал счётчики, и гонки было некому поймать.
//
// Детектор гонок ловит map стабильно только при итерации, поэтому читатель
// перебирает Codes, а писатель добавляет новые ключи — это заставляет map
// расти под чтением. Чтение через range обязательно: точечный Codes[200]
// детектор пропускал на сотнях итераций.
func TestSnapshotIsSafeUnderConcurrentAdd(t *testing.T) {
	acc := NewAccumulator(0)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Писатель без пауз: детектор ловит по факту совпавших доступов, а не
	// по потенциалу, и вялый писатель может ни разу не попасть под чтение.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for code := 100; ; code++ {
			select {
			case <-stop:
				return
			default:
			}
			for i := 0; i < 64; i++ {
				acc.Add(resp(code, time.Millisecond))
			}
		}
	}()

	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		s := acc.Snapshot()
		n := 0
		for range s.Codes {
			n++
		}
		if n > s.Total {
			t.Fatalf("кодов %d больше, чем запросов %d: снимок несогласован", n, s.Total)
		}
	}
	close(stop)
	wg.Wait()
}

// Снимок — копия, а не окно: правка снимка не должна доехать до аккумулятора,
// иначе читатель /metrics, ничего не желая, портит итоговый отчёт.
func TestSnapshotIsDeepCopy(t *testing.T) {
	acc := NewAccumulator(0)
	acc.Add(resp(200, time.Millisecond))
	acc.Add(failed(errTimeout, time.Millisecond))

	s := acc.Snapshot()
	s.Codes[200] = 999
	s.Errors[ErrTimeout] = 999

	again := acc.Snapshot()
	if again.Codes[200] != 1 || again.Errors[ErrTimeout] != 1 {
		t.Errorf("правка снимка видна аккумулятору: codes=%v errors=%v", again.Codes, again.Errors)
	}
}

var errTimeout = timeoutErr{}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return false }
