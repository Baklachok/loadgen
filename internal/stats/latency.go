// Распределение задержек: накопление замеров, перцентили и бакеты
// гистограммы. Ничего не знает ни о запросах, ни об ошибках.
package stats

import (
	"math"
	"slices"
	"time"
)

const histogramBuckets = 10

type Latencies struct {
	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration
}

// samples копит замеры для перцентилей. Сумму держим рядом, чтобы среднее
// не считать вторым проходом по миллионам значений.
type samples struct {
	values []time.Duration
	total  time.Duration
}

func (s *samples) add(d time.Duration) {
	s.values = append(s.values, d)
	s.total += d
}

// sorted сортирует на месте: упорядоченный слайс нужен и перцентилям,
// и гистограмме, а копировать его ради этого незачем. Идемпотентна.
func (s *samples) sorted() []time.Duration {
	slices.Sort(s.values)
	return s.values
}

// latencies на пустом наборе возвращает нули — «замеров не было».
func (s *samples) latencies() Latencies {
	sorted := s.sorted()
	if len(sorted) == 0 {
		return Latencies{}
	}

	return Latencies{
		Min:  sorted[0],
		Max:  sorted[len(sorted)-1],
		Mean: s.total / time.Duration(len(sorted)),
		P50:  Percentile(sorted, 0.50),
		P90:  Percentile(sorted, 0.90),
		P95:  Percentile(sorted, 0.95),
		P99:  Percentile(sorted, 0.99),
	}
}

// Percentile ожидает отсортированный слайс.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}

// Bucket — один столбик гистограммы: сколько замеров попало в интервал,
// заканчивающийся на Upper.
type Bucket struct {
	Upper time.Duration
	Count int
}

// histogram раскладывает уже отсортированные замеры по n равным по ширине
// бакетам. Шкала линейная: на длинном хвосте это даёт пустоту между горбами,
// но именно она и показывает, что распределение не одно, а два.
func histogram(sorted []time.Duration, n int) []Bucket {
	if len(sorted) == 0 || n < 1 {
		return nil
	}

	lo, hi := sorted[0], sorted[len(sorted)-1]
	if lo == hi {
		return []Bucket{{Upper: hi, Count: len(sorted)}}
	}

	width := float64(hi-lo) / float64(n)
	buckets := make([]Bucket, n)
	for i := range buckets {
		buckets[i].Upper = lo + time.Duration(float64(i+1)*width)
	}
	buckets[n-1].Upper = hi // накопленное округление не должно отсечь максимум

	b := 0
	for _, d := range sorted {
		for b < n-1 && d > buckets[b].Upper {
			b++
		}
		buckets[b].Count++
	}
	return buckets
}
