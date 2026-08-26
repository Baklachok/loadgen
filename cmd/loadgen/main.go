package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Result struct {
	Duration   time.Duration
	StatusCode int
	Err        error
	BytesRead  int64
}

type Config struct {
	URL         string
	Requests    int
	Concurrency int
	Timeout     time.Duration
}

func doRequest(client *http.Client, url string) Result {
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err}
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	return Result{Duration: time.Since(start), StatusCode: resp.StatusCode, BytesRead: n}
}

func Run(ctx context.Context, cfg Config) []Result {
	jobs := make(chan struct{}, cfg.Concurrency)
	results := make(chan Result, cfg.Concurrency)

	client := &http.Client{Timeout: cfg.Timeout}

	// 1. Воркеры
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				results <- doRequest(client, cfg.URL)
			}
		}()
	}

	// 2. Продюсер
	go func() {
		defer close(jobs)
		for i := 0; i < cfg.Requests; i++ {
			select {
			case jobs <- struct{}{}:
			case <-ctx.Done():
				return
			}
		}
	}()

	// 3. Закрывашка
	go func() {
		wg.Wait()
		close(results)
	}()

	// 4. Коллектор
	all := make([]Result, 0, cfg.Requests)
	for r := range results {
		all = append(all, r)
	}
	return all
}

func main() {
	cfg := Config{
		URL:         "http://localhost:8000",
		Requests:    200,
		Concurrency: 20,
		Timeout:     5 * time.Second,
	}

	fmt.Printf("Запуск %d запросов к %s в %d потоков...\n\n", cfg.Requests, cfg.URL, cfg.Concurrency)

	wallStart := time.Now()
	results := Run(context.Background(), cfg)
	elapsed := time.Since(wallStart)

	var (
		totalDuration time.Duration
		successCount  int
		minD          = time.Duration(1<<63 - 1)
		maxD          time.Duration
		totalBytes    int64
	)

	codes := make(map[int]int)
	errs := make(map[string]int)

	for _, r := range results {
		if r.Err != nil {
			errs[r.Err.Error()]++
			continue
		}
		successCount++
		totalDuration += r.Duration
		totalBytes += r.BytesRead
		codes[r.StatusCode]++
		if r.Duration < minD {
			minD = r.Duration
		}
		if r.Duration > maxD {
			maxD = r.Duration
		}
	}

	fmt.Printf("Всего:     %d\n", len(results))
	fmt.Printf("Успешно:   %d\n", successCount)
	fmt.Printf("Ошибок:    %d\n", len(results)-successCount)
	fmt.Printf("Время:     %v\n", elapsed.Round(time.Millisecond))

	if successCount > 0 {
		fmt.Printf("RPS:       %.1f\n", float64(successCount)/elapsed.Seconds())
		fmt.Printf("Latency:   min %v | avg %v | max %v\n",
			minD.Round(time.Microsecond),
			(totalDuration / time.Duration(successCount)).Round(time.Microsecond),
			maxD.Round(time.Microsecond))
		fmt.Printf("Прочитано: %d байт\n", totalBytes)
	}

	if len(codes) > 0 {
		fmt.Printf("\nКоды ответов:\n")
		for code, cnt := range codes {
			fmt.Printf("  %d: %d\n", code, cnt)
		}
	}
	if len(errs) > 0 {
		fmt.Printf("\nОшибки:\n")
		for e, cnt := range errs {
			fmt.Printf("  %d × %s\n", cnt, e)
		}
	}
}