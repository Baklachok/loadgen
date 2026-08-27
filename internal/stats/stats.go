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
	Total   int
	Success int
	Failed  int

	Elapsed time.Duration
	RPS     float64

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

	Codes  map[int]int
	Errors map[ErrorKind]int
}

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

	durations := make([]time.Duration, 0, len(results))
	corrected := make([]time.Duration, 0, len(results))
	var total, totalCorrected time.Duration

	for _, r := range results {
		if r.Lag > s.MaxLag {
			s.MaxLag = r.Lag
		}
		if r.Err != nil {
			s.Failed++
			s.Errors[Classify(r.Err)]++
			continue
		}
		s.Success++
		s.Codes[r.StatusCode]++
		s.BytesRead += r.BytesRead
		durations = append(durations, r.Duration)
		corrected = append(corrected, r.Lag+r.Duration)
		total += r.Duration
		totalCorrected += r.Lag + r.Duration
	}

	if s.Success > 0 {
		s.Latency = summarize(durations, total)
		s.Corrected = summarize(corrected, totalCorrected)
		s.Histogram = histogram(durations, histogramBuckets)
	}

	if elapsed > 0 {
		s.RPS = float64(s.Success) / elapsed.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / elapsed.Seconds()
	}

	return s
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

// summarize сортирует xs на месте. Вызывается только при len(xs) > 0.
func summarize(xs []time.Duration, total time.Duration) Latencies {
	slices.Sort(xs)
	return Latencies{
		Min:  xs[0],
		Max:  xs[len(xs)-1],
		Mean: total / time.Duration(len(xs)),
		P50:  Percentile(xs, 0.50),
		P90:  Percentile(xs, 0.90),
		P95:  Percentile(xs, 0.95),
		P99:  Percentile(xs, 0.99),
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
