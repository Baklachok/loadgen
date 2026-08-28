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
	Warmup     bool   // запрос из прогрева: в статистику не идёт
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

	// Прогрев: первые запросы платят за TCP, TLS и холодные кэши сервера,
	// к установившейся нагрузке это отношения не имеет. Задаётся либо числом
	// запросов, либо длительностью — не тем и другим сразу. Прогрев входит
	// в прогон, а не добавляется к нему: -n 1000 -warmup 100 шлёт 1000
	// запросов, из которых измеряются 900.
	WarmupRequests int
	WarmupDuration time.Duration

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
	return c.validateWarmup()
}

func (c Config) validateWarmup() error {
	if c.WarmupRequests < 0 || c.WarmupDuration < 0 {
		return errors.New("прогрев не может быть отрицательным")
	}
	if c.WarmupRequests > 0 && c.WarmupDuration > 0 {
		return errors.New("прогрев задаётся либо числом запросов, либо длительностью, но не обоими")
	}

	// Прогрев, съедающий весь прогон, оставляет пустую статистику —
	// это молчаливо бесполезный результат, лучше сказать сразу.
	if c.Requests > 0 && c.WarmupRequests >= c.Requests {
		return fmt.Errorf("прогрев в %d запросов не оставляет что измерять из %d", c.WarmupRequests, c.Requests)
	}
	if c.Duration > 0 && c.WarmupDuration >= c.Duration {
		return fmt.Errorf("прогрев в %v не оставляет времени на измерение из %v", c.WarmupDuration, c.Duration)
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

// Report — что дал прогон. Окно измерения отдаётся вместе с замерами
// намеренно: делить измеренные запросы на полную длительность прогона —
// значит занижать RPS ровно на долю прогрева.
type Report struct {
	Results []Result

	Elapsed time.Duration // весь прогон, от старта до последнего результата
	Window  time.Duration // от старта первого измеряемого запроса до конца

	// TargetRate — заданная частота open-loop, 0 в closed-loop. Едет вместе
	// с замерами, чтобы отчёт мог сопоставить обещанное с полученным.
	TargetRate float64

	// StartedAt и Proto нужны отчёту, чтобы прогон можно было повторить
	// через полгода. Протокол известен только после первого ответа, поэтому
	// приходит отсюда, а не из конфига: сервер мог договориться не о том,
	// что просили.
	StartedAt time.Time
	Proto     string

	// Interrupted — прогон оборвали сигналом, а не расписанием. Отчёт обязан
	// это показать: частичный результат, неотличимый от полного, становится
	// скриншотом «держим 4000 rps», сделанным по прерванному прогону.
	Interrupted bool
}

func Run(ctx context.Context, cfg Config) (Report, error) {
	if err := cfg.Validate(); err != nil {
		return Report{}, fmt.Errorf("некорректная конфигурация: %w", err)
	}

	factory, err := newRequestFactory(cfg)
	if err != nil {
		return Report{}, fmt.Errorf("не удалось собрать запрос: %w", err)
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
	e := &engine{
		cfg:      cfg,
		client:   newClient(cfg, tr),
		factory:  factory,
		out:      results,
		runStart: time.Now(),
	}

	go func() {
		defer close(results)
		if cfg.Rate > 0 {
			e.openLoop(ctx, runCtx)
			return
		}
		e.closedLoop(ctx, runCtx)
	}()

	all := collect(results, cfg.Requests)
	end := time.Now()

	return Report{
		Results:    all,
		Elapsed:    end.Sub(e.runStart),
		Window:     e.measuredWindow(end),
		TargetRate: cfg.Rate,
		StartedAt:  e.runStart,
		Proto:      e.observedProto(),
		// Дедлайн -z живёт в runCtx; отменённым ctx бывает только по сигналу.
		Interrupted: ctx.Err() != nil,
	}, nil
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
