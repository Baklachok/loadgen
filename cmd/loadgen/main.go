package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Baklachok/loadgen/internal/report"
	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

var version = "dev" // подставляется через -ldflags при сборке из Makefile

// buildVersion возвращает версию сборки. Приоритет у вшитого линкером значения,
// но если собирали не через Makefile — например, через `go install ...@latest` —
// подойдёт то, что Go записал в бинарник сам.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}
	return versionFrom(info)
}

// versionFrom вынесена отдельно от чтения глобального состояния, чтобы её
// можно было проверить тестом.
func versionFrom(info *debug.BuildInfo) string {
	var rev, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}

	// VCS-данные идут первыми: при сборке из рабочей копии Go кладёт в
	// Main.Version псевдоверсию вида v0.0.0-20260827120054-5f62ef91018b+dirty,
	// а короткий хеш рядом читается несравнимо лучше.
	if rev != "" {
		if len(rev) > 7 {
			rev = rev[:7]
		}
		return rev + dirty
	}

	// При `go install pkg@v1.2.3` VCS-данных нет, зато Main.Version — ровно v1.2.3.
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	return "dev"
}

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

// flags — объявленные флаги одним типом: иначе run таскает полтора десятка
// указателей через все свои шаги.
type flags struct {
	n, c        *int
	z, timeout  *time.Duration
	method      *string
	body        *string
	output      *string
	rate        *float64
	trace       *bool
	insecure    *bool
	noKeepAlive *bool
	http2       *bool
	showVersion *bool

	headers headerFlag
	warmup  warmupFlag
}

func newFlags(fs *flag.FlagSet, stderr io.Writer) *flags {
	f := &flags{
		n:           fs.Int("n", 200, "количество запросов"),
		c:           fs.Int("c", 50, "конкурентность; в open-loop — потолок запросов в полёте"),
		z:           fs.Duration("z", 0, "длительность прогона (взаимоисключающе с -n)"),
		method:      fs.String("m", "GET", "HTTP-метод"),
		body:        fs.String("d", "", "тело запроса"),
		timeout:     fs.Duration("t", 10*time.Second, "таймаут запроса"),
		rate:        fs.Float64("rate", 0, "постоянный RPS, режим open-loop (0 — closed-loop)"),
		output:      fs.String("o", "text", "формат вывода: text или json"),
		trace:       fs.Bool("trace", false, "разбить latency по фазам: DNS, TCP, TLS, TTFB"),
		insecure:    fs.Bool("insecure", false, "не проверять TLS-сертификат"),
		noKeepAlive: fs.Bool("disable-keepalive", false, "новое соединение на каждый запрос"),
		http2:       fs.Bool("http2", false, "разрешить HTTP/2"),
		showVersion: fs.Bool("version", false, "показать версию"),
	}
	fs.Var(&f.headers, "H", "заголовок в формате 'Key: Value' (можно несколько раз)")
	fs.Var(&f.warmup, "warmup", "прогрев: длительность (5s) или число запросов (100), в статистику не идёт")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "loadgen — нагрузочный тестер HTTP\n\n")
		fmt.Fprintf(stderr, "Использование:\n  loadgen [флаги] URL\n\nФлаги:\n")
		fs.PrintDefaults()

		fmt.Fprintf(stderr, "\nПримеры:\n")
		for _, ex := range []string{
			"loadgen -n 1000 -c 50 http://localhost:8080",
			"loadgen -z 30s -rate 500 -c 200 http://localhost:8080   # open-loop",
			"loadgen -n 1000 -o json http://localhost:8080 | jq .latency.p99_ms",
			`loadgen -m POST -d '{"a":1}' -H 'Content-Type: application/json' http://localhost:8080/api`,
		} {
			fmt.Fprintf(stderr, "  %s\n", ex)
		}
	}
	return f
}

// config собирает конфиг прогона и ловит противоречие, которого Validate
// увидеть не может: он смотрит на значения, а не на то, задавал ли их человек.
func (f *flags) config(fs *flag.FlagSet) (runner.Config, error) {
	// Заданы ли флаги явно, знает только FlagSet: сравнение с умолчанием
	// не годится — «-n 200 -z 5s» тоже противоречие, хотя 200 это дефолт.
	var setN, setZ bool
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "n":
			setN = true
		case "z":
			setZ = true
		}
	})
	if setN && setZ {
		return runner.Config{}, errors.New("-n и -z взаимоисключающи")
	}

	// -n обнуляется при -z, иначе Validate решит, что заданы оба.
	requests := *f.n
	if *f.z > 0 {
		requests = 0
	}

	cfg := runner.Config{
		URL:              fs.Arg(0),
		Method:           *f.method,
		Body:             []byte(*f.body),
		Headers:          f.headers.h,
		Requests:         requests,
		Duration:         *f.z,
		Concurrency:      *f.c,
		Timeout:          *f.timeout,
		Rate:             *f.rate,
		Trace:            *f.trace,
		WarmupRequests:   f.warmup.requests,
		WarmupDuration:   f.warmup.duration,
		DisableKeepAlive: *f.noKeepAlive,
		Insecure:         *f.insecure,
		HTTP2:            *f.http2,
	}
	return cfg, cfg.Validate()
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

	return outcome(ctx, summary)
}

// interruptible возвращает контекст, отменяемый по Ctrl+C.
//
// Свой канал вместо signal.NotifyContext: его stop() отменяет контекст, и
// наблюдатель просыпался при обычном завершении — сообщение об остановке
// печаталось на каждом успешном прогоне.
func interruptible(stderr io.Writer) (context.Context, func()) {
	// Буфер на два: пока печатаем и отменяем, второй Ctrl+C не должен потеряться.
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-signals
		fmt.Fprintln(stderr, "\nостановка, собираю результаты… (ещё раз Ctrl+C — выйти немедленно)")
		cancel()

		// Без этого зависший запрос делает Ctrl+C бесполезным, и пользователь
		// уходит в kill -9, теряя все собранные результаты.
		<-signals
		fmt.Fprintln(stderr, "прервано")
		os.Exit(exitInterrupt)
	}()

	return ctx, func() {
		signal.Stop(signals)
		cancel()
	}
}

// outcome переводит итог прогона в код возврата.
//
// «Сервер вернул 500» кодом не является: инструмент отработал, он для того
// и нужен, чтобы показать пятисотки. Код 2 означает другое — измерить не
// удалось вовсе, ни одного ответа не пришло.
func outcome(ctx context.Context, s stats.Summary) int {
	if ctx.Err() != nil {
		return exitInterrupt
	}
	if s.Responses() == 0 {
		return exitNoRun
	}
	return exitOK
}
