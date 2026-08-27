package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type Result struct {
	Duration   time.Duration // время самого запроса: отправка → тело дочитано
	Lag        time.Duration // насколько старт отстал от расписания (open-loop)
	StatusCode int
	Err        error
	BytesRead  int64
	Trace      *Trace // разбивка по фазам; nil, когда трассировка выключена
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
	Trace       bool    // собирать разбивку latency по фазам соединения

	DisableKeepAlive bool
	Insecure         bool
	HTTP2            bool
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

// offset — на сколько позже старта по расписанию должен уйти запрос номер i.
// Считаем от начала, а не «предыдущий плюс период»: иначе ошибка округления
// каждого шага накапливается.
func (c Config) offset(i int) time.Duration {
	return time.Duration(float64(i) * float64(time.Second) / c.Rate)
}

func Run(ctx context.Context, cfg Config) ([]Result, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("некорректная конфигурация: %w", err)
	}

	factory, err := newRequestFactory(cfg)
	if err != nil {
		return nil, fmt.Errorf("не удалось собрать запрос: %w", err)
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

	results := make(chan Result, cfg.Concurrency)
	e := &engine{cfg: cfg, client: newClient(cfg, tr), factory: factory, out: results}

	go func() {
		defer close(results)
		if cfg.Rate > 0 {
			e.openLoop(ctx, runCtx)
			return
		}
		e.closedLoop(ctx, runCtx)
	}()

	return collect(results, cfg.Requests), nil
}

// collect преаллоцирует слайс под ожидаемое число запросов. В режиме -z оно
// заранее неизвестно — берём разумный старт, дальше слайс растёт сам.
func collect(results <-chan Result, expected int) []Result {
	if expected <= 0 {
		expected = 1024
	}

	all := make([]Result, 0, expected)
	for r := range results {
		all = append(all, r)
	}
	return all
}
