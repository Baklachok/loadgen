package stats

import (
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

func TestComputeTrace(t *testing.T) {

	t.Run("без трассировки — nil", func(t *testing.T) {
		s := Compute([]runner.Result{{Duration: ms(1), StatusCode: 200}}, time.Second)
		if s.Trace != nil {
			t.Errorf("Trace = %+v, ожидался nil: отчёт должен отличать «не измеряли» от «фаз не было»", s.Trace)
		}
	})

	t.Run("нулевые фазы не попадают в перцентили", func(t *testing.T) {
		// Первый запрос соединялся, три следующих взяли соединение из пула:
		// у них DNS/Connect/TLS равны нулю, потому что их не было.
		results := []runner.Result{
			{Duration: ms(50), StatusCode: 200, Trace: &runner.Trace{DNS: ms(4), Connect: ms(6), TLS: ms(10), TTFB: ms(40)}},
			{Duration: ms(20), StatusCode: 200, Trace: &runner.Trace{TTFB: ms(18), Reused: true}},
			{Duration: ms(22), StatusCode: 200, Trace: &runner.Trace{TTFB: ms(20), Reused: true}},
			{Duration: ms(24), StatusCode: 200, Trace: &runner.Trace{TTFB: ms(22), Reused: true}},
		}

		s := Compute(results, time.Second)
		if s.Trace == nil {
			t.Fatal("Trace = nil")
		}

		if s.Trace.Traced != 4 || s.Trace.Reused != 3 {
			t.Errorf("traced=%d reused=%d, ожидалось 4 и 3", s.Trace.Traced, s.Trace.Reused)
		}
		if s.Trace.Connect.Count != 1 {
			t.Errorf("Connect.Count = %d, ожидался 1: три запроса не соединялись вовсе", s.Trace.Connect.Count)
		}
		// Если бы нули учитывались, среднее было бы 1.5мс вместо 6мс
		if s.Trace.Connect.Mean != ms(6) {
			t.Errorf("Connect.Mean = %v, ожидалось 6ms", s.Trace.Connect.Mean)
		}
		if s.Trace.TTFB.Count != 4 {
			t.Errorf("TTFB.Count = %d, ожидалось 4: первый байт получили все", s.Trace.TTFB.Count)
		}
	})

	t.Run("фаза без единого замера остаётся пустой", func(t *testing.T) {
		results := []runner.Result{
			{Duration: ms(5), StatusCode: 200, Trace: &runner.Trace{TTFB: ms(4), Reused: true}},
		}

		s := Compute(results, time.Second)
		if s.Trace.TLS.Count != 0 || s.Trace.TLS.P99 != 0 {
			t.Errorf("TLS = %+v, ожидалась пустая фаза: по HTTP рукопожатия нет", s.Trace.TLS)
		}
	})
}

// Три исхода — единственное место, где отчёт может выглядеть рабочим и врать,
// поэтому проверки собраны вместе.

// Прогрев делает рукопожатия — и именно поэтому его нельзя оставлять в фазах:
// шапка отчёта называет эти запросы отброшенными.
func TestComputeTraceExcludesWarmup(t *testing.T) {
	warm := resp(200, ms(80))
	warm.Warmup = true
	warm.Trace = &runner.Trace{Connect: ms(50), TTFB: ms(30)}

	measured := resp(200, ms(5))
	measured.Trace = &runner.Trace{TTFB: ms(4), Reused: true}

	s := Compute([]runner.Result{warm, measured}, time.Second)

	if s.Trace.Traced != 1 {
		t.Errorf("Traced = %d, ожидался 1: прогрев не измеряется", s.Trace.Traced)
	}
	if s.Trace.Connect.Count != 0 {
		t.Errorf("Connect.Count = %d, ожидался 0: рукопожатие сделал прогрев", s.Trace.Connect.Count)
	}
	if s.Trace.TTFB.P50 != ms(4) {
		t.Errorf("TTFB p50 = %v, ожидалось 4ms", s.Trace.TTFB.P50)
	}
}
