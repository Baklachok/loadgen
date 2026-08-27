package report

import (
	"encoding/json"
	"io"
	"math"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

// Отдельные DTO, а не теги на stats.Summary: time.Duration сериализуется в
// наносекунды-числом, что нечитаемо, и формат отчёта не должен ломаться
// каждый раз, когда внутри stats переименовали поле.
type jsonLatencies struct {
	MinMs  float64 `json:"min_ms"`
	MeanMs float64 `json:"mean_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P90Ms  float64 `json:"p90_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	MaxMs  float64 `json:"max_ms"`
}

type jsonBucket struct {
	UpperMs float64 `json:"upper_ms"`
	Count   int     `json:"count"`
}

type jsonPhase struct {
	Count int     `json:"count"`
	P50Ms float64 `json:"p50_ms"`
	P90Ms float64 `json:"p90_ms"`
	P99Ms float64 `json:"p99_ms"`
	MaxMs float64 `json:"max_ms"`
}

type jsonTrace struct {
	Traced  int       `json:"traced"`
	Reused  int       `json:"reused"`
	DNS     jsonPhase `json:"dns"`
	Connect jsonPhase `json:"connect"`
	TLS     jsonPhase `json:"tls"`
	TTFB    jsonPhase `json:"ttfb"`
}

type jsonSummary struct {
	Total         int     `json:"total"`
	Warmup        int     `json:"warmup_discarded"`
	OK            int     `json:"ok"`
	NonOK         int     `json:"non_2xx"`
	Failed        int     `json:"failed"`
	SuccessRate   float64 `json:"success_rate"`
	ElapsedMs     float64 `json:"elapsed_ms"`
	WindowMs      float64 `json:"window_ms"`
	RPS           float64 `json:"rps"`
	TargetRate    float64 `json:"target_rate"`
	RateShortfall float64 `json:"rate_shortfall"`

	BytesRead     int64   `json:"bytes_read"`
	ThroughputMBs float64 `json:"throughput_mb_s"`

	Latency jsonLatencies `json:"latency"`
	// В closed-loop поправки нет — поля не должны появляться вовсе,
	// иначе потребитель решит, что расписание было и оно совпало.
	Corrected *jsonLatencies `json:"corrected,omitempty"`
	MaxLagMs  *float64       `json:"max_lag_ms,omitempty"`

	// Без -trace фаз не измеряли — поля быть не должно вовсе,
	// иначе потребитель решит, что все они нулевые.
	Trace *jsonTrace `json:"trace,omitempty"`

	Histogram []jsonBucket            `json:"histogram"`
	Codes     map[int]int             `json:"codes"`
	Errors    map[stats.ErrorKind]int `json:"errors"`
}

// ms переводит длительность в миллисекунды с точностью до микросекунды:
// наносекунды в отчёте — шум, а float без округления даёт хвосты вида 5.000000001.
func ms(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Microsecond)) / 1000
}

func latencies(l stats.Latencies) jsonLatencies {
	return jsonLatencies{
		MinMs:  ms(l.Min),
		MeanMs: ms(l.Mean),
		P50Ms:  ms(l.P50),
		P90Ms:  ms(l.P90),
		P95Ms:  ms(l.P95),
		P99Ms:  ms(l.P99),
		MaxMs:  ms(l.Max),
	}
}

func phase(ph stats.PhaseStats) jsonPhase {
	return jsonPhase{
		Count: ph.Count,
		P50Ms: ms(ph.P50),
		P90Ms: ms(ph.P90),
		P99Ms: ms(ph.P99),
		MaxMs: ms(ph.Max),
	}
}

func JSON(w io.Writer, s stats.Summary, opt Options) error {
	out := jsonSummary{
		Total:         s.Total,
		Warmup:        s.Warmup,
		OK:            s.OK,
		NonOK:         s.NonOK,
		Failed:        s.Failed,
		SuccessRate:   s.SuccessRate(),
		ElapsedMs:     ms(s.Elapsed),
		WindowMs:      ms(s.Window),
		RPS:           s.RPS,
		TargetRate:    s.TargetRate,
		RateShortfall: s.RateShortfall(),
		BytesRead:     s.BytesRead,
		ThroughputMBs: s.Throughput,
		Latency:       latencies(s.Latency),
		Histogram:     make([]jsonBucket, 0, len(s.Histogram)),
		Codes:         s.Codes,
		Errors:        s.Errors,
	}

	if s.Trace != nil {
		out.Trace = &jsonTrace{
			Traced:  s.Trace.Traced,
			Reused:  s.Trace.Reused,
			DNS:     phase(s.Trace.DNS),
			Connect: phase(s.Trace.Connect),
			TLS:     phase(s.Trace.TLS),
			TTFB:    phase(s.Trace.TTFB),
		}
	}

	if opt.OpenLoop {
		corrected := latencies(s.Corrected)
		lag := ms(s.MaxLag)
		out.Corrected = &corrected
		out.MaxLagMs = &lag
	}

	for _, b := range s.Histogram {
		out.Histogram = append(out.Histogram, jsonBucket{UpperMs: ms(b.Upper), Count: b.Count})
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
