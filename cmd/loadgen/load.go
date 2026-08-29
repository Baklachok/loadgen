// Режим нагрузки: прогон и его итог. Парный к compare.go — раньше он
// оставался внутри run, и диспетчер режимов был длиннее самих режимов.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Baklachok/loadgen/internal/report"
	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/slo"
	"github.com/Baklachok/loadgen/internal/stats"
)

// runLoad прогоняет нагрузку и печатает отчёт.
func runLoad(f *flags, fs *flag.FlagSet, stdin, stdout, stderr *os.File) int {
	if fs.NArg() != 1 {
		fs.Usage()
		return exitUsage
	}

	renderer, err := report.NewRenderer(*f.output)
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	cfg, err := f.config(fs)
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	// Спрашиваем до шапки: «Запуск 50000 запросов к …» не должно опережать
	// вопрос о том, можно ли вообще туда стрелять.
	if !permitted(cfg, *f.yes, stdin, stderr) {
		return exitUsage
	}

	opt := report.Options{
		Color:    report.ColorEnabled(stdout),
		Width:    report.TerminalWidth(stdout, 80),
		OpenLoop: cfg.Rate > 0,
		Run:      report.RunInfo{Version: buildVersion(), Config: cfg},
	}

	ctx, stop := interruptible(stderr, os.Exit)
	defer stop()

	// Накопитель заводится до прогона: результаты в нём и оседают, а сам
	// прогон их не хранит — на длинном -z это сотни мегабайт.
	acc := stats.NewAccumulator(cfg.Rate)

	// /metrics поднимается до шапки и гаснет после последнего запроса: занятый
	// порт — ошибка запуска, и «Прогон 3s на …» не должен её опережать.
	if *f.metrics != "" {
		m, err := listenMetrics(*f.metrics, acc)
		if err != nil {
			fmt.Fprintln(stderr, "ошибка:", err)
			return exitUsage
		}
		defer m.Close()
		fmt.Fprintf(stderr, "метрики: http://%s/metrics\n", m.Addr())
	}

	// Печатать ли шапку, решает сам рендерер: в машинных форматах её нет.
	if err := renderer.Header(stdout, opt); err != nil {
		fmt.Fprintln(stderr, "ошибка вывода:", err)
		return exitNoRun
	}

	// Время меряет сам runner: он один знает, когда кончился прогрев.
	rep, err := runner.Run(ctx, cfg, acc.Add)
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	// Протокол и время старта известны только после прогона.
	opt.Run.Proto, opt.Run.StartedAt = rep.Proto, rep.StartedAt

	summary := acc.Summary(rep)
	if err := renderer.Render(stdout, summary, opt); err != nil {
		fmt.Fprintln(stderr, "ошибка вывода:", err)
		return exitNoRun
	}

	return outcome(ctx, summary, f.slo(fs), stderr)
}

// outcome переводит итог прогона в код возврата.
//
// «Сервер вернул 500» кодом не является: инструмент отработал, он для того
// и нужен, чтобы показать пятисотки. Код 2 означает другое — измерить не
// удалось: либо не пришло ни одного ответа, либо не хватило данных на
// проверку, которую попросили.
func outcome(ctx context.Context, s stats.Summary, thresholds slo.Thresholds, stderr io.Writer) int {
	if ctx.Err() != nil {
		return exitInterrupt
	}
	if s.Responses() == 0 {
		return exitNoRun
	}

	violations := thresholds.Check(s)
	for _, v := range violations {
		fmt.Fprintf(stderr, "SLO %s: требовалось %s, получено %s\n", v.Metric, v.Want, v.Got)
	}

	switch {
	case slo.Unmeasured(violations):
		return exitNoRun
	case len(violations) > 0:
		return exitSLO
	}
	return exitOK
}
