// Package report отвечает только за представление: текст для человека,
// JSON для CI. Ничего не считает — все числа приходят готовыми из stats.
package report

import (
	"fmt"
	"io"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

type Options struct {
	Color    bool // раскрашивать вывод ANSI-кодами
	Width    int  // ширина терминала: под неё масштабируется гистограмма
	OpenLoop bool // печатать блок с поправкой на расписание

	// Run описывает сам прогон. Отчёт живёт дольше, чем память о том, как
	// его получили, поэтому эти поля печатаются всегда.
	Run RunInfo
}

// RunInfo — всё, что нужно, чтобы повторить прогон через полгода.
type RunInfo struct {
	Version   string
	Config    runner.Config
	Proto     string // по чему реально договорились, а не что просили
	StartedAt time.Time
}

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
