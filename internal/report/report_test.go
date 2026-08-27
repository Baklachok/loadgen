package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Baklachok/loadgen/internal/stats"
)

func millis(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// render отдаёт весь текстовый отчёт строкой: тестам интересна не запись
// в io.Writer, а то, что в ней оказалось.
func render(s stats.Summary, opt Options) string {
	var buf bytes.Buffer
	Text(&buf, s, opt)
	return buf.String()
}

func renderJSON(t *testing.T, s stats.Summary, opt Options) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := JSON(&buf, s, opt); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sample() stats.Summary {
	return stats.Summary{
		Total: 100, Success: 98, Failed: 2,
		Elapsed: 2 * time.Second, RPS: 49,
		Latency:   stats.Latencies{Min: millis(1), Mean: millis(5), Max: millis(90), P50: millis(4), P90: millis(9), P95: millis(30), P99: millis(80)},
		Corrected: stats.Latencies{Min: millis(1), Mean: millis(9), Max: millis(120), P50: millis(5), P90: millis(40), P95: millis(70), P99: millis(110)},
		MaxLag:    millis(30),
		Histogram: []stats.Bucket{
			{Upper: millis(10), Count: 80},
			{Upper: millis(20), Count: 3},
			{Upper: millis(120), Count: 15},
		},
		BytesRead: 4096, Throughput: 0.002,
		Codes:  map[int]int{200: 90, 404: 5, 503: 3},
		Errors: map[stats.ErrorKind]int{stats.ErrTimeout: 2},
	}
}

// traced — тот же прогон, но с -trace. Соединялись двое из тысячи,
// рукопожатия TLS не было вовсе: обычная картина по HTTP с keep-alive.
func traced() stats.Summary {
	s := sample()
	s.Trace = &stats.TraceSummary{
		Traced:  1000,
		Reused:  998,
		DNS:     stats.PhaseStats{Count: 2, Latencies: stats.Latencies{P50: millis(1), P99: millis(3), Max: millis(3)}},
		Connect: stats.PhaseStats{Count: 2, Latencies: stats.Latencies{P50: millis(1), P99: millis(2), Max: millis(2)}},
		TTFB:    stats.PhaseStats{Count: 1000, Latencies: stats.Latencies{P50: millis(6), P99: millis(400), Max: millis(450)}},
	}
	return s
}

func TestTextNoEscapesWhenColorOff(t *testing.T) {
	if strings.ContainsRune(render(sample(), Options{Color: false, Width: 80}), '\x1b') {
		t.Error("в выводе есть ANSI-коды при Color=false — они уедут в пайп")
	}
}

func TestTextHasEscapesWhenColorOn(t *testing.T) {
	if !strings.ContainsRune(render(sample(), Options{Color: true, Width: 80}), '\x1b') {
		t.Error("Color=true, но раскраски нет")
	}
}

// Бар должен укладываться в ширину терминала: перенос строки разваливает картинку.
func TestHistogramFitsWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120, 200} {
		for _, line := range strings.Split(render(sample(), Options{Width: width}), "\n") {
			if !strings.Contains(line, "█") {
				continue
			}
			if n := utf8.RuneCountInString(line); n > width {
				t.Errorf("width=%d: строка длиной %d рун: %q", width, n, line)
			}
		}
	}
}

// Самый высокий столбик должен занимать всю доступную ширину, иначе картинка
// схлопывается в огрызок независимо от размера окна.
func TestHistogramScalesToWidth(t *testing.T) {
	longest := func(width int) int {
		best := 0
		for _, line := range strings.Split(render(sample(), Options{Width: width}), "\n") {
			best = max(best, strings.Count(line, "█"))
		}
		return best
	}

	narrow, wide := longest(60), longest(120)
	if wide <= narrow {
		t.Errorf("бар не растянулся: 60 колонок → %d, 120 колонок → %d", narrow, wide)
	}
}

func TestHistogramSurvivesTinyWidth(t *testing.T) {
	if render(sample(), Options{Width: 1}) == "" { // не должно паниковать
		t.Error("пустой вывод")
	}
}

// При длинном хвосте маленький бакет округляется в ноль столбиков и становится
// неотличим от пустого — а это разные вещи.
func TestHistogramShowsSmallBuckets(t *testing.T) {
	s := sample()
	s.Histogram = []stats.Bucket{
		{Upper: millis(1), Count: 10000},
		{Upper: millis(2), Count: 0},
		{Upper: millis(3), Count: 7},
	}

	var bars []int
	for _, line := range strings.Split(render(s, Options{Width: 80}), "\n") {
		if strings.Contains(line, "[") && strings.Contains(line, "ms") {
			bars = append(bars, strings.Count(line, "█"))
		}
	}

	if len(bars) != 3 {
		t.Fatalf("строк гистограммы %d, want 3", len(bars))
	}
	if bars[1] != 0 {
		t.Errorf("пустой бакет нарисован %d столбиками, want 0", bars[1])
	}
	if bars[2] == 0 {
		t.Error("бакет на 7 замеров нарисован пустым — не отличить от нуля")
	}
}

func TestCorrectedBlockOnlyInOpenLoop(t *testing.T) {
	const marker = "поправкой на расписание"

	if strings.Contains(render(sample(), Options{Width: 80}), marker) {
		t.Error("closed-loop: блок с поправкой не должен печататься — расписания не было")
	}
	if !strings.Contains(render(sample(), Options{Width: 80, OpenLoop: true}), marker) {
		t.Error("open-loop: блок с поправкой пропал")
	}
}

func TestTraceBlockOnlyWhenMeasured(t *testing.T) {
	const marker = "Фазы соединения"

	if strings.Contains(render(sample(), Options{Width: 80}), marker) {
		t.Error("блок фаз печатается без -trace")
	}
	if !strings.Contains(render(traced(), Options{Width: 80}), marker) {
		t.Error("блок фаз пропал при включённой трассировке")
	}
}

// Пустая фаза должна быть видна как прочерк: ноль замеров и «0ms» —
// разные утверждения, и путать их нельзя.
func TestTraceShowsEmptyPhaseAsDash(t *testing.T) {
	for _, line := range strings.Split(render(traced(), Options{Width: 80}), "\n") {
		if !strings.Contains(line, "TLS handshake") {
			continue
		}
		if !strings.Contains(line, "—") {
			t.Errorf("фаза без замеров показана как %q", strings.TrimSpace(line))
		}
		return
	}
	t.Error("строки TLS handshake нет вовсе")
}

// Колонка «замеров» — главное в этом блоке: без неё p99 по двум замерам
// выглядит так же солидно, как p99 по тысяче.
func TestTraceShowsSampleCounts(t *testing.T) {
	out := render(traced(), Options{Width: 100})

	for _, want := range []string{"замеров", "998 из 1000"} {
		if !strings.Contains(out, want) {
			t.Errorf("в блоке фаз нет %q", want)
		}
	}
}

// «0 из 200 взяли соединение из пула — фазы им не понадобились» — бессмыслица.
func TestTraceNoteWhenNothingReused(t *testing.T) {
	s := traced()
	s.Trace.Reused = 0

	out := render(s, Options{Width: 100})
	if strings.Contains(out, "0 из") {
		t.Error("напечатано «0 из N взяли соединение из пула»")
	}
	if !strings.Contains(out, "ни один") {
		t.Errorf("нет внятной формулировки для случая без переиспользования:\n%s", out)
	}
}

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
