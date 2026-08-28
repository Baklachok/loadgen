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

// Контракт кодов выхода. Без него loadgen нельзя поставить в пайплайн:
// он всегда «успешен», даже когда сто процентов запросов упали.
//
// Отступление от stdlib осознанное. flag.ExitOnError выходит с кодом 2 на
// любой ошибке разбора, но здесь 2 занят под «измерить не удалось» — это
// разные вещи, и CI должен краснеть по-разному. Поэтому флаги разбираются
// с ContinueOnError, а подсказка печатается вручную.
const (
	exitOK        = 0   // прогон состоялся; SLO, если заданы, выдержаны
	exitUsage     = 1   // флаги, URL, несовместимые опции
	exitNoRun     = 2   // измерить не удалось: ни одного ответа
	exitSLO       = 3   // SLO нарушен; появится вместе с --slo-*
	exitInterrupt = 130 // прервано пользователем, результат частичный
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run принимает аргументы и потоки, а не читает глобальные: только так
// контракт кодов выхода можно проверить тестом, а не руками.
func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := newFlags(fs, stderr)

	// ContinueOnError уже напечатал причину и подсказку; нам остаётся код.
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if *f.showVersion {
		fmt.Fprintln(stdout, "loadgen", buildVersion())
		return exitOK
	}
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

	opt := report.Options{
		Color:    report.ColorEnabled(stdout),
		Width:    report.TerminalWidth(stdout, 80),
		OpenLoop: cfg.Rate > 0,
		Run:      report.RunInfo{Version: buildVersion(), Config: cfg},
	}

	ctx, stop := interruptible(stderr)
	defer stop()

	// Печатать ли шапку, решает сам рендерер: в машинных форматах её нет.
	if err := renderer.Header(stdout, opt); err != nil {
		fmt.Fprintln(stderr, "ошибка вывода:", err)
		return exitNoRun
	}

	// Время меряет сам runner: он один знает, когда кончился прогрев.
	rep, err := runner.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	// Протокол и время старта известны только после прогона.
	opt.Run.Proto, opt.Run.StartedAt = rep.Proto, rep.StartedAt

	summary := stats.Compute(rep)
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
