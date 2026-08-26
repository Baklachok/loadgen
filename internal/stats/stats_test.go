package stats

import (
	"context"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

func TestPercentile(t *testing.T) {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

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
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }

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
	if s.Mean != ms(20) {
		t.Errorf("Mean = %v, want 20ms", s.Mean)
	}
	if s.Codes[200] != 2 || s.Codes[500] != 1 {
		t.Errorf("codes = %v", s.Codes)
	}
	if s.Errors[ErrTimeout] != 1 {
		t.Errorf("errors = %v", s.Errors)
	}
}
