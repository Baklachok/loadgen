package runner

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
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
	URL              string
	Requests         int
	Concurrency      int
	Timeout          time.Duration
	DisableKeepAlive bool
	Insecure         bool
	HTTP2            bool
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

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err, BytesRead: n}
	}

	return Result{
		Duration:   time.Since(start),
		StatusCode: resp.StatusCode,
		BytesRead:  n,
	}
}

func (c Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("URL не задан")
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("concurrency должен быть >= 1, получено %d", c.Concurrency)
	}
	if c.Requests < 1 {
		return fmt.Errorf("requests должен быть >= 1, получено %d", c.Requests)
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout должен быть > 0, получено %v", c.Timeout)
	}
	if c.Concurrency > c.Requests {
		return fmt.Errorf("concurrency (%d) больше числа запросов (%d)", c.Concurrency, c.Requests)
	}
	return nil
}

func Run(ctx context.Context, cfg Config) ([]Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("некорректная конфигурация: %w", err)
	}

	jobs := make(chan struct{}, cfg.Concurrency)
	results := make(chan Result, cfg.Concurrency)

	tr := newTransport(cfg)
	defer tr.CloseIdleConnections()
	client := newClient(cfg, tr)

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

	go func() {
		wg.Wait()
		close(results)
	}()

	all := make([]Result, 0, cfg.Requests)
	for r := range results {
		all = append(all, r)
	}
	return all, nil
}
