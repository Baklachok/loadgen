package stats

import (
	"context"
	"crypto/tls"
	"errors"
	"math"
	"net"
	"slices"
	"syscall"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

const histogramBuckets = 10

type ErrorKind string

const (
	ErrTimeout   ErrorKind = "timeout"
	ErrRefused   ErrorKind = "connection refused"
	ErrReset     ErrorKind = "connection reset"
	ErrDNS       ErrorKind = "dns"
	ErrTLS       ErrorKind = "tls"
	ErrCanceled  ErrorKind = "canceled"
	ErrOtherKind ErrorKind = "other"
)

type Latencies struct {
	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration
}

type Summary struct {
	// Три исхода, а не два. 429 и таймаут — разные события: первый означает,
	// что сервер работает и отказывает, второй — что ответа не было вовсе.
	// Слепить их вместе значит либо отчитаться об успехе там, где сервис
	// отдавал одни отказы, либо утопить перцентили в значении таймаута.
	Total  int // всего запросов
	OK     int // ответ 2xx
	NonOK  int // ответ получен, но не 2xx
	Failed int // ответа не было: таймаут, обрыв, отказ в соединении

	Elapsed time.Duration
	// RPS — полученных ответов в секунду, то есть пропускная способность.
	// Не «успешных в секунду»: 500-ка это тоже обслуженный запрос.
	RPS float64

	// Latency — время самих запросов, Corrected — оно же плюс отставание
	// старта от расписания. В closed-loop они совпадают; в open-loop расходятся
	// ровно настолько, насколько генератор не успевал за собственным планом,
	// и именно Corrected показывает, что видел бы клиент, шлющий по часам.
	Latency   Latencies
	Corrected Latencies
	MaxLag    time.Duration

	BytesRead  int64
	Throughput float64 // МБ/с

	// Histogram — распределение Latency по равным бакетам, для картинки в отчёте
	Histogram []Bucket

	// Trace — разбивка по фазам соединения; nil, если трассировку не включали
	Trace *TraceSummary

	Codes  map[int]int
	Errors map[ErrorKind]int
}

// Responses — сколько запросов получили ответ, любой.
func (s Summary) Responses() int { return s.OK + s.NonOK }

// SuccessRate — доля 2xx от всех запросов. Считается, а не хранится: поле
// рядом со счётчиками рано или поздно разойдётся с ними при правке.
func (s Summary) SuccessRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.OK) / float64(s.Total)
}

// isOK: успех — это 2xx. 3xx сюда не входит осознанно — редиректы мы не
// проходим, и 301 означает, что запрошенного ресурса по этому адресу нет.
func isOK(code int) bool { return code >= 200 && code < 300 }

// Bucket — один столбик гистограммы: сколько замеров попало в интервал,
// заканчивающийся на Upper.
type Bucket struct {
	Upper time.Duration
	Count int
}

func Compute(results []runner.Result, elapsed time.Duration) Summary {
	s := Summary{
		Total:   len(results),
		Elapsed: elapsed,
		Codes:   make(map[int]int),
		Errors:  make(map[ErrorKind]int),
	}

	var service, corrected samples

	for _, r := range results {
		s.MaxLag = max(s.MaxLag, r.Lag)

		if r.Err != nil {
			s.recordError(r.Err)
			continue
		}

		s.recordResponse(r)
		service.add(r.Duration)
		corrected.add(r.Lag + r.Duration)
	}

	// Перцентили — по всем полученным ответам: 503 за 2мс это настоящая работа
	// сервера, и прятать её нельзя. А вот таймауты сюда не попадают, иначе p99
	// схлопнется в значение -t и деградацию станет не видно.
	if s.Responses() > 0 {
		s.Latency = service.latencies()
		s.Corrected = corrected.latencies()
		s.Histogram = histogram(service.sorted(), histogramBuckets)
	}

	s.Trace = computeTrace(results)

	if elapsed > 0 {
		s.RPS = float64(s.Responses()) / elapsed.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / elapsed.Seconds()
	}

	return s
}

// recordError — ответа не было вовсе: таймаут, обрыв, отказ в соединении.
func (s *Summary) recordError(err error) {
	s.Failed++
	s.Errors[Classify(err)]++
}

// recordResponse — сервер ответил, и это результат независимо от кода.
func (s *Summary) recordResponse(r runner.Result) {
	if isOK(r.StatusCode) {
		s.OK++
	} else {
		s.NonOK++
	}

	s.Codes[r.StatusCode]++
	s.BytesRead += r.BytesRead
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

func Classify(err error) ErrorKind {
	switch {
	case errors.Is(err, context.Canceled):
		return ErrCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ErrRefused
	case errors.Is(err, syscall.ECONNRESET):
		return ErrReset
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrDNS
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return ErrTLS
	}

	return ErrOtherKind
}
