package stats

import (
	"testing"
	"time"
)

// hdr обязан вести себя как samples везде, где это можно проверить точно:
// счётчик, min, max, и перцентили на выборке, где ответ известен заранее.
func TestHDR(t *testing.T) {
	t.Run("пусто — нули", func(t *testing.T) {
		if got := newHDR().latencies(); got != (Latencies{}) {
			t.Errorf("на пустом накопителе %+v, ожидались нули", got)
		}
		if got := newHDR().histogram(10); got != nil {
			t.Errorf("гистограмма на пустом %v, ожидался nil", got)
		}
	})

	// 1..1000 мкс: ближайший ранг даёт p50=500, p90=900, p95=950, p99=990,
	// и три значащих цифры обязаны попасть в них ровно.
	t.Run("перцентили на 1..1000 мкс совпадают с ближайшим рангом", func(t *testing.T) {
		d := newHDR()
		for i := 1; i <= 1000; i++ {
			d.add(time.Duration(i) * time.Microsecond)
		}
		l := d.latencies()

		for _, tt := range []struct {
			name string
			got  time.Duration
			want int
		}{
			{"p50", l.P50, 500}, {"p90", l.P90, 900}, {"p95", l.P95, 950}, {"p99", l.P99, 990},
			{"min", l.Min, 1}, {"max", l.Max, 1000},
		} {
			if got := int(tt.got / time.Microsecond); got != tt.want {
				t.Errorf("%s = %d мкс, ожидалось %d", tt.name, got, tt.want)
			}
		}
		if l.Samples != 1000 {
			t.Errorf("Samples = %d, ожидалось 1000", l.Samples)
		}
	})

	// Таймаут выше диапазона — худшее, что можно потерять: пропавший из max
	// хвост делает отчёт ложью. Он обязан остаться и в max, и в счётчике.
	t.Run("замер выше диапазона не теряется", func(t *testing.T) {
		d := newHDR()
		for i := 0; i < 99; i++ {
			d.add(5 * time.Millisecond)
		}
		d.add(30 * time.Second) // -t 30s, больше hdrHighest

		l := d.latencies()
		if l.Samples != 100 {
			t.Errorf("Samples = %d, ожидалось 100: переполнение выпало из счёта", l.Samples)
		}
		if l.Max != 30*time.Second {
			t.Errorf("Max = %v, ожидалось 30s: таймаут потерян", l.Max)
		}
		// Один хвост не двигает медиану; допуск — квантизация 0.1%, не более.
		if l.P50 < 5*time.Millisecond || l.P50 > 5*time.Millisecond+5*time.Microsecond {
			t.Errorf("p50 = %v, ожидалось 5ms±0.1%%: один хвост не должен двигать медиану", l.P50)
		}
	})

	t.Run("хвост целиком за диапазоном виден в p99", func(t *testing.T) {
		d := newHDR()
		for i := 0; i < 90; i++ {
			d.add(time.Millisecond)
		}
		for i := 0; i < 10; i++ {
			d.add(20 * time.Second)
		}
		if l := d.latencies(); l.P99 != 20*time.Second {
			t.Errorf("p99 = %v, ожидалось 20s: десять процентов таймаутов обязаны быть в p99", l.P99)
		}
	})

	t.Run("гистограмма — n бакетов, сумма равна числу замеров", func(t *testing.T) {
		d := newHDR()
		for i := 1; i <= 1000; i++ {
			d.add(time.Duration(i) * time.Microsecond)
		}
		buckets := d.histogram(10)

		if len(buckets) != 10 {
			t.Fatalf("бакетов %d, ожидалось 10", len(buckets))
		}
		total := 0
		for _, b := range buckets {
			total += b.Count
		}
		if total != 1000 {
			t.Errorf("в бакетах %d замеров, ожидалось 1000", total)
		}
		if buckets[9].Upper != time.Millisecond {
			t.Errorf("последний бакет кончается на %v, ожидалось max=1ms", buckets[9].Upper)
		}
	})
}
