package stats

import (
	"time"

	"github.com/HdrHistogram/hdrhistogram-go"
)

// Диапазон HDR. Нижняя граница — микросекунда: ниже неё loopback не бывает.
// Верхняя — 10 с, таймаут по умолчанию; замер выше него не теряется, см. add.
const (
	hdrLowest  = int64(time.Microsecond)
	hdrHighest = int64(10 * time.Second)

	// Три значащих цифры — 131 КБ фиксированной памяти и погрешность ниже
	// 0.1% (две дали бы 19 КБ, но +0.5% на p50). Порог закреплён
	// в TestHDRAccuracy: число там, а не здесь.
	hdrDigits = 3

	// hdrTolerance — обещанная погрешность перцентиля, долей от значения.
	// Следует из hdrDigits: 10^-3. Тесты берут допуск отсюда, а не из головы.
	hdrTolerance = 1e-3
)

// hdr — накопитель с фиксированной памятью. Перцентиль здесь — верхняя
// граница бакета шириной 0.1% от значения, а не конкретный замер. Это
// и есть цена: на трёх цифрах разница с ближайшим рангом ниже десятой
// процента, но «время реального запроса» она уже не показывает.
//
// min, max и среднее считаются мимо гистограммы. У HDR они тоже границы
// бакетов (три замера по 20 мс дают Min 19.988 и Mean 19.996), а держать
// точные стоит 24 байта — здесь квантизация ничем не оправдана.
type hdr struct {
	h *hdrhistogram.Histogram

	n        int
	min, max time.Duration
	total    time.Duration

	// Замеры за верхней границей диапазона. HDR их не принимает, а терять
	// молча нельзя: пропавший из max таймаут — худшее, что может сделать
	// нагрузочник. Копим счётчик и максимум руками.
	overflow    int
	overflowMax time.Duration
}

func newHDR() *hdr {
	return &hdr{h: hdrhistogram.New(hdrLowest, hdrHighest, hdrDigits)}
}

func (d *hdr) add(v time.Duration) {
	if d.n == 0 || v < d.min {
		d.min = v
	}
	d.max = max(d.max, v)
	d.total += v
	d.n++

	if err := d.h.RecordValue(int64(max(v, time.Duration(hdrLowest)))); err != nil {
		d.overflow++
		d.overflowMax = max(d.overflowMax, v)
	}
}

func (d *hdr) latencies() Latencies {
	if d.n == 0 {
		return Latencies{}
	}
	return Latencies{
		Samples: d.n,
		Min:     d.min,
		Max:     d.max,
		Mean:    d.total / time.Duration(d.n),
		P50:     d.quantile(50),
		P90:     d.quantile(90),
		P95:     d.quantile(95),
		P99:     d.quantile(99),
	}
}

// quantile — перцентиль q (в процентах). Если доля переполнения больше
// доли, которую перцентиль отсекает сверху (для p99 это 1%), сам перцентиль
// лежит уже за диапазоном — и HDR показал бы потолок в 10 с вместо
// настоящего хвоста.
func (d *hdr) quantile(q float64) time.Duration {
	tail := (100 - q) / 100
	if d.overflow > 0 && float64(d.overflow)/float64(d.n) > tail {
		return d.overflowMax
	}
	return time.Duration(d.h.ValueAtQuantile(q))
}

// histogram — та же шкала, что у точного накопителя; точки — бары HDR по их
// верхней границе, переполнение — одной точкой на overflowMax.
func (d *hdr) histogram(n int) []Bucket {
	if d.n == 0 {
		return nil
	}
	return bucketize(d.min, d.max, n, func(place func(time.Duration, int)) {
		for _, bar := range d.h.Distribution() {
			if bar.Count > 0 {
				place(time.Duration(bar.To), int(bar.Count))
			}
		}
		if d.overflow > 0 {
			place(d.overflowMax, d.overflow)
		}
	})
}
