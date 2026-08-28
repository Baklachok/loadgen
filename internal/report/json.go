package report

import (
	"encoding/json"
	"io"
	"math"
	"runtime"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

// Документ описан отдельными DTO, а не тегами на stats.Summary: time.Duration
// сериализуется наносекундами-числом, что нечитаемо, а форма контракта не
// должна меняться каждый раз, когда внутри stats переименовали поле.

// schemaVersion — версия контракта JSON-вывода.
//
// Бампается, когда поле убрано, переименовано, сменило тип или смысл —
// то есть когда чужой парсер сломается. Добавление поля версию не меняет:
// потребитель, читающий известные ему ключи, переживает это молча.
//
// Форма документа зафиксирована здесь осознанно. Хендофф предлагал сгруппировать
// счётчики под `summary`, но верхнеуровневые headline-числа дают `jq .rps`
// вместо `jq .summary.rps`, и примеры в README уже такие. Менять форму ради
// симметрии в момент её заморозки — худший из моментов.
//
// История версий — по строке на бамп, чтобы потребитель понимал, что для него
// изменилось, не читая коммиты:
//
//	2 — появился исход truncated, и failed сузился: оборванные ответы больше
//	    в него не входят. Гейт по failed перестанет их ловить, если не добавить
//	    truncated. Само поле truncated — добавление и версию бы не двигало;
//	    двигает её смена смысла у failed.
//	1 — первая зафиксированная форма.
const schemaVersion = 2

type jsonSummary struct {
	Schema int        `json:"schema"`
	Config jsonConfig `json:"config"`

	// Порядок полей — это порядок ключей в документе: encoding/json пишет их
	// как объявлены. Группы разделены пустыми строками, а не перестановкой.

	Total   int  `json:"total"`
	Warmup  int  `json:"warmup_discarded"`
	Partial bool `json:"partial"`

	// Четыре исхода, дающие в сумме total. non_2xx и truncated разведены
	// не ради симметрии: сервис, отдающий отказы, и сервис, рвущий соединения,
	// требуют противоположных действий, а выглядели одинаково.
	OK        int `json:"ok"`
	NonOK     int `json:"non_2xx"`
	Failed    int `json:"failed"`
	Truncated int `json:"truncated"`

	// client_errors — подмножество failed: кончились ресурсы у нас, а не
	// у сервиса. Ненулевое значение обесценивает весь документ.
	ClientErrors int     `json:"client_errors"`
	SuccessRate  float64 `json:"success_rate"`

	ElapsedMs float64 `json:"elapsed_ms"`
	WindowMs  float64 `json:"window_ms"`

	RPS           float64 `json:"rps"`
	TargetRate    float64 `json:"target_rate"`
	RateShortfall float64 `json:"rate_shortfall"`
	Late          int     `json:"late"`
	LateShare     float64 `json:"late_share"`

	BytesRead     int64   `json:"bytes_read"`
	ThroughputMBs float64 `json:"throughput_mb_s"`

	// Необязательные блоки — указатели, и это осознанный отказ от Null Object:
	// пустая структура на месте отсутствующей подменила бы «не измеряли»
	// на «измерили и вышли нули». Для машинного потребителя это разные факты,
	// и переспросить он не может.
	Latency jsonLatencies `json:"latency"`
	// В closed-loop поправки нет — поля не должны появляться вовсе,
	// иначе потребитель решит, что расписание было и оно совпало.
	Corrected *jsonLatencies `json:"corrected,omitempty"`
	MaxLagMs  *float64       `json:"max_lag_ms,omitempty"`

	// Без -trace фаз не измеряли — поля быть не должно вовсе,
	// иначе потребитель решит, что все они нулевые.
	Trace *jsonTrace `json:"trace,omitempty"`

	Histogram []jsonBucket            `json:"histogram"`
	Codes     map[int]int             `json:"codes"`
	Errors    map[stats.ErrorKind]int `json:"errors"`
}

// Перцентили — указатели: на недостаточной выборке они уезжают в null.
// Число здесь было бы такой же ложью, как в текстовом отчёте, только
// потребитель у него автоматический и переспросить не может.
type jsonLatencies struct {
	Samples int      `json:"samples"`
	MinMs   float64  `json:"min_ms"`
	MeanMs  float64  `json:"mean_ms"`
	P50Ms   *float64 `json:"p50_ms"`
	P90Ms   *float64 `json:"p90_ms"`
	P95Ms   *float64 `json:"p95_ms"`
	P99Ms   *float64 `json:"p99_ms"`
	MaxMs   float64  `json:"max_ms"`
}

type jsonBucket struct {
	UpperMs float64 `json:"upper_ms"`
	Count   int     `json:"count"`
}

// jsonPhase не обнуляет перцентили на малой выборке, в отличие от
// jsonLatencies, и это не недосмотр. Фазы соединения по своей природе
// измеряются единицами замеров: при keep-alive рукопожатие делает только
// первый запрос. Обнулить их значило бы не показать ничего и никогда.
// Вместо этого рядом всегда стоит samples — читатель видит, что число
// посчитано по двум замерам, и сам решает, что оно стоит.
type jsonPhase struct {
	Samples int     `json:"samples"`
	P50Ms   float64 `json:"p50_ms"`
	P90Ms   float64 `json:"p90_ms"`
	P99Ms   float64 `json:"p99_ms"`
	MaxMs   float64 `json:"max_ms"`
}

type jsonTrace struct {
	Traced  int       `json:"traced"`
	Reused  int       `json:"reused"`
	DNS     jsonPhase `json:"dns"`
	Connect jsonPhase `json:"connect"`
	TLS     jsonPhase `json:"tls"`
	TTFB    jsonPhase `json:"ttfb"`
}

// jsonConfig — то, что заказывали: те же поля, что в блоке «Прогон»
// текстового отчёта. Без них сохранённый JSON через полгода не воспроизвести.
//
// Длительности прогона здесь нет намеренно: elapsed_ms лежит на верхнем
// уровне, потому что это результат, а не настройка. Текстовый блок мешает
// их в одну таблицу — человеку так удобнее, машине нет.
type jsonConfig struct {
	Version     string  `json:"version"`
	URL         string  `json:"url"`
	Method      string  `json:"method"`
	Requests    int     `json:"requests"`
	DurationMs  float64 `json:"duration_ms"`
	Concurrency int     `json:"concurrency"`
	Rate        float64 `json:"rate"`
	TimeoutMs   float64 `json:"timeout_ms"`
	KeepAlive   bool    `json:"keepalive"`
	Proto       string  `json:"proto"`
	GOMAXPROCS  int     `json:"gomaxprocs"`
	StartedAt   string  `json:"started_at"`
}

// Конвертеры ниже названы единообразно — xxxJSON: «превратить внутренний тип
// в кусок документа». Раньше они звались latencies, phase, buckets, traceJSON
// и runConfig, и предсказать имя было нельзя.

// ms переводит длительность в миллисекунды с точностью до микросекунды:
// наносекунды в отчёте — шум, а float без округления даёт хвосты вида 5.000000001.
func ms(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Microsecond)) / 1000
}

