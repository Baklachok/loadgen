package stats

import (
	"context"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

// ms — короткая запись для длительностей: тесты здесь про соотношения
// значений, а не про time.Duration.
func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func TestPercentile(t *testing.T) {

	tests := []struct {
		name   string
		sorted []time.Duration
		p      float64
		want   time.Duration
	}{
		{"empty", nil, 0.5, 0},
		{"single p50", []time.Duration{ms(5)}, 0.50, ms(5)},
		{"single p99", []time.Duration{ms(5)}, 0.99, ms(5)},
		{"ten p50", tenMs(), 0.50, ms(5)},
		{"ten p90", tenMs(), 0.90, ms(9)},
		{"ten p95", tenMs(), 0.95, ms(10)},
		{"ten p99", tenMs(), 0.99, ms(10)},
		{"p0 is min", tenMs(), 0.0, ms(1)},
		{"p1 is max", tenMs(), 1.0, ms(10)},
		{"identical", []time.Duration{ms(3), ms(3), ms(3)}, 0.99, ms(3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Percentile(tt.sorted, tt.p)
			if got != tt.want {
				t.Errorf("Percentile(%v, %.2f) = %v, want %v", tt.sorted, tt.p, got, tt.want)
			}
		})
	}
}

func tenMs() []time.Duration {
	d := make([]time.Duration, 10)
	for i := range d {
		d[i] = time.Duration(i+1) * time.Millisecond
	}
	return d
}

func TestCompute(t *testing.T) {

	results := []runner.Result{
		{Duration: ms(10), StatusCode: 200, BytesRead: 100},
		{Duration: ms(20), StatusCode: 200, BytesRead: 100},
		{Duration: ms(30), StatusCode: 500, BytesRead: 50},
		{Duration: ms(5), Err: context.DeadlineExceeded},
	}

	s := Compute(results, 2*time.Second)

	if s.Total != 4 || s.Success != 3 || s.Failed != 1 {
		t.Errorf("counts: total=%d success=%d failed=%d", s.Total, s.Success, s.Failed)
	}
	if s.RPS != 1.5 {
		t.Errorf("RPS = %v, want 1.5", s.RPS)
	}
	if s.Latency.Mean != ms(20) {
		t.Errorf("Mean = %v, want 20ms", s.Latency.Mean)
	}
	if s.Codes[200] != 2 || s.Codes[500] != 1 {
		t.Errorf("codes = %v", s.Codes)
	}
	if s.Errors[ErrTimeout] != 1 {
		t.Errorf("errors = %v", s.Errors)
	}
}

func TestHistogram(t *testing.T) {

	t.Run("пустой вход", func(t *testing.T) {
		if got := histogram(nil, 10); got != nil {
			t.Errorf("histogram(nil) = %v, want nil", got)
		}
	})

	t.Run("все замеры одинаковые", func(t *testing.T) {
		got := histogram([]time.Duration{ms(5), ms(5), ms(5)}, 10)
		if len(got) != 1 || got[0].Count != 3 || got[0].Upper != ms(5) {
			t.Errorf("got %+v, want один бакет на 3 замера", got)
		}
	})

	t.Run("ни один замер не потерян", func(t *testing.T) {
		sorted := make([]time.Duration, 0, 1000)
		for i := 1; i <= 1000; i++ {
			sorted = append(sorted, time.Duration(i)*time.Microsecond)
		}

		buckets := histogram(sorted, 10)
		if len(buckets) != 10 {
			t.Fatalf("бакетов %d, want 10", len(buckets))
		}

		total := 0
		for _, b := range buckets {
			total += b.Count
		}
		if total != len(sorted) {
			t.Errorf("сумма счётчиков %d, замеров %d", total, len(sorted))
		}
	})

	t.Run("границы растут и накрывают максимум", func(t *testing.T) {
		buckets := histogram([]time.Duration{ms(1), ms(3), ms(7), ms(200)}, 4)

		for i := 1; i < len(buckets); i++ {
			if buckets[i].Upper <= buckets[i-1].Upper {
				t.Errorf("граница %d (%v) не больше предыдущей (%v)", i, buckets[i].Upper, buckets[i-1].Upper)
			}
		}
		if last := buckets[len(buckets)-1].Upper; last != ms(200) {
			t.Errorf("последняя граница %v, want 200ms: максимум обязан попасть внутрь", last)
		}
	})

	t.Run("длинный хвост виден как разрыв", func(t *testing.T) {
		// 99 быстрых замеров и один медленный: линейная шкала должна оставить
		// пустые бакеты между ними, а не размазать выброс
		sorted := make([]time.Duration, 0, 100)
		for i := 0; i < 99; i++ {
			sorted = append(sorted, ms(5))
		}
		sorted = append(sorted, ms(400))

		buckets := histogram(sorted, 10)
		if buckets[0].Count != 99 {
			t.Errorf("первый бакет = %d, want 99", buckets[0].Count)
		}
		if buckets[len(buckets)-1].Count != 1 {
			t.Errorf("последний бакет = %d, want 1", buckets[len(buckets)-1].Count)
		}

		empty := 0
		for _, b := range buckets {
			if b.Count == 0 {
				empty++
			}
		}
		if empty == 0 {
			t.Error("между горбами нет пустых бакетов — разрыв не читается")
		}
	})
}

func TestComputeFillsHistogram(t *testing.T) {
	results := []runner.Result{
		{Duration: 10 * time.Millisecond, StatusCode: 200},
		{Duration: 20 * time.Millisecond, StatusCode: 200},
		{Duration: 5 * time.Millisecond, Err: context.DeadlineExceeded},
	}

	s := Compute(results, time.Second)

	total := 0
	for _, b := range s.Histogram {
		total += b.Count
	}
	if total != s.Success {
		t.Errorf("в гистограмме %d замеров, успешных запросов %d", total, s.Success)
	}
}

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
