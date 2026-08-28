package report

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Baklachok/loadgen/internal/stats"
)

// Цвет включается только там, где его увидит человек.
func TestColorOutput(t *testing.T) {
	t.Run("выключен — ни одного ANSI-кода", func(t *testing.T) {
		if strings.ContainsRune(render(sample(), Options{Color: false, Width: 80}), '\x1b') {
			t.Error("в выводе есть ANSI-коды при Color=false — они уедут в пайп")
		}
	})

	t.Run("включён — раскраска есть", func(t *testing.T) {
		if !strings.ContainsRune(render(sample(), Options{Color: true, Width: 80}), '\x1b') {
			t.Error("Color=true, но раскраски нет")
		}
	})
}

// Гистограмма обязана помещаться в окно и не терять непустые бакеты.
func TestHistogramBlock(t *testing.T) {
	// Бар должен укладываться в ширину терминала: перенос строки разваливает картинку.
	t.Run("укладывается в ширину терминала", func(t *testing.T) {
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
	})

	// Самый высокий столбик должен занимать всю доступную ширину, иначе картинка
	// схлопывается в огрызок независимо от размера окна.
	t.Run("растягивается вместе с окном", func(t *testing.T) {
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
	})

	t.Run("не падает на ширине 1", func(t *testing.T) {
		if render(sample(), Options{Width: 1}) == "" { // не должно паниковать
			t.Error("пустой вывод")
		}
	})

	// При длинном хвосте маленький бакет округляется в ноль столбиков и становится
	// неотличим от пустого — а это разные вещи.
	t.Run("маленький бакет остаётся видимым", func(t *testing.T) {
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
	})
}

// Блок фаз: печатается только когда мерили, и не выдаёт
// отсутствие фазы за мгновенную.
func TestTraceBlock(t *testing.T) {
	t.Run("только при включённой трассировке", func(t *testing.T) {
		const marker = "Фазы соединения"

		if strings.Contains(render(sample(), Options{Width: 80}), marker) {
			t.Error("блок фаз печатается без -trace")
		}
		if !strings.Contains(render(traced(), Options{Width: 80}), marker) {
			t.Error("блок фаз пропал при включённой трассировке")
		}
	})

	// Пустая фаза должна быть видна как прочерк: ноль замеров и «0ms» —
	// разные утверждения, и путать их нельзя.
	t.Run("пустая фаза — прочерк, а не ноль", func(t *testing.T) {
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
	})

	// Колонка «замеров» — главное в этом блоке: без неё p99 по двум замерам
	// выглядит так же солидно, как p99 по тысяче.
	t.Run("число замеров рядом с перцентилями", func(t *testing.T) {
		out := render(traced(), Options{Width: 100})

		for _, want := range []string{"замеров", "998 из 1000"} {
			if !strings.Contains(out, want) {
				t.Errorf("в блоке фаз нет %q", want)
			}
		}
	})

	// «0 из 200 взяли соединение из пула — фазы им не понадобились» — бессмыслица.
	t.Run("внятная фраза без переиспользования", func(t *testing.T) {
		s := traced()
		s.Trace.Reused = 0

		out := render(s, Options{Width: 100})
		if strings.Contains(out, "0 из") {
			t.Error("напечатано «0 из N взяли соединение из пула»")
		}
		if !strings.Contains(out, "ни один") {
			t.Errorf("нет внятной формулировки для случая без переиспользования:\n%s", out)
		}
	})
}

