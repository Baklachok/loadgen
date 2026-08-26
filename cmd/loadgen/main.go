package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
	"crypto/tls"
	"net"
	"math"
)

type Result struct {
	Duration   time.Duration
	StatusCode int
	Err        error
	BytesRead  int64
}

type Config struct {
	URL              string
	Requests         int
	Concurrency      int
	Timeout          time.Duration
	DisableKeepAlive bool
	Insecure         bool
	HTTP2            bool
}

func doRequest(ctx context.Context, client *http.Client, url string) Result {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err}
	}
	resp, err := client.Do(req)
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

	tr := newTransport(cfg)
	defer tr.CloseIdleConnections()
	client := newClient(cfg, tr)

	// 1. Воркеры
	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				results <- doRequest(ctx, client, cfg.URL)
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

func newTransport(cfg Config) *http.Transport {
	return &http.Transport{
		MaxIdleConns:        cfg.Concurrency,
		MaxIdleConnsPerHost: cfg.Concurrency,
		MaxConnsPerHost:     cfg.Concurrency,
		IdleConnTimeout:     90 * time.Second,

		DisableKeepAlives:  cfg.DisableKeepAlive,
		DisableCompression: true,

		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,

		ForceAttemptHTTP2: cfg.HTTP2,
		//nolint:gosec // осознанный флаг --insecure
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure},
	}
}

func newClient(cfg Config, tr *http.Transport) *http.Client {
	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func main() {
		cfg := Config{
		URL:              "http://localhost:8080",
		Requests:         5000,
		Concurrency:      50,
		Timeout:          10 * time.Second,
		DisableKeepAlive: false,
		HTTP2:            false,
	}

	fmt.Printf("Запуск %d запросов к %s в %d потоков...\n\n", cfg.Requests, cfg.URL, cfg.Concurrency)

	wallStart := time.Now()
	results := Run(context.Background(), cfg)
	elapsed := time.Since(wallStart)

	var (
		totalDuration time.Duration
		successCount  int
		minD          = time.Duration(math.MaxInt64)
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