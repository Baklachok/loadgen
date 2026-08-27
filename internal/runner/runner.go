package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Result struct {
	Duration   time.Duration // время самого запроса: отправка → тело дочитано
	Lag        time.Duration // насколько старт отстал от расписания (open-loop)
	StatusCode int
	Err        error
	BytesRead  int64
}

type Config struct {
	URL         string
	Method      string
	Body        []byte
	Headers     http.Header
	Requests    int
	Duration    time.Duration // режим -z, взаимоисключающе с Requests
	Concurrency int
	Timeout     time.Duration
	Rate        float64 // >0 — open-loop с постоянной частотой; 0 — closed-loop

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

func doRequest(ctx context.Context, client *http.Client, cfg Config) Result {
	start := time.Now()

	var body io.Reader
	if len(cfg.Body) > 0 {
		body = bytes.NewReader(cfg.Body) // новый Reader каждый раз
	}

	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, body)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err}
	}
	for k, vs := range cfg.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
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
	return Result{Duration: time.Since(start), StatusCode: resp.StatusCode, BytesRead: n}
}

func (c Config) Validate() error {
	if c.URL == "" {
		return errors.New("URL не задан")
	}
	if _, err := url.Parse(c.URL); err != nil {
		return fmt.Errorf("некорректный URL: %w", err)
	}
	if c.Concurrency < 1 {
		return fmt.Errorf("concurrency должен быть >= 1, получено %d", c.Concurrency)
	}
	if c.Requests < 1 && c.Duration <= 0 {
		return errors.New("нужен либо -n, либо -z")
	}
	if c.Requests > 0 && c.Duration > 0 {
		return errors.New("-n и -z взаимоисключающи")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout должен быть > 0, получено %v", c.Timeout)
	}
	if c.Rate < 0 {
		return fmt.Errorf("rate не может быть отрицательным, получено %v", c.Rate)
	}
	return nil
}

// hasMore сообщает, нужно ли выдавать задачу номер i: в режиме -z предела по
// количеству нет, в режиме -n их ровно Requests.
func (c Config) hasMore(i int) bool {
	return c.Duration > 0 || i < c.Requests
}

func Run(ctx context.Context, cfg Config) ([]Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("некорректная конфигурация: %w", err)
	}

	// runCtx гасит выдачу новых задач; ctx живёт до Ctrl+C, поэтому запросы,
	// уже улетевшие в сеть, дорабатывают и не превращаются в фейковые таймауты
	runCtx := ctx
	if cfg.Duration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}

	tr := newTransport(cfg)
	defer tr.CloseIdleConnections()
	client := newClient(cfg, tr)

	results := make(chan Result, cfg.Concurrency)
	go func() {
		defer close(results)
		if cfg.Rate > 0 {
			openLoop(ctx, runCtx, cfg, client, results)
		} else {
			closedLoop(ctx, runCtx, cfg, client, results)
		}
	}()

	capHint := cfg.Requests
	if capHint <= 0 {
		capHint = 1024
	}
	all := make([]Result, 0, capHint)
	for r := range results {
		all = append(all, r)
	}
	return all, nil
}

// closedLoop: Concurrency воркеров, каждый шлёт следующий запрос только после
// того, как ответил сервер. Частота — какую выдержит сервер, не наша.
func closedLoop(ctx, runCtx context.Context, cfg Config, client *http.Client, results chan<- Result) {
	jobs := make(chan struct{}, cfg.Concurrency)

	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				res := doRequest(ctx, client, cfg)
				if errors.Is(res.Err, context.Canceled) {
					return
				}
				results <- res
				if runCtx.Err() != nil {
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for i := 0; cfg.hasMore(i); i++ {
			select {
			case jobs <- struct{}{}:
			case <-runCtx.Done():
				return
			}
		}
	}()

	wg.Wait()
}

// openLoop: запросы уходят по расписанию Rate в секунду независимо от того,
// ответил ли сервер на предыдущие. Concurrency здесь — потолок числа запросов
// в полёте: упёрлись в него — расписание начинает отставать, и это отставание
// оседает в Result.Lag, а не растворяется в цифрах (coordinated omission).
func openLoop(ctx, runCtx context.Context, cfg Config, client *http.Client, results chan<- Result) {
	slots := make(chan struct{}, cfg.Concurrency)

	var wg sync.WaitGroup
	defer wg.Wait()

	// один переиспользуемый таймер вместо time.After на каждый запрос:
	// на высоких частотах его аллокация сама попала бы в измерения.
	// Reset без вычитки канала корректен начиная с go 1.23 (см. go.mod)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	start := time.Now()
	for i := 0; cfg.hasMore(i); i++ {
		// расписание считаем от start, а не «предыдущий + период»,
		// иначе ошибка округления каждого шага накапливается
		due := start.Add(time.Duration(float64(i) * float64(time.Second) / cfg.Rate))

		if d := time.Until(due); d > 0 {
			timer.Reset(d)
			select {
			case <-timer.C:
			case <-runCtx.Done():
				return
			}
		}

		select {
		case slots <- struct{}{}:
		case <-runCtx.Done():
			return
		}

		wg.Add(1)
		go func(due time.Time) {
			defer wg.Done()
			defer func() { <-slots }()

			lag := time.Since(due)
			if lag < 0 {
				lag = 0
			}

			res := doRequest(ctx, client, cfg)
			res.Lag = lag
			if errors.Is(res.Err, context.Canceled) {
				return
			}
			results <- res
		}(due)
	}
}