// Шапка — то немногое, что читают всегда. Каждая проверка здесь про
// случай, когда она могла бы соврать или зашуметь.
func TestTotals(t *testing.T) {
	t.Run("поток не-2xx не читается как успех", func(t *testing.T) {
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
	})

	t.Run("прогрев показан, только если он был", func(t *testing.T) {
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
	})

	// Строка «Измерялось» появляется, только когда окно отличается от прогона:
	// без прогрева это был бы дубль строки выше.
	t.Run("окно измерения — только при прогреве", func(t *testing.T) {
		const marker = "Измерялось:"

		// Без прогрева окно всё равно на волосок короче прогона — строки быть
		// не должно именно поэтому, а не из-за точного равенства.
		same := sample()
		same.Elapsed, same.Window = 2*time.Second, 2*time.Second-317*time.Microsecond
		if strings.Contains(render(same, Options{Width: 100}), marker) {
			t.Error("окно напечатано, хотя прогрева не было")
		}

		shortened := sample()
		shortened.Warmup = 50
		shortened.Elapsed, shortened.Window = 6*time.Second, time.Second
		out := render(shortened, Options{Width: 100})

		if !strings.Contains(out, marker) {
			t.Errorf("окно не показано, хотя короче прогона:\n%s", out)
		}
		if !strings.Contains(out, "1s") || !strings.Contains(out, "6s") {
			t.Errorf("в шапке нет обоих значений:\n%s", out)
		}
	})

	t.Run("достигнутая частота рядом с заданной", func(t *testing.T) {
		closed := sample()
		closed.RPS = 1500
		if out := render(closed, Options{Width: 100}); strings.Contains(out, "заданных") {
			t.Error("в closed-loop не с чем сравнивать, а цель напечатана")
		}

		open := sample()
		open.TargetRate, open.RPS = 1000, 995 // недобор полпроцента — шум
		out := render(open, Options{Width: 100})

		if !strings.Contains(out, "995.0 из 1000 заданных") {
			t.Errorf("достигнутое не поставлено рядом с заданным:\n%s", out)
		}
		if strings.Contains(out, "не удержана") {
			t.Error("предупреждение на расхождении в полпроцента")
		}
	})

	// Пока не выяснено, кто не удержал частоту, остальные цифры интерпретировать
	// нельзя — поэтому предупреждение обязано быть в отчёте, а не в stderr.
	t.Run("предупреждение при реальном недоборе", func(t *testing.T) {
		s := sample()
		s.TargetRate, s.RPS = 1000, 480

		out := render(s, Options{Width: 100})

		if !strings.Contains(out, "(−52%)") {
			t.Errorf("недобор не показан рядом с числом:\n%s", out)
		}
		if !strings.Contains(out, "не удержана") {
			t.Errorf("нет предупреждения при недоборе вдвое:\n%s", out)
		}
		// Предупреждение должно стоять до цифр, которые оно ставит под сомнение
		if warn, lat := strings.Index(out, "не удержана"), strings.Index(out, "Latency"); warn > lat {
			t.Error("предупреждение напечатано после блока latency")
		}
	})
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

// Массовые опоздания старта означают, что запросы не успевали уходить,
// — это потолок генератора, а не медленный сервис. Пока данных на такой
// вывод нет, предупреждение обязано честно называть обе версии.
func TestRateWarningNamesTheCauseWhenKnown(t *testing.T) {
	t.Run("опоздания массовые — виноват генератор", func(t *testing.T) {
		s := sample()
		s.TargetRate, s.RPS = 2000, 500
		s.Total, s.Late, s.MaxLag = 1000, 800, 250*time.Millisecond

		out := render(s, Options{Width: 100})

		if !strings.Contains(out, "не успевали уходить") {
			t.Errorf("причина не названа, хотя 80%% опоздали:\n%s", out)
		}
		if !strings.Contains(out, "Опоздали:") || !strings.Contains(out, "800 (80%)") {
			t.Errorf("счётчик опозданий не показан:\n%s", out)
		}
	})

	t.Run("опозданий нет — обе версии остаются", func(t *testing.T) {
		s := sample()
		s.TargetRate, s.RPS = 2000, 500
		s.Total, s.Late = 1000, 0

		out := render(s, Options{Width: 100})

		if strings.Contains(out, "не успевали уходить") {
			t.Error("генератор назначен виноватым без единого опоздания")
		}
		if !strings.Contains(out, "Либо сервис") {
			t.Errorf("нет честного перечисления причин:\n%s", out)
		}
		if strings.Contains(out, "Опоздали:") {
			t.Error("напечатана строка опозданий при нулевом счётчике")
		}
	})
}

// Число вместо прочерка читатель принял бы за результат и сделал бы по нему
// вывод — поэтому недостоверные перцентили не печатаются вовсе.
func TestSmallSampleHidesPercentiles(t *testing.T) {
	s := sample()
	s.Latency.Samples = 50

	out := render(s, Options{Width: 100})

	if !strings.Contains(out, "50 замеров") {
		t.Errorf("в заголовке нет числа замеров:\n%s", out)
	}
	if strings.Contains(out, "80ms") {
		t.Error("p99 напечатан числом на выборке в 50 замеров")
	}
	if !strings.Contains(out, "p99  —") {
		t.Errorf("p99 не заменён прочерком:\n%s", out)
	}
	// p50 на пятидесяти замерах ещё осмыслен
	if !strings.Contains(out, "p50  4ms") {
		t.Errorf("p50 скрыт, хотя порог для него всего 20:\n%s", out)
	}
	if !strings.Contains(out, "прочерк — мало данных") {
		t.Errorf("прочерки не объяснены, выглядят поломкой:\n%s", out)
	}
}

func TestFullSampleShowsEveryPercentile(t *testing.T) {
	out := render(sample(), Options{Width: 100})

	if strings.Contains(out, "—") && strings.Contains(out, "мало данных") {
		t.Errorf("прочерки на достаточной выборке:\n%s", out)
	}
	if !strings.Contains(out, "p99  80ms") {
		t.Errorf("p99 не напечатан на 2000 замерах:\n%s", out)
	}
}

// Прочерк в строке и упоминание в пояснении обязаны совпадать. Раньше список
// перцентилей был выписан в двух функциях и мог разойтись молча.
func TestDashedPercentilesAreAllExplained(t *testing.T) {
	s := sample()
	s.Latency.Samples = 150 // хватает на p50 и p90, не хватает на p95 и p99

	out := render(s, Options{Width: 100})

	for _, q := range stats.Quantiles {
		dashed := strings.Contains(out, q.Name+"  —")
		explained := strings.Contains(out, q.Name+" от ")

		if dashed != explained {
			t.Errorf("%s: прочерк=%v, пояснение=%v — строка и сноска разошлись\n%s",
				q.Name, dashed, explained, out)
		}
	}
}
