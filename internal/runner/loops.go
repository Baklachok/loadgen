// Две формы нагрузки. Обе пользуются engine как исполнителем и ничего
// не знают ни про прогрев, ни про трассировку — это его забота.
package runner

import (
	"context"
	"sync"
	"time"
)

// loop выбирает форму нагрузки. Развилка живёт здесь, рядом с обеими
// формами, а не в Run: тот собирает прогон и не должен знать, из чего
// именно выбирает.
func (e *engine) loop(ctx, runCtx context.Context) {
	if e.cfg.Rate > 0 {
		e.openLoop(ctx, runCtx)
		return
	}
	e.closedLoop(ctx, runCtx)
}

// hasMore и offset — это расписание, а не настройка. Живут рядом с циклами,
// которые их спрашивают: меняются они вместе с формой нагрузки, а не вместе
// с набором флагов.

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
