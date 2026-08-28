package runner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync/atomic"
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

	runStart time.Time    // отсчёт прогрева по времени
	started  atomic.Int64 // отсчёт прогрева по количеству

	// measuredFrom — момент старта первого запроса, попавшего в измерения,
	// в наносекундах Unix. Ноль означает, что мерить ещё не начинали.
	measuredFrom atomic.Int64

	// proto — протокол первого ответа. Хранится строкой, а не в каждом
	// Result: на миллионах запросов это были бы лишние сотни мегабайт
	// ради значения, одинакового для всего прогона.
	proto atomic.Pointer[string]
}

// newEngine фиксирует момент старта: от него отсчитывается и прогрев,
// и полная длительность прогона, поэтому взять его надо один раз и строго
// до первого запроса.
func newEngine(cfg Config, factory *requestFactory, tr *http.Transport, out chan<- Result) *engine {
	return &engine{
		cfg:      cfg,
		client:   newClient(cfg, tr),
		factory:  factory,
		out:      out,
		runStart: time.Now(),
	}
}

// report собирает итог прогона. Живёт на engine, а не в Run, потому что
// половину полей знает только он: когда начали, когда начали мерить
// и о чём в итоге договорились с сервером.
func (e *engine) report(end time.Time, interrupted bool) Report {
	return Report{
		Elapsed:     end.Sub(e.runStart),
		Window:      e.measuredWindow(end),
		TargetRate:  e.cfg.Rate,
		StartedAt:   e.runStart,
		Proto:       e.observedProto(),
		Interrupted: interrupted,
	}
}

// observedProto — по чему реально договорились с сервером. Пусто, если
// ни один ответ не пришёл.
func (e *engine) observedProto() string {
	if p := e.proto.Load(); p != nil {
		return *p
	}
	return ""
}

// measuredWindow — сколько времени длилось измерение. Без прогрева совпадает
// с длительностью прогона; с прогревом короче ровно на него.
func (e *engine) measuredWindow(end time.Time) time.Duration {
	from := e.measuredFrom.Load()
	if from == 0 {
		return 0
	}
	return end.Sub(time.Unix(0, from))
}

// isWarmup решает судьбу запроса в момент его старта, а не завершения:
// иначе медленный запрос из прогрева «переедет» в измерения ровно тогда,
// когда прогрев важнее всего.
func (e *engine) isWarmup(at time.Time) bool {
	if e.cfg.WarmupDuration > 0 {
		return at.Sub(e.runStart) < e.cfg.WarmupDuration
	}
	if e.cfg.WarmupRequests > 0 {
		return e.started.Add(1) <= int64(e.cfg.WarmupRequests)
	}
	return false
}

// markMeasured запоминает старт первого измеряемого запроса. CAS, а не
// проверка с присваиванием: запросы стартуют из разных горутин.
func (e *engine) markMeasured(at time.Time) {
	e.measuredFrom.CompareAndSwap(0, at.UnixNano())
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

	// Копия, а не &resp.Proto: указатель внутрь ответа удержал бы весь
	// http.Response живым до конца прогона. CAS без проверки — промах
	// безвреден, побеждает первый ответивший.
	proto := resp.Proto
	e.proto.CompareAndSwap(nil, &proto)

	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		// Код ответа переносим и сюда: заголовки уже пришли, и «503, тело
		// оборвалось» — это совсем не то же самое, что «сервис недоступен».
		// Тот же случай, что и с proto выше: знание из частично прочитанного
		// ответа терять незачем.
		return Result{
			Duration:   time.Since(start),
			StatusCode: resp.StatusCode,
			Err:        err,
			BytesRead:  n,
			Trace:      t.snapshot(),
		}
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
	now := time.Now()

	warmup := e.isWarmup(now)
	if !warmup {
		e.markMeasured(now)
	}

	res := e.do(ctx)
	res.Lag = lag
	res.Warmup = warmup

	if errors.Is(res.Err, context.Canceled) {
		return
	}
	e.out <- res
}
