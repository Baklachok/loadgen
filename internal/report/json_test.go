package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

// Поля документа: каждое несёт то, что обещает его имя.
func TestJSONFields(t *testing.T) {
	t.Run("ядро документа", func(t *testing.T) {
		var got struct {
			Total   int `json:"total"`
			Latency struct {
				P99Ms float64 `json:"p99_ms"`
			} `json:"latency"`
			Corrected *struct {
				P99Ms float64 `json:"p99_ms"`
			} `json:"corrected"`
			MaxLagMs  *float64       `json:"max_lag_ms"`
			Codes     map[string]int `json:"codes"`
			Errors    map[string]int `json:"errors"`
			Histogram []struct {
				UpperMs float64 `json:"upper_ms"`
				Count   int     `json:"count"`
			} `json:"histogram"`
		}

		decodeJSON(t, sample(), Options{OpenLoop: true}, &got)

		if got.Total != 100 {
			t.Errorf("total = %d, want 100", got.Total)
		}
		if got.Latency.P99Ms != 80 {
			t.Errorf("latency.p99_ms = %v, want 80", got.Latency.P99Ms)
		}
		if got.Corrected == nil || got.Corrected.P99Ms != 110 {
			t.Errorf("corrected = %+v, want p99_ms=110", got.Corrected)
		}
		if got.MaxLagMs == nil || *got.MaxLagMs != 30 {
			t.Errorf("max_lag_ms = %v, want 30", got.MaxLagMs)
		}
		if got.Codes["200"] != 90 || got.Codes["503"] != 3 {
			t.Errorf("codes = %v", got.Codes)
		}
		if got.Errors["timeout"] != 2 {
			t.Errorf("errors = %v", got.Errors)
		}
		if len(got.Histogram) != 3 || got.Histogram[0].Count != 80 {
			t.Errorf("histogram = %+v", got.Histogram)
		}
	})

	// Заголовок отчёта не должен читаться как успех, когда сервис отдавал
	// одни отказы: ровно эта строка раньше и врала.
	t.Run("исходы по отдельности", func(t *testing.T) {
		var got struct {
			Total       int     `json:"total"`
			OK          int     `json:"ok"`
			NonOK       int     `json:"non_2xx"`
			Failed      int     `json:"failed"`
			SuccessRate float64 `json:"success_rate"`
		}

		decodeJSON(t, sample(), Options{}, &got)

		if got.Total != 100 || got.OK != 90 || got.NonOK != 8 || got.Failed != 2 {
			t.Errorf("исходы: %+v", got)
		}
		if got.SuccessRate != 0.9 {
			t.Errorf("success_rate = %v, ожидалось 0.9", got.SuccessRate)
		}
	})

	// Строка прогрева появляется только когда он был: постоянный «Прогрев: 0»
	// приучает не читать шапку.
	t.Run("отброшенный прогрев", func(t *testing.T) {
		s := sample()
		s.Warmup = 42

		var got struct {
			Warmup int `json:"warmup_discarded"`
		}
		decodeJSON(t, s, Options{}, &got)
		if got.Warmup != 42 {
			t.Errorf("warmup_discarded = %d, ожидалось 42", got.Warmup)
		}
	})

	t.Run("окно измерения", func(t *testing.T) {
		s := sample()
		s.Elapsed, s.Window = 6*time.Second, 1500*time.Millisecond

		var got struct {
			ElapsedMs float64 `json:"elapsed_ms"`
			WindowMs  float64 `json:"window_ms"`
		}
		decodeJSON(t, s, Options{}, &got)

		if got.ElapsedMs != 6000 || got.WindowMs != 1500 {
			t.Errorf("elapsed_ms=%v window_ms=%v, ожидалось 6000 и 1500", got.ElapsedMs, got.WindowMs)
		}
	})

	t.Run("недобор частоты", func(t *testing.T) {
		s := sample()
		s.TargetRate, s.RPS = 1000, 400

		var got struct {
			TargetRate    float64 `json:"target_rate"`
			RateShortfall float64 `json:"rate_shortfall"`
		}
		decodeJSON(t, s, Options{}, &got)

		if got.TargetRate != 1000 {
			t.Errorf("target_rate = %v, ожидалось 1000", got.TargetRate)
		}
		if got.RateShortfall != 0.6 {
			t.Errorf("rate_shortfall = %v, ожидалось 0.6", got.RateShortfall)
		}
	})

	t.Run("опоздавшие запросы", func(t *testing.T) {
		s := sample()
		s.Total, s.Late = 1000, 250

		var got struct {
			Late      int     `json:"late"`
			LateShare float64 `json:"late_share"`
		}
		decodeJSON(t, s, Options{}, &got)
		if got.Late != 250 || got.LateShare != 0.25 {
			t.Errorf("late=%d late_share=%v, ожидалось 250 и 0.25", got.Late, got.LateShare)
		}
	})

	t.Run("прерванный прогон", func(t *testing.T) {
		var full, cut struct {
			Partial bool `json:"partial"`
		}
		decodeJSON(t, sample(), Options{}, &full)

		s := sample()
		s.Partial = true
		decodeJSON(t, s, Options{}, &cut)

		if full.Partial {
			t.Error("partial=true на полном прогоне")
		}
		if !cut.Partial {
			t.Error("partial=false на прерванном")
		}
	})

	t.Run("конфигурация прогона", func(t *testing.T) {
		opt := Options{Run: RunInfo{
			Version:   "v0.1.1",
			Proto:     "HTTP/2.0",
			StartedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
			Config: runner.Config{
				URL: "http://example/api", Method: "POST",
				Requests: 1000, Concurrency: 20, Rate: 500,
				Timeout: 7 * time.Second, DisableKeepAlive: true,
			},
		}}

		var got struct {
			Config struct {
				Version     string  `json:"version"`
				URL         string  `json:"url"`
				Method      string  `json:"method"`
				Requests    int     `json:"requests"`
				Concurrency int     `json:"concurrency"`
				Rate        float64 `json:"rate"`
				TimeoutMs   float64 `json:"timeout_ms"`
				KeepAlive   bool    `json:"keepalive"`
				Proto       string  `json:"proto"`
				GOMAXPROCS  int     `json:"gomaxprocs"`
				StartedAt   string  `json:"started_at"`
			} `json:"config"`
		}
		decodeJSON(t, sample(), opt, &got)

		c := got.Config
		if c.Version != "v0.1.1" || c.URL != "http://example/api" || c.Method != "POST" {
			t.Errorf("цель и версия: %+v", c)
		}
		if c.Requests != 1000 || c.Concurrency != 20 || c.Rate != 500 || c.TimeoutMs != 7000 {
			t.Errorf("флаги прогона: %+v", c)
		}
		if c.KeepAlive {
			t.Error("keepalive=true при -disable-keepalive")
		}
		if c.Proto != "HTTP/2.0" || c.StartedAt != "2026-08-28T12:00:00Z" {
			t.Errorf("протокол и время: proto=%q started_at=%q", c.Proto, c.StartedAt)
		}
		if c.GOMAXPROCS < 1 {
			t.Errorf("gomaxprocs = %d", c.GOMAXPROCS)
		}
	})
}

