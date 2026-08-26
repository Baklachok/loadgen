package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

var version = "dev" // подставляется через -ldflags

func printSummary(s stats.Summary) {
	fmt.Printf("Всего:      %d\n", s.Total)
	fmt.Printf("Успешно:    %d\n", s.Success)
	fmt.Printf("Ошибок:     %d\n", s.Failed)
	fmt.Printf("Время:      %v\n", s.Elapsed.Round(time.Millisecond))
	fmt.Printf("RPS:        %.1f\n", s.RPS)
	fmt.Printf("Throughput: %.2f МБ/с\n", s.Throughput)

	if s.Success > 0 {
		fmt.Printf("\nLatency:\n")
		fmt.Printf("  min  %v\n", s.Min.Round(time.Microsecond))
		fmt.Printf("  mean %v\n", s.Mean.Round(time.Microsecond))
		fmt.Printf("  p50  %v\n", s.P50.Round(time.Microsecond))
		fmt.Printf("  p90  %v\n", s.P90.Round(time.Microsecond))
		fmt.Printf("  p95  %v\n", s.P95.Round(time.Microsecond))
		fmt.Printf("  p99  %v\n", s.P99.Round(time.Microsecond))
		fmt.Printf("  max  %v\n", s.Max.Round(time.Microsecond))
	}

	if len(s.Codes) > 0 {
		fmt.Printf("\nКоды ответов:\n")
		codes := make([]int, 0, len(s.Codes))
		for code := range s.Codes {
			codes = append(codes, code)
		}
		slices.Sort(codes)
		for _, code := range codes {
			fmt.Printf("  %d: %d\n", code, s.Codes[code])
		}
	}

	if len(s.Errors) > 0 {
		fmt.Printf("\nОшибки:\n")
		for kind, cnt := range s.Errors {
			fmt.Printf("  %s: %d\n", kind, cnt)
		}
	}
}

func main() {
	var (
		n           = flag.Int("n", 200, "количество запросов")
		c           = flag.Int("c", 50, "конкурентность")
		z           = flag.Duration("z", 0, "длительность прогона (взаимоисключающе с -n)")
		method      = flag.String("m", "GET", "HTTP-метод")
		bodyStr     = flag.String("d", "", "тело запроса")
		timeout     = flag.Duration("t", 10*time.Second, "таймаут запроса")
		rateLimit   = flag.Float64("rate", 0, "лимит RPS (0 — без лимита)")
		insecure    = flag.Bool("insecure", false, "не проверять TLS-сертификат")
		noKeepAlive = flag.Bool("disable-keepalive", false, "новое соединение на каждый запрос")
		http2       = flag.Bool("http2", false, "разрешить HTTP/2")
		showVersion = flag.Bool("version", false, "показать версию")
	)

	var headers headerFlag
	flag.Var(&headers, "H", "заголовок в формате 'Key: Value' (можно несколько раз)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "loadgen — нагрузочный тестер HTTP\n\n")
		fmt.Fprintf(os.Stderr, "Использование:\n  loadgen [флаги] URL\n\nФлаги:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nПримеры:\n")
		fmt.Fprintf(os.Stderr, "  loadgen -n 1000 -c 50 http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  loadgen -z 30s -c 100 -rate 500 http://localhost:8080\n")
		fmt.Fprintf(os.Stderr, "  loadgen -m POST -d '{\"a\":1}' -H 'Content-Type: application/json' http://localhost:8080/api\n")
	}

	flag.Parse()

	if *showVersion {
		fmt.Println("loadgen", version)
		return
	}

	if flag.NArg() != 1 {
		flag.Usage()
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
		DisableKeepAlive: *noKeepAlive,
		Insecure:         *insecure,
		HTTP2:            *http2,
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		fmt.Fprintln(os.Stderr, "\nостановка, собираю результаты...")
	}()

	printHeader(cfg)

	start := time.Now()
	results, err := runner.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}

	printSummary(stats.Compute(results, time.Since(start)))
}

func printHeader(cfg runner.Config) {
	if cfg.Duration > 0 {
		fmt.Printf("Прогон %v на %s в %d потоков\n\n", cfg.Duration, cfg.URL, cfg.Concurrency)
	} else {
		fmt.Printf("Запуск %d запросов к %s в %d потоков\n\n", cfg.Requests, cfg.URL, cfg.Concurrency)
	}
}
