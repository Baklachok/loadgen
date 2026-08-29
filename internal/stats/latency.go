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

	// Samples — по скольким замерам всё это посчитано. Без него перцентиль
	// невозможно оценить: p99 по пятидесяти замерам — это максимум,
	// названный красивым словом.
	Samples int
}

// Quantile — перцентиль, который инструмент считает и печатает: имя,
// сама квантиль и способ достать уже посчитанное значение.
//
// Имя хранится, а не выводится из числа: fmt.Sprintf("p%g", 0.9*100) даёт
// «p90.00000000000001», потому что 0.9*100 в двоичной арифметике не ровно 90.
type Quantile struct {
	Name  string
	Q     float64
	Value func(Latencies) time.Duration
}

// Quantiles — один список на весь проект. Раньше он был выписан дважды:
// в строках отчёта и в пояснении к прочеркам, и добавление перцентиля
// требовало не забыть про оба.
var Quantiles = []Quantile{
	{"p50", 0.50, func(l Latencies) time.Duration { return l.P50 }},
	{"p90", 0.90, func(l Latencies) time.Duration { return l.P90 }},
	{"p95", 0.95, func(l Latencies) time.Duration { return l.P95 }},
	{"p99", 0.99, func(l Latencies) time.Duration { return l.P99 }},
}

// MinSamples — сколько замеров нужно, чтобы перцентиль p что-то значил.
// Правило n >= 10/(1-p): десять наблюдений в самом хвосте. Отсюда p90
// требует 100 замеров, p95 — 200, p99 — 1000.
func MinSamples(p float64) int {
	if p <= 0 || p >= 1 {
		return 1
	}
	// Эпсилон обязателен: 1-0.9 в двоичной арифметике даёт 0.09999999999999998,
	// и Ceil честно округляет получившиеся 100.00000000000001 до 101.
	return int(math.Ceil(10/(1-p) - 1e-9))
}

// Reliable сообщает, набралось ли замеров на осмысленный перцентиль p.
func (l Latencies) Reliable(p float64) bool {
	return l.Samples >= MinSamples(p)
}

// distribution — накопитель замеров, который умеет отдать перцентили
// и гистограмму. Это шов, о котором говорил хендофф: реализаций две —
// точная samples и HDR с фиксированной памятью, — и замена одной на другую
// не должна трогать ничего, кроме конструктора в Accumulator.
//
// Гистограмма считается внутри накопителя намеренно. Раньше Accumulator
// доставал сырой отсортированный слайс и строил её сам — а у HDR никакого
// слайса нет, и ровно эта строка расползлась бы при замене.
type distribution interface {
	add(time.Duration)
	latencies() Latencies
	histogram(n int) []Bucket
}

// samples копит замеры для перцентилей — точно, без округления. Сумму держим
// рядом, чтобы среднее не считать вторым проходом по миллионам значений.
//
// Остаётся в коде рядом с HDR как эталон: по ней меряется его погрешность,
// и на ней держатся тесты перцентилей и гистограммы.
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
		Samples: len(sorted),
		Min:     sorted[0],
		Max:     sorted[len(sorted)-1],
		Mean:    s.total / time.Duration(len(sorted)),
		P50:     Percentile(sorted, 0.50),
		P90:     Percentile(sorted, 0.90),
		P95:     Percentile(sorted, 0.95),
		P99:     Percentile(sorted, 0.99),
	}
}

// histogram — распределение по n равным бакетам.
func (s *samples) histogram(n int) []Bucket {
	return histogram(s.sorted(), n)
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
	if len(sorted) == 0 {
		return nil
	}
	return bucketize(sorted[0], sorted[len(sorted)-1], n, func(place func(time.Duration, int)) {
		for _, d := range sorted {
			place(d, 1)
		}
	})
}

// bucketize — каркас гистограммы, общий для точного и HDR-накопителя:
// n равных бакетов от lo до hi, а точки в них кладёт вызывающий через place.
// Раньше каркас был выписан дважды и отличался только этим циклом.
func bucketize(lo, hi time.Duration, n int, feed func(place func(v time.Duration, count int))) []Bucket {
	if n < 1 {
		return nil
	}
	if lo == hi {
		total := 0
		feed(func(_ time.Duration, c int) { total += c })
		return []Bucket{{Upper: hi, Count: total}}
	}

	width := float64(hi-lo) / float64(n)
	buckets := make([]Bucket, n)
	for i := range buckets {
		buckets[i].Upper = lo + time.Duration(float64(i+1)*width)
	}
	buckets[n-1].Upper = hi // накопленное округление не должно отсечь максимум

	// Точки идут по возрастанию у обоих источников, поэтому курсор b только
	// растёт; точка выше hi (бар HDR шириной в бакет) прижимается к последнему.
	b := 0
	feed(func(v time.Duration, count int) {
		for b < n-1 && v > buckets[b].Upper {
			b++
		}
		buckets[b].Count += count
	})
	return buckets
}