// Отсутствие поля — такое же утверждение, как значение в нём.
// «Не измеряли» и «измерили и вышли нули» для машины разные факты,
// а переспросить она не может.
func TestJSONAbsence(t *testing.T) {
	t.Run("нет поправки без расписания", func(t *testing.T) {
		out := string(renderJSON(t, sample(), Options{OpenLoop: false}))

		if strings.Contains(out, "corrected") || strings.Contains(out, "max_lag_ms") {
			t.Error("в closed-loop полей поправки быть не должно: иначе потребитель решит, что расписание было")
		}
	})

	t.Run("нет фаз без -trace", func(t *testing.T) {
		if out := string(renderJSON(t, sample(), Options{})); strings.Contains(out, `"trace"`) {
			t.Error("без -trace секции trace в JSON быть не должно")
		}

		var got struct {
			Trace *struct {
				Traced  int `json:"traced"`
				Reused  int `json:"reused"`
				Connect struct {
					Samples int `json:"samples"`
				} `json:"connect"`
			} `json:"trace"`
		}
		decodeJSON(t, traced(), Options{}, &got)

		if got.Trace == nil || got.Trace.Traced != 1000 || got.Trace.Reused != 998 {
			t.Errorf("trace = %+v", got.Trace)
		}
		if got.Trace.Connect.Samples != 2 {
			t.Errorf("connect.samples = %d, ожидалось 2", got.Trace.Connect.Samples)
		}
	})

	// Для машины недостоверный перцентиль — null, а не число: переспросить
	// она не может, и любое число примет за факт.
	t.Run("null вместо недостоверного перцентиля", func(t *testing.T) {
		s := sample()
		s.Latency.Samples = 50

		var got struct {
			Latency struct {
				Samples int      `json:"samples"`
				P50Ms   *float64 `json:"p50_ms"`
				P99Ms   *float64 `json:"p99_ms"`
				MaxMs   float64  `json:"max_ms"`
			} `json:"latency"`
		}
		decodeJSON(t, s, Options{}, &got)

		if got.Latency.Samples != 50 {
			t.Errorf("samples = %d, ожидалось 50", got.Latency.Samples)
		}
		if got.Latency.P99Ms != nil {
			t.Errorf("p99_ms = %v, ожидался null на 50 замерах", *got.Latency.P99Ms)
		}
		if got.Latency.P50Ms == nil {
			t.Error("p50_ms обнулён, хотя порог для него всего 20 замеров")
		}
		// max — не перцентиль, он остаётся числом при любой выборке
		if got.Latency.MaxMs != 90 {
			t.Errorf("max_ms = %v, ожидалось 90", got.Latency.MaxMs)
		}
	})

	t.Run("пустая гистограмма это [], а не null", func(t *testing.T) {
		empty := stats.Summary{Codes: map[int]int{}, Errors: map[stats.ErrorKind]int{}}

		var fields map[string]any
		decodeJSON(t, empty, Options{}, &fields)
		if fields["histogram"] == nil {
			t.Error("histogram должен быть [], а не null — потребителю проще итерироваться")
		}
	})
}

// Версия схемы — первое, что должен прочитать чужой парсер, и единственное,
// на что он может опереться, решая, понимает ли он документ.
func TestJSONSchemaVersion(t *testing.T) {
	var got struct {
		Schema int `json:"schema"`
	}
	raw := decodeJSON(t, sample(), Options{}, &got)

	if got.Schema != schemaVersion {
		t.Errorf("schema = %d, ожидалось %d", got.Schema, schemaVersion)
	}
	if schemaVersion < 1 {
		t.Errorf("schemaVersion = %d: версия начинается с единицы", schemaVersion)
	}

	// Первым полем — чтобы версию было видно, не разбирая документ целиком
	if idx := strings.Index(string(raw), `"schema"`); idx < 0 || idx > 20 {
		t.Errorf("schema не в начале документа (позиция %d):\n%s", idx, raw[:60])
	}
}
