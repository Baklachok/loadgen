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

func sample() stats.Summary {
	ms := func(n int) time.Duration { return time.Duration(n) * time.Millisecond }
	return stats.Summary{
		Total: 100, Success: 98, Failed: 2,
		Elapsed: 2 * time.Second, RPS: 49,
		Latency:   stats.Latencies{Min: ms(1), Mean: ms(5), Max: ms(90), P50: ms(4), P90: ms(9), P95: ms(30), P99: ms(80)},
		Corrected: stats.Latencies{Min: ms(1), Mean: ms(9), Max: ms(120), P50: ms(5), P90: ms(40), P95: ms(70), P99: ms(110)},
		MaxLag:    ms(30),
		Histogram: []stats.Bucket{
			{Upper: ms(10), Count: 80},
			{Upper: ms(20), Count: 3},
			{Upper: ms(120), Count: 15},
		},
		BytesRead: 4096, Throughput: 0.002,
		Codes:  map[int]int{200: 90, 404: 5, 503: 3},
		Errors: map[stats.ErrorKind]int{stats.ErrTimeout: 2},
	}
}

func TestTextNoEscapesWhenColorOff(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, sample(), Options{Color: false, Width: 80})

	if strings.ContainsRune(buf.String(), '\x1b') {
		t.Error("в выводе есть ANSI-коды при Color=false — они уедут в пайп")
	}
}

func TestTextHasEscapesWhenColorOn(t *testing.T) {
	var buf bytes.Buffer
	Text(&buf, sample(), Options{Color: true, Width: 80})

	if !strings.ContainsRune(buf.String(), '\x1b') {
		t.Error("Color=true, но раскраски нет")
	}
}

// Бар должен укладываться в ширину терминала: перенос строки разваливает картинку.
func TestHistogramFitsWidth(t *testing.T) {
	for _, width := range []int{40, 60, 80, 120, 200} {
		var buf bytes.Buffer
		Text(&buf, sample(), Options{Color: false, Width: width})

		for _, line := range strings.Split(buf.String(), "\n") {
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
		var buf bytes.Buffer
		Text(&buf, sample(), Options{Color: false, Width: width})
		best := 0
		for _, line := range strings.Split(buf.String(), "\n") {
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
	var buf bytes.Buffer
	Text(&buf, sample(), Options{Color: false, Width: 1}) // не должно паниковать
	if buf.Len() == 0 {
		t.Error("пустой вывод")
	}
}

func TestCorrectedBlockOnlyInOpenLoop(t *testing.T) {
	const marker = "поправкой на расписание"

	var closed, open bytes.Buffer
	Text(&closed, sample(), Options{Width: 80, OpenLoop: false})
	Text(&open, sample(), Options{Width: 80, OpenLoop: true})

	if strings.Contains(closed.String(), marker) {
		t.Error("closed-loop: блок с поправкой не должен печататься — расписания не было")
	}
	if !strings.Contains(open.String(), marker) {
		t.Error("open-loop: блок с поправкой пропал")
	}
}

func TestJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, sample(), Options{OpenLoop: true}); err != nil {
		t.Fatal(err)
	}

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
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("невалидный JSON: %v\n%s", err, buf.String())
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
	var buf bytes.Buffer
	if err := JSON(&buf, sample(), Options{OpenLoop: false}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "corrected") || strings.Contains(buf.String(), "max_lag_ms") {
		t.Error("в closed-loop полей поправки быть не должно: иначе потребитель решит, что расписание было")
	}
}

func TestJSONOnEmptyRun(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, stats.Summary{Codes: map[int]int{}, Errors: map[stats.ErrorKind]int{}}, Options{}); err != nil {
		t.Fatal(err)
	}
	var any map[string]any
	if err := json.Unmarshal(buf.Bytes(), &any); err != nil {
		t.Fatalf("невалидный JSON на пустом прогоне: %v", err)
	}
	if any["histogram"] == nil {
		t.Error("histogram должен быть [], а не null — потребителю проще итерироваться")
	}
}

// При длинном хвосте маленький бакет округляется в ноль столбиков и становится
// неотличим от пустого — а это разные вещи.
func TestHistogramShowsSmallBuckets(t *testing.T) {
	s := sample()
	s.Histogram = []stats.Bucket{
		{Upper: time.Millisecond, Count: 10000},
		{Upper: 2 * time.Millisecond, Count: 0},
		{Upper: 3 * time.Millisecond, Count: 7},
	}

	var buf bytes.Buffer
	Text(&buf, s, Options{Color: false, Width: 80})

	var bars []int
	for _, line := range strings.Split(buf.String(), "\n") {
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
