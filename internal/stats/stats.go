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

type Summary struct {
	Total   int
	Success int
	Failed  int

	Elapsed time.Duration
	RPS     float64

	Min, Mean, Max     time.Duration
	P50, P90, P95, P99 time.Duration

	BytesRead  int64
	Throughput float64 // МБ/с

	Codes  map[int]int
	Errors map[ErrorKind]int
}

type Result struct {
	Duration   time.Duration
	StatusCode int
	Err        error
	BytesRead  int64
}

func Compute(results []runner.Result, elapsed time.Duration) Summary {
	s := Summary{
		Total:   len(results),
		Elapsed: elapsed,
		Codes:   make(map[int]int),
		Errors:  make(map[ErrorKind]int),
	}

	durations := make([]time.Duration, 0, len(results))
	var total time.Duration

	for _, r := range results {
		if r.Err != nil {
			s.Failed++
			s.Errors[Classify(r.Err)]++
			continue
		}
		s.Success++
		s.Codes[r.StatusCode]++
		s.BytesRead += r.BytesRead
		durations = append(durations, r.Duration)
		total += r.Duration
	}

	if s.Success > 0 {
		slices.Sort(durations)
		s.Min = durations[0]
		s.Max = durations[len(durations)-1]
		s.Mean = total / time.Duration(s.Success)
		s.P50 = Percentile(durations, 0.50)
		s.P90 = Percentile(durations, 0.90)
		s.P95 = Percentile(durations, 0.95)
		s.P99 = Percentile(durations, 0.99)
	}

	if elapsed > 0 {
		s.RPS = float64(s.Success) / elapsed.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / elapsed.Seconds()
	}

	return s
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
