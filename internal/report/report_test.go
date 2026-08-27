package report

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Baklachok/loadgen/internal/stats"
)

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

func TestTotalsDoNotReadAsSuccessOnAllNon2xx(t *testing.T) {
	s := stats.Summary{
		Total: 12500, OK: 0, NonOK: 12500,
		Elapsed: 5 * time.Second, RPS: 2500,
		Codes: map[int]int{429: 12500},
	}

	out := render(s, Options{Width: 100})

	if !strings.Contains(out, "0 (0.0%)") {
		t.Errorf("доли 2xx нет в шапке:\n%s", out)
	}
	if !strings.Contains(out, "12500") {
		t.Error("число не-2xx не показано")
	}
	// «Успешно: 12500» в любом виде — это та самая ложь
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "Успешно") && strings.Contains(line, "12500") {
			t.Errorf("строка читается как успех: %q", strings.TrimSpace(line))
		}
	}
}

func TestWarmupLineOnlyWhenUsed(t *testing.T) {
	const marker = "Прогрев:"

	if strings.Contains(render(sample(), Options{Width: 100}), marker) {
		t.Error("строка прогрева печатается, хотя прогрева не было")
	}

	s := sample()
	s.Warmup = 100

	out := render(s, Options{Width: 100})
	if !strings.Contains(out, marker) || !strings.Contains(out, "100 отброшено") {
		t.Errorf("прогрев не показан в шапке:\n%s", out)
	}
}
