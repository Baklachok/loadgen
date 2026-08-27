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

func printLatencies(l stats.Latencies) {
	fmt.Printf("  min  %v\n", l.Min.Round(time.Microsecond))
	fmt.Printf("  mean %v\n", l.Mean.Round(time.Microsecond))
	fmt.Printf("  p50  %v\n", l.P50.Round(time.Microsecond))
	fmt.Printf("  p90  %v\n", l.P90.Round(time.Microsecond))
	fmt.Printf("  p95  %v\n", l.P95.Round(time.Microsecond))
	fmt.Printf("  p99  %v\n", l.P99.Round(time.Microsecond))
	fmt.Printf("  max  %v\n", l.Max.Round(time.Microsecond))
}

func printSummary(s stats.Summary, openLoop bool) {
	fmt.Printf("Всего:      %d\n", s.Total)
	fmt.Printf("Успешно:    %d\n", s.Success)
	fmt.Printf("Ошибок:     %d\n", s.Failed)
	fmt.Printf("Время:      %v\n", s.Elapsed.Round(time.Millisecond))
	fmt.Printf("RPS:        %.1f\n", s.RPS)
	fmt.Printf("Throughput: %.2f МБ/с\n", s.Throughput)

	if s.Success > 0 {
		fmt.Printf("\nLatency (время запроса):\n")
		printLatencies(s.Latency)

		if openLoop {
			// Разница между блоками — и есть coordinated omission: без поправки
			// запросы, простоявшие в очереди генератора, выглядели бы быстрыми
			fmt.Printf("\nLatency с поправкой на расписание:\n")
			printLatencies(s.Corrected)
			fmt.Printf("\nМакс. отставание старта: %v\n", s.MaxLag.Round(time.Microsecond))
		}
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
		kinds := make([]stats.ErrorKind, 0, len(s.Errors))
		for kind := range s.Errors {
			kinds = append(kinds, kind)
		}
		slices.Sort(kinds)
		for _, kind := range kinds {
			fmt.Printf("  %s: %d\n", kind, s.Errors[kind])
		}
	}
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
		fmt.Fprintf(os.Stderr, "  loadgen -z 30s -rate 500 -c 200 http://localhost:8080   # open-loop\n")
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

	printSummary(stats.Compute(results, time.Since(start)), cfg.Rate > 0)
}

func printHeader(cfg runner.Config) {
	mode := fmt.Sprintf("closed-loop, %d потоков", cfg.Concurrency)
	if cfg.Rate > 0 {
		mode = fmt.Sprintf("open-loop %.0f RPS, до %d запросов в полёте", cfg.Rate, cfg.Concurrency)
	}

	if cfg.Duration > 0 {
		fmt.Printf("Прогон %v на %s (%s)\n\n", cfg.Duration, cfg.URL, mode)
	} else {
		fmt.Printf("Запуск %d запросов к %s (%s)\n\n", cfg.Requests, cfg.URL, mode)
	}
}
