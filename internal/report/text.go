// Сборка текстового документа: предстартовая шапка и порядок секций.
// Парная к json.go — там тот же отчёт собирается для машины.
package report

import (
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

// Header печатается до прогона, чтобы было видно, что вообще запустили.
func writeHeader(w io.Writer, opt Options) {
	p := palette(opt.Color)
	cfg := opt.Run.Config

	mode := runMode(cfg)
	target := p.bold(cfg.URL)
	if cfg.Duration > 0 {
		fmt.Fprintf(w, "Прогон %v на %s %s\n\n", cfg.Duration, target, p.dim("("+mode+")"))
		return
	}
	fmt.Fprintf(w, "Запуск %d запросов к %s %s\n\n", cfg.Requests, target, p.dim("("+mode+")"))
}

// section — кусок отчёта. Печататься или промолчать решает он сам: условие
// «есть ли что показывать» — часть знания секции. Раньше половина условий
// жила в оркестраторе, и добавление секции требовало помнить, под каким из
// вложенных if её место.
type section func(w io.Writer, s stats.Summary, opt Options, p palette)

// Порядок вывода. Происхождение прогона идёт последним: первый экран должен
// отвечать на вопрос, а справка нужна позже.
var sections = []section{
	writePartial,
	writeTotals,
	writeClientWarning,
	writeRateWarning,
	writeLatency,
	writeTrace,
	writeHistogram,
	writeCodes,
	writeErrors,
	writeProvenance,
}

func writeReport(w io.Writer, s stats.Summary, opt Options) {
	p := palette(opt.Color)
	for _, write := range sections {
		write(w, s, opt, p)
	}
}

// runMode описывает режим нагрузки одной строкой. Нужен и предстартовой
// шапке, и блоку происхождения в конце.
func runMode(cfg runner.Config) string {
	if cfg.Rate > 0 {
		return fmt.Sprintf("open-loop %.0f RPS, до %d запросов в полёте", cfg.Rate, cfg.Concurrency)
	}
	return fmt.Sprintf("closed-loop, %d потоков", cfg.Concurrency)
}
