// Пороги приёмки: заданный порог превращает прогон из измерения в проверку.
// Без них инструмент в пайплайне бесполезен — он всегда «успешен».
//
// Отдельный пакет, а не часть stats, по двум причинам. Меняется он от другого:
// stats — когда добавляется метрика, slo — когда добавляется вид порога.
// И зависит от него только cmd: держать его в stats значило бы заставить
// report тянуть за собой критерии приёмки, которых он в глаза не видел.
package slo

import (
	"fmt"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

// SLO — пороги, при нарушении которых прогон считается провалившимся.
// Нулевое поле означает «порог не задан»: проверка не выполняется вовсе.
type Thresholds struct {
	// P99 сверяется с Corrected, а не с Latency. В closed-loop это одно и то
	// же число (Lag всегда ноль), а в open-loop Corrected — то, что почувствовал
	// бы клиент, шлющий по часам. Гейт по заниженному значению обесценил бы
	// всё, ради чего считается поправка.
	P99 time.Duration

	// ErrorRate — доля запросов, не давших 2xx: и отказы сервиса, и отсутствие
	// ответа. 429 в пайплайне — такой же провал, как таймаут.
	ErrorRate float64
}

func (s Thresholds) Empty() bool { return s.P99 <= 0 && s.ErrorRate < 0 }

// Violation — не выдержанный порог, готовый к печати.
type Violation struct {
	Metric string
	Want   string
	Got    string

	// Unmeasured — порог не нарушен, а не проверен: данных не хватило.
	// Молча пропустить такую проверку значит вернуть зелёный CI на прогоне,
	// который поставленного вопроса не решил.
	Unmeasured bool
}

// Check возвращает только неудачи: пустой список означает, что всё заданное
// выдержано.
func (s Thresholds) Check(sum stats.Summary) []Violation {
	var out []Violation

	if s.P99 > 0 {
		switch {
		case !sum.Corrected.Reliable(0.99):
			out = append(out, Violation{
				Metric:     "p99",
				Want:       fmt.Sprintf("≤ %v", s.P99),
				Got:        fmt.Sprintf("нет данных (%d замеров, нужно %d)", sum.Corrected.Samples, stats.MinSamples(0.99)),
				Unmeasured: true,
			})
		case sum.Corrected.P99 > s.P99:
			out = append(out, Violation{
				Metric: "p99",
				Want:   fmt.Sprintf("≤ %v", s.P99),
				Got:    sum.Corrected.P99.Round(time.Microsecond).String(),
			})
		}
	}

	if s.ErrorRate >= 0 {
		got := 1 - sum.SuccessRate()
		switch {
		case sum.Total == 0:
			out = append(out, Violation{
				Metric:     "error-rate",
				Want:       fmt.Sprintf("≤ %.2f%%", s.ErrorRate*100),
				Got:        "нет данных (ни одного запроса)",
				Unmeasured: true,
			})
		case got > s.ErrorRate:
			out = append(out, Violation{
				Metric: "error-rate",
				Want:   fmt.Sprintf("≤ %.2f%%", s.ErrorRate*100),
				Got:    fmt.Sprintf("%.2f%%", got*100),
			})
		}
	}

	return out
}

// Unmeasured сообщает, что хотя бы один порог не удалось проверить.
func Unmeasured(vs []Violation) bool {
	for _, v := range vs {
		if v.Unmeasured {
			return true
		}
	}
	return false
}
