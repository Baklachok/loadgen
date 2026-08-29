// Что сравниваем и в какую сторону хорошо. Направление живёт здесь, а не
// в report: документ не знает, что рост p99 это плохо, а рост RPS — нет.
package compare

import (
	"fmt"

	"github.com/Baklachok/loadgen/internal/report"
)

// metric — что сравниваем и в какую сторону хорошо. Направление здесь,
// а не в report: документ не знает, что рост p99 это плохо, а рост RPS — нет.
type metric struct {
	label  string
	better direction
	gate   string // с каким порогом сверяется; пусто — ни с каким
	get    func(report.RunSummary) *float64
	format func(float64) string
}

type direction int

const (
	lower direction = iota // ниже — лучше
	higher
)

func ms(v float64) string    { return fmt.Sprintf("%.3f мс", v) }
func rate(v float64) string  { return fmt.Sprintf("%.1f", v) }
func count(v float64) string { return fmt.Sprintf("%.0f", v) }
func share(v float64) string { return fmt.Sprintf("%.1f%%", v*100) }

func num(v float64) *float64 { return &v }

// Порядок вывода: сначала то, ради чего прогон затевался.
var metrics = []metric{
	{"RPS", higher, "rps", func(s report.RunSummary) *float64 { return num(s.RPS) }, rate},
	{"p50", lower, "", func(s report.RunSummary) *float64 { return s.Latency.P50 }, ms},
	{"p90", lower, "", func(s report.RunSummary) *float64 { return s.Latency.P90 }, ms},
	{"p95", lower, "", func(s report.RunSummary) *float64 { return s.Latency.P95 }, ms},
	{"p99", lower, "p99", func(s report.RunSummary) *float64 { return s.Latency.P99 }, ms},
	{"p99 с поправкой", lower, "", correctedP99, ms},
	{"успешных", higher, "", func(s report.RunSummary) *float64 { return num(s.SuccessRate) }, share},
	{"не-2xx", lower, "", func(s report.RunSummary) *float64 { return num(float64(s.NonOK)) }, count},
	{"без ответа", lower, "", func(s report.RunSummary) *float64 { return num(float64(s.Failed)) }, count},
	{"оборвано", lower, "", func(s report.RunSummary) *float64 { return num(float64(s.Truncated)) }, count},
	{"throughput", higher, "", func(s report.RunSummary) *float64 { return num(s.Throughput) }, rate},
}

func correctedP99(s report.RunSummary) *float64 {
	if s.Corrected == nil {
		return nil
	}
	return s.Corrected.P99
}