func latenciesJSON(l stats.Latencies) jsonLatencies {
	// percentile отдаёт nil, когда замеров не хватило на осмысленное число.
	percentile := func(d time.Duration, q float64) *float64 {
		if !l.Reliable(q) {
			return nil
		}
		v := ms(d)
		return &v
	}

	return jsonLatencies{
		Samples: l.Samples,
		MinMs:   ms(l.Min),
		MeanMs:  ms(l.Mean),
		P50Ms:   percentile(l.P50, 0.50),
		P90Ms:   percentile(l.P90, 0.90),
		P95Ms:   percentile(l.P95, 0.95),
		P99Ms:   percentile(l.P99, 0.99),
		MaxMs:   ms(l.Max),
	}
}

func phaseJSON(ph stats.PhaseStats) jsonPhase {
	return jsonPhase{
		Samples: ph.Samples,
		P50Ms:   ms(ph.P50),
		P90Ms:   ms(ph.P90),
		P99Ms:   ms(ph.P99),
		MaxMs:   ms(ph.Max),
	}
}

// writeJSON только кодирует. Сборка документа — отдельно: раньше эта
// функция и собирала, и решала, какие блоки включить, и кодировала.
func writeJSON(w io.Writer, s stats.Summary, opt Options) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(summaryJSON(s, opt))
}

func summaryJSON(s stats.Summary, opt Options) jsonSummary {
	out := jsonSummary{
		Schema: schemaVersion,
		Config: configJSON(opt.Run),

		Total:   s.Total,
		Warmup:  s.Warmup,
		Partial: s.Partial,

		OK:        s.OK,
		NonOK:     s.NonOK,
		Failed:    s.Failed,
		Truncated: s.Truncated,

		ClientErrors: s.ClientErrors,
		SuccessRate:  s.SuccessRate(),

		ElapsedMs: ms(s.Elapsed),
		WindowMs:  ms(s.Window),

		RPS:           s.RPS,
		TargetRate:    s.TargetRate,
		RateShortfall: s.RateShortfall(),
		Late:          s.Late,
		LateShare:     s.LateShare(),

		BytesRead:     s.BytesRead,
		ThroughputMBs: s.Throughput,
		Latency:       latenciesJSON(s.Latency),
		Histogram:     bucketsJSON(s.Histogram),
		Codes:         s.Codes,
		Errors:        s.Errors,
		Trace:         traceJSON(s.Trace),
	}

	// В closed-loop расписания не было, и поправка к нему бессмысленна.
	if opt.OpenLoop {
		corrected := latenciesJSON(s.Corrected)
		lag := ms(s.MaxLag)
		out.Corrected, out.MaxLagMs = &corrected, &lag
	}

	return out
}

// buckets возвращает пустой слайс, а не nil: в JSON это [] вместо null,
// и потребителю не нужна отдельная ветка на «гистограммы не было».
func bucketsJSON(src []stats.Bucket) []jsonBucket {
	out := make([]jsonBucket, 0, len(src))
	for _, b := range src {
		out = append(out, jsonBucket{UpperMs: ms(b.Upper), Count: b.Count})
	}
	return out
}

// traceJSON возвращает nil, когда трассировки не было: отсутствие поля
// и нули в нём — разные утверждения.
func traceJSON(t *stats.TraceSummary) *jsonTrace {
	if t == nil {
		return nil
	}
	return &jsonTrace{
		Traced:  t.Traced,
		Reused:  t.Reused,
		DNS:     phaseJSON(t.DNS),
		Connect: phaseJSON(t.Connect),
		TLS:     phaseJSON(t.TLS),
		TTFB:    phaseJSON(t.TTFB),
	}
}

func configJSON(run RunInfo) jsonConfig {
	cfg := run.Config
	return jsonConfig{
		Version:     run.Version,
		URL:         cfg.URL,
		Method:      cfg.Method,
		Requests:    cfg.Requests,
		DurationMs:  ms(cfg.Duration),
		Concurrency: cfg.Concurrency,
		Rate:        cfg.Rate,
		TimeoutMs:   ms(cfg.Timeout),
		KeepAlive:   !cfg.DisableKeepAlive,
		Proto:       run.Proto,
		GOMAXPROCS:  runtime.GOMAXPROCS(0),
		StartedAt:   run.StartedAt.Format(time.RFC3339),
	}
}
