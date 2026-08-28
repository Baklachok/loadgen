package main

import (
	"context"
	"flag"
	"fmt"
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

func main() {
	var (
		n           = flag.Int("n", 200, "количество запросов")
		c           = flag.Int("c", 50, "конкурентность; в open-loop — потолок запросов в полёте")
		z           = flag.Duration("z", 0, "длительность прогона (взаимоисключающе с -n)")
		method      = flag.String("m", "GET", "HTTP-метод")
		bodyStr     = flag.String("d", "", "тело запроса")
		timeout     = flag.Duration("t", 10*time.Second, "таймаут запроса")
		rateLimit   = flag.Float64("rate", 0, "постоянный RPS, режим open-loop (0 — closed-loop)")
		output      = flag.String("o", "text", "формат вывода: text или json")
		trace       = flag.Bool("trace", false, "разбить latency по фазам: DNS, TCP, TLS, TTFB")
		insecure    = flag.Bool("insecure", false, "не проверять TLS-сертификат")
		noKeepAlive = flag.Bool("disable-keepalive", false, "новое соединение на каждый запрос")
		http2       = flag.Bool("http2", false, "разрешить HTTP/2")
		showVersion = flag.Bool("version", false, "показать версию")
	)

	var headers headerFlag
	flag.Var(&headers, "H", "заголовок в формате 'Key: Value' (можно несколько раз)")

	var warmup warmupFlag
	flag.Var(&warmup, "warmup", "прогрев: длительность (5s) или число запросов (100), в статистику не идёт")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "loadgen — нагрузочный тестер HTTP\n\n")
		fmt.Fprintf(os.Stderr, "Использование:\n  loadgen [флаги] URL\n\nФлаги:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nПримеры:\n")
		fmt.Fprintf(os.Stderr, "  loadgen -n 1000 -c 50 http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  loadgen -z 30s -rate 500 -c 200 http://localhost:8080   # open-loop\n")
		fmt.Fprintf(os.Stderr, "  loadgen -n 1000 -o json http://localhost:8080 | jq .latency.p99_ms\n")
		fmt.Fprintf(os.Stderr, "  loadgen -m POST -d '{\"a\":1}' -H 'Content-Type: application/json' http://localhost:8080/api\n")
	}

	flag.Parse()

	if *showVersion {
		fmt.Println("loadgen", buildVersion())
		return
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	renderer, err := report.NewRenderer(*output)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(2)
	}

	// -n сбрасываем в 0, если задан -z, иначе Validate решит, что заданы оба
	requests := *n
	if *z > 0 {
		requests = 0
	}

	cfg := runner.Config{
		URL:              flag.Arg(0),
		Method:           *method,
		Body:             []byte(*bodyStr),
		Headers:          headers.h,
		Requests:         requests,
		Duration:         *z,
		Concurrency:      *c,
		Timeout:          *timeout,
		Rate:             *rateLimit,
		Trace:            *trace,
		WarmupRequests:   warmup.requests,
		WarmupDuration:   warmup.duration,
		DisableKeepAlive: *noKeepAlive,
		Insecure:         *insecure,
		HTTP2:            *http2,
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(2)
	}

	opt := report.Options{
		Color:    report.ColorEnabled(os.Stdout),
		Width:    report.TerminalWidth(os.Stdout, 80),
		OpenLoop: cfg.Rate > 0,
		Run:      report.RunInfo{Version: buildVersion(), Config: cfg},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		fmt.Fprintln(os.Stderr, "\nостановка, собираю результаты...")
	}()

	// Печатать ли шапку, решает сам рендерер: в машинных форматах её нет.
	if err := renderer.Header(os.Stdout, opt); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка вывода:", err)
		os.Exit(1)
	}

	// Время меряет сам runner: он один знает, когда кончился прогрев.
	rep, err := runner.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}

	// Протокол и время старта известны только после прогона.
	opt.Run.Proto, opt.Run.StartedAt = rep.Proto, rep.StartedAt

	if err := renderer.Render(os.Stdout, stats.Compute(rep), opt); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка вывода:", err)
		os.Exit(1)
	}
}
