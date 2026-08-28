// Прогон целиком: что он принимает на вход, что отдаёт наружу и как
// собирается из движка, расписания и клиента.
package runner

import (
	"context"
	"fmt"
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

	runCtx, cancel := deadline(ctx, cfg)
	defer cancel()

	tr := newTransport(cfg)
	defer tr.CloseIdleConnections()

	results := make(chan Result, cfg.Concurrency)
	e := newEngine(cfg, factory, tr, results)

	go func() {
		defer close(results)
		e.loop(ctx, runCtx)
	}()

	all := collect(results, cfg.Requests)

	// Дедлайн -z живёт в runCtx; отменённым ctx бывает только по сигналу.
	return e.report(all, time.Now(), ctx.Err() != nil), nil
}

// deadline даёт контекст, гасящий выдачу новых задач по -z. Родительский ctx
// живёт дольше: запросы, уже улетевшие в сеть, обязаны дорабатывать, иначе
// они превратятся в фейковые таймауты на последней секунде прогона.
func deadline(ctx context.Context, cfg Config) (context.Context, context.CancelFunc) {
	if cfg.Duration > 0 {
		return context.WithTimeout(ctx, cfg.Duration)
	}
	return context.WithCancel(ctx)
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
