package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

func main() {
	cfg := runner.Config{
		URL:         "http://localhost:8080",
		Requests:    5000,
		Concurrency: 50,
		Timeout:     10 * time.Second,
	}

	fmt.Printf("Запуск %d запросов к %s в %d потоков...\n\n",
		cfg.Requests, cfg.URL, cfg.Concurrency)

	start := time.Now()
	results, err := runner.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
	s := stats.Compute(results, time.Since(start))

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
		for code, cnt := range s.Codes {
			fmt.Printf("  %d: %d\n", code, cnt)
		}
	}

	if len(s.Errors) > 0 {
		fmt.Printf("\nОшибки:\n")
		for kind, cnt := range s.Errors {
			fmt.Printf("  %s: %d\n", kind, cnt)
		}
	}
}
