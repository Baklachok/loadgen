package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Baklachok/loadgen/internal/stats"
)

func TestJSONShape(t *testing.T) {
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

	raw := renderJSON(t, sample(), Options{OpenLoop: true})
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("невалидный JSON: %v\n%s", err, raw)
	}

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
}

func TestJSONOmitsCorrectedInClosedLoop(t *testing.T) {
	out := string(renderJSON(t, sample(), Options{OpenLoop: false}))

	if strings.Contains(out, "corrected") || strings.Contains(out, "max_lag_ms") {
		t.Error("в closed-loop полей поправки быть не должно: иначе потребитель решит, что расписание было")
	}
}

func TestJSONOmitsTraceWhenNotMeasured(t *testing.T) {
	if out := string(renderJSON(t, sample(), Options{})); strings.Contains(out, `"trace"`) {
		t.Error("без -trace секции trace в JSON быть не должно")
	}

	var got struct {
		Trace *struct {
			Traced  int `json:"traced"`
			Reused  int `json:"reused"`
			Connect struct {
				Count int `json:"count"`
			} `json:"connect"`
		} `json:"trace"`
	}
	if err := json.Unmarshal(renderJSON(t, traced(), Options{}), &got); err != nil {
		t.Fatalf("невалидный JSON: %v", err)
	}

	if got.Trace == nil || got.Trace.Traced != 1000 || got.Trace.Reused != 998 {
		t.Errorf("trace = %+v", got.Trace)
	}
	if got.Trace.Connect.Count != 2 {
		t.Errorf("connect.count = %d, ожидалось 2", got.Trace.Connect.Count)
	}
}

func TestJSONOnEmptyRun(t *testing.T) {
	empty := stats.Summary{Codes: map[int]int{}, Errors: map[stats.ErrorKind]int{}}

	var fields map[string]any
	if err := json.Unmarshal(renderJSON(t, empty, Options{}), &fields); err != nil {
		t.Fatalf("невалидный JSON на пустом прогоне: %v", err)
	}
	if fields["histogram"] == nil {
		t.Error("histogram должен быть [], а не null — потребителю проще итерироваться")
	}
}

// Заголовок отчёта не должен читаться как успех, когда сервис отдавал
// одни отказы: ровно эта строка раньше и врала.

func TestJSONReportsOutcomesSeparately(t *testing.T) {
	var got struct {
		Total       int     `json:"total"`
		OK          int     `json:"ok"`
		NonOK       int     `json:"non_2xx"`
		Failed      int     `json:"failed"`
		SuccessRate float64 `json:"success_rate"`
	}

	if err := json.Unmarshal(renderJSON(t, sample(), Options{}), &got); err != nil {
		t.Fatal(err)
	}

	if got.Total != 100 || got.OK != 90 || got.NonOK != 8 || got.Failed != 2 {
		t.Errorf("исходы: %+v", got)
	}
	if got.SuccessRate != 0.9 {
		t.Errorf("success_rate = %v, ожидалось 0.9", got.SuccessRate)
	}
}

// Строка прогрева появляется только когда он был: постоянный «Прогрев: 0»
// приучает не читать шапку.

func TestJSONReportsWarmup(t *testing.T) {
	s := sample()
	s.Warmup = 42

	var got struct {
		Warmup int `json:"warmup_discarded"`
	}
	if err := json.Unmarshal(renderJSON(t, s, Options{}), &got); err != nil {
		t.Fatal(err)
	}
	if got.Warmup != 42 {
		t.Errorf("warmup_discarded = %d, ожидалось 42", got.Warmup)
	}
}
