// Отметка о прерванном прогоне.
package report

import (
	"fmt"
	"io"

	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

// writePartial ставится первой секцией отчёта. Частичный результат,
// неотличимый от полного, — это будущий скриншот в чате с подписью
// «сервис держит 4000 rps», сделанный по прерванному прогону.
func writePartial(w io.Writer, s stats.Summary, opt Options, p palette) {
	if !s.Partial {
		return
	}

	fmt.Fprintf(w, "%s %s\n\n", p.red("ПРЕРВАНО:"), p.bold(partialScope(s, opt.Run.Config)))
}

// partialScope говорит, сколько от задуманного успели: в режиме -n это доля
// запросов, в -z — доля времени.
func partialScope(s stats.Summary, cfg runner.Config) string {
	if cfg.Duration > 0 {
		return fmt.Sprintf("%v из %v", s.Elapsed.Round(time.Second), cfg.Duration)
	}
	if cfg.Requests > 0 {
		return fmt.Sprintf("%d запросов из %d", s.Total+s.Warmup, cfg.Requests)
	}
	return fmt.Sprintf("%d запросов", s.Total+s.Warmup)
}
