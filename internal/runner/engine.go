package runner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// engine — общее для обоих режимов: чем слать и куда складывать результаты.
// Контексты сюда не кладём, они приходят параметрами: их два и живут они
// разное время, прятать такое в поле — верный способ перепутать.
type engine struct {
	cfg     Config
	client  *http.Client
	factory *requestFactory
	out     chan<- Result
}

// do выполняет один запрос и замеряет его целиком: от отправки до дочитанного тела.
func (e *engine) do(ctx context.Context) Result {
	// Трассировку вешаем на контекст до сборки запроса: factory всё равно
	// клонирует его с этим контекстом, лишнего копирования не возникает.
	var t *tracer
	if e.cfg.Trace {
		t = &tracer{}
		ctx = httptrace.WithClientTrace(ctx, t.hooks())
	}

	start := time.Now()

	req, err := e.factory.request(ctx)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err}
	}

	resp, err := e.client.Do(req)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err, Trace: t.snapshot()}
	}
	defer resp.Body.Close()

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err, BytesRead: n, Trace: t.snapshot()}
	}
	return Result{
		Duration:   time.Since(start),
		StatusCode: resp.StatusCode,
		BytesRead:  n,
		Trace:      t.snapshot(),
	}
}

// emit шлёт запрос и кладёт результат в канал. Отменённый запрос — это Ctrl+C,
// а не отказ сервера, и в статистике ему не место.
func (e *engine) emit(ctx context.Context, lag time.Duration) {
	res := e.do(ctx)
	res.Lag = lag

	if errors.Is(res.Err, context.Canceled) {
		return
	}
	e.out <- res
}

// closedLoop: Concurrency воркеров, каждый шлёт следующий запрос только после
// того, как ответил сервер. Частота — какую выдержит сервер, не наша.
func (e *engine) closedLoop(ctx, runCtx context.Context) {
	jobs := make(chan struct{}, e.cfg.Concurrency)
	go e.feed(runCtx, jobs)

	var wg sync.WaitGroup
	for i := 0; i < e.cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range jobs {
				e.emit(ctx, 0)
				if runCtx.Err() != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
}

// feed выдаёт задачи, пока прогон не исчерпан и не отменён.
func (e *engine) feed(runCtx context.Context, jobs chan<- struct{}) {
	defer close(jobs)

	for i := 0; e.cfg.hasMore(i); i++ {
		select {
		case jobs <- struct{}{}:
		case <-runCtx.Done():
			return
		}
	}
}

// openLoop: запросы уходят по расписанию Rate в секунду независимо от того,
// ответил ли сервер на предыдущие. Concurrency здесь — потолок числа запросов
// в полёте: упёрлись в него — расписание начинает отставать, и это отставание
// оседает в Result.Lag, а не растворяется в цифрах (coordinated omission).
func (e *engine) openLoop(ctx, runCtx context.Context) {
	slots := make(chan struct{}, e.cfg.Concurrency)

	var wg sync.WaitGroup
	defer wg.Wait()

	// Один переиспользуемый таймер вместо time.After на каждый запрос:
	// на высоких частотах его аллокация сама попала бы в измерения.
	// Reset без вычитки канала корректен начиная с go 1.23 (см. go.mod).
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	start := time.Now()
	for i := 0; e.cfg.hasMore(i); i++ {
		due := start.Add(e.cfg.offset(i))

		if wait := time.Until(due); wait > 0 {
			timer.Reset(wait)
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
		go func() {
			defer wg.Done()
			defer func() { <-slots }()

			e.emit(ctx, max(0, time.Since(due)))
		}()
	}
}
