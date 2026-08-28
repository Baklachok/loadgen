package slo

// ms — короткая запись длительности: у пакета свои тесты, и хелпер из stats
// сюда не дотягивается.

import (
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// Порог, который не задан, не должен ничего проверять: иначе прогон без
// -slo-* начнёт падать сам собой.
func TestEmptyChecksNothing(t *testing.T) {
	empty := Thresholds{ErrorRate: -1}

	if !empty.Empty() {
		t.Error("SLO без порогов не считается пустым")
	}
	if v := empty.Check(stats.Summary{Total: 10, OK: 0}); len(v) != 0 {
		t.Errorf("незаданные пороги дали нарушения: %+v", v)
	}
}

func TestP99(t *testing.T) {
	// Тысяча замеров — ровно порог достоверности p99
	sample := func(p99 time.Duration) stats.Summary {
		return stats.Summary{
			Total: 1000, OK: 1000,
			Corrected: stats.Latencies{Samples: 1000, P99: p99},
		}
	}

	t.Run("уложились", func(t *testing.T) {
		if v := (Thresholds{P99: ms(200), ErrorRate: -1}).Check(sample(ms(150))); len(v) != 0 {
			t.Errorf("нарушение при p99 ниже порога: %+v", v)
		}
	})

	t.Run("не уложились", func(t *testing.T) {
		v := (Thresholds{P99: ms(200), ErrorRate: -1}).Check(sample(ms(312)))
		if len(v) != 1 || v[0].Metric != "p99" || v[0].Unmeasured {
			t.Fatalf("ожидалось одно нарушение p99, получено %+v", v)
		}
		if v[0].Got != "312ms" {
			t.Errorf("Got = %q", v[0].Got)
		}
	})

	// Молча пропустить проверку значит вернуть зелёный CI на прогоне,
	// который поставленного вопроса не решил.
	t.Run("замеров не хватило на p99", func(t *testing.T) {
		small := sample(ms(150))
		small.Corrected.Samples = 50

		v := (Thresholds{P99: ms(200), ErrorRate: -1}).Check(small)
		if len(v) != 1 || !v[0].Unmeasured {
			t.Fatalf("ожидалась пометка «не измерено», получено %+v", v)
		}
		if !Unmeasured(v) {
			t.Error("Unmeasured не видит непроверенный порог")
		}
	})
}

func TestErrorRate(t *testing.T) {
	sample := func(ok, total int) stats.Summary { return stats.Summary{Total: total, OK: ok} }

	t.Run("уложились", func(t *testing.T) {
		// 995 из 1000 — 0.5% ошибок при пороге 1%
		if v := (Thresholds{ErrorRate: 0.01}).Check(sample(995, 1000)); len(v) != 0 {
			t.Errorf("нарушение при доле ниже порога: %+v", v)
		}
	})

	t.Run("не уложились", func(t *testing.T) {
		v := (Thresholds{ErrorRate: 0.01}).Check(sample(950, 1000))
		if len(v) != 1 || v[0].Metric != "error-rate" {
			t.Fatalf("ожидалось нарушение error-rate, получено %+v", v)
		}
		if v[0].Got != "5.00%" {
			t.Errorf("Got = %q, ожидалось 5.00%%", v[0].Got)
		}
	})

	// Не-2xx считается ошибкой наравне с отсутствием ответа: 429 в пайплайне
	// такой же провал, как таймаут.
	t.Run("не-2xx считается ошибкой", func(t *testing.T) {
		s := stats.Summary{Total: 100, OK: 0, NonOK: 100}

		if v := (Thresholds{ErrorRate: 0.5}).Check(s); len(v) != 1 {
			t.Errorf("поток 429 не нарушил порог ошибок: %+v", v)
		}
	})

	t.Run("ноль — осмысленный порог", func(t *testing.T) {
		clean := (Thresholds{ErrorRate: 0}).Check(sample(100, 100))
		dirty := (Thresholds{ErrorRate: 0}).Check(sample(99, 100))

		if len(clean) != 0 {
			t.Errorf("чистый прогон нарушил нулевой порог: %+v", clean)
		}
		if len(dirty) != 1 {
			t.Errorf("одна ошибка не нарушила нулевой порог: %+v", dirty)
		}
	})
}
