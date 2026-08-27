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

type jsonSummary struct {
	Total     int     `json:"total"`
	Success   int     `json:"success"`
	Failed    int     `json:"failed"`
	ElapsedMs float64 `json:"elapsed_ms"`
	RPS       float64 `json:"rps"`

	BytesRead     int64   `json:"bytes_read"`
	ThroughputMBs float64 `json:"throughput_mb_s"`

	Latency jsonLatencies `json:"latency"`
	// В closed-loop поправки нет — поля не должны появляться вовсе,
	// иначе потребитель решит, что расписание было и оно совпало.
	Corrected *jsonLatencies `json:"corrected,omitempty"`
	MaxLagMs  *float64       `json:"max_lag_ms,omitempty"`

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

func JSON(w io.Writer, s stats.Summary, opt Options) error {
	out := jsonSummary{
		Total:         s.Total,
		Success:       s.Success,
		Failed:        s.Failed,
		ElapsedMs:     ms(s.Elapsed),
		RPS:           s.RPS,
		BytesRead:     s.BytesRead,
		ThroughputMBs: s.Throughput,
		Latency:       latencies(s.Latency),
		Histogram:     make([]jsonBucket, 0, len(s.Histogram)),
		Codes:         s.Codes,
		Errors:        s.Errors,
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
