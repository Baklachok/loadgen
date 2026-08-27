// Package report отвечает только за представление: текст для человека,
// JSON для CI. Ничего не считает — все числа приходят готовыми из stats.
package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

type Options struct {
	Color    bool // раскрашивать вывод ANSI-кодами
	Width    int  // ширина терминала: под неё масштабируется гистограмма
	OpenLoop bool // печатать блок с поправкой на расписание
}

// Header печатается до прогона, чтобы было видно, что вообще запустили.
func Header(w io.Writer, cfg runner.Config, opt Options) {
	p := palette(opt.Color)

	mode := fmt.Sprintf("closed-loop, %d потоков", cfg.Concurrency)
	if cfg.Rate > 0 {
		mode = fmt.Sprintf("open-loop %.0f RPS, до %d запросов в полёте", cfg.Rate, cfg.Concurrency)
	}

	target := p.bold(cfg.URL)
	if cfg.Duration > 0 {
		fmt.Fprintf(w, "Прогон %v на %s %s\n\n", cfg.Duration, target, p.dim("("+mode+")"))
		return
	}
	fmt.Fprintf(w, "Запуск %d запросов к %s %s\n\n", cfg.Requests, target, p.dim("("+mode+")"))
}

func Text(w io.Writer, s stats.Summary, opt Options) {
	p := palette(opt.Color)

	writeTotals(w, s, p)

	// Порог — полученные ответы, а не 2xx: у прогона из одних 503 латентность
	// осмысленна и показывает, как быстро сервис отказывает.
	if s.Responses() > 0 {
		writeLatency(w, s, opt, p)

		if s.Trace != nil {
			writeTrace(w, s.Trace, p)
		}
		if len(s.Histogram) > 0 {
			fmt.Fprintf(w, "\n%s\n", p.bold("Распределение"))
			writeHistogram(w, s.Histogram, p, opt.Width)
		}
	}

	writeCodes(w, s, p)
	writeErrors(w, s, p)
}

// Ширина колонки подписей. Раньше отступы были вбиты пробелами в сами
// подписи, и правка любой из них требовала пересчитать остальные руками.
const totalsLabel = 14

func writeTotals(w io.Writer, s stats.Summary, p palette) {
	row := func(label, value string) {
		fmt.Fprintf(w, "%s %s\n", p.dim(fmt.Sprintf("%-*s", totalsLabel, label)), value)
	}

	row("Всего:", strconv.Itoa(s.Total))

	// Строка прогрева появляется только когда он был: постоянный «Прогрев: 0»
	// приучает не читать шапку.
	if s.Warmup > 0 {
		row("Прогрев:", p.dim(fmt.Sprintf("%d отброшено", s.Warmup)))
	}

	// Доля 2xx стоит вплотную к числу намеренно: «Успешно: 12500» на прогоне,
	// где сервис отдавал одни 429, читается как хорошая новость.
	row("Успешно (2xx):", p.green(fmt.Sprintf("%d (%.1f%%)", s.OK, s.SuccessRate()*100)))
	row("Не-2xx:", highlightNonZero(s.NonOK, p.yellow))
	row("Без ответа:", highlightNonZero(s.Failed, p.red))
	row("Время:", s.Elapsed.Round(time.Millisecond).String())

	// Условие — «был ли прогрев», а не «отличаются ли длительности»: окно
	// всегда начинается на микросекунды позже прогона, так что сравнение
	// на равенство печатало бы эту строку всегда.
	if s.Warmup > 0 && s.Window > 0 {
		row("Измерялось:", s.Window.Round(time.Millisecond).String())
	}
	row("RPS (ответов):", p.bold(fmt.Sprintf("%.1f", s.RPS)))
	row("Throughput:", fmt.Sprintf("%.2f МБ/с", s.Throughput))
}

// highlightNonZero красит счётчик, только если он ненулевой: подсвеченный
// ноль приучает не обращать внимания на подсветку.
func highlightNonZero(n int, color func(string) string) string {
	if n == 0 {
		return strconv.Itoa(n)
	}
	return color(strconv.Itoa(n))
}

func writeLatency(w io.Writer, s stats.Summary, opt Options, p palette) {
	fmt.Fprintf(w, "\n%s\n", p.bold("Latency (время запроса)"))
	writeLatencies(w, s.Latency, p)

	if !opt.OpenLoop {
		return
	}

	// разница между блоками — и есть coordinated omission
	fmt.Fprintf(w, "\n%s\n", p.bold("Latency с поправкой на расписание"))
	writeLatencies(w, s.Corrected, p)
	fmt.Fprintf(w, "\n%s %v\n", p.dim("Макс. отставание старта:"), s.MaxLag.Round(time.Microsecond))
}

func writeLatencies(w io.Writer, l stats.Latencies, p palette) {
	row := func(name string, d time.Duration) {
		fmt.Fprintf(w, "  %s %v\n", p.dim(fmt.Sprintf("%-4s", name)), d.Round(time.Microsecond))
	}
	row("min", l.Min)
	row("mean", l.Mean)
	row("p50", l.P50)
	row("p90", l.P90)
	row("p95", l.P95)
	row("p99", l.P99)
	row("max", l.Max)
}

// writeHistogram рисует столбики, растягивая самый высокий на всю оставшуюся
// ширину строки. Подписи считаются заранее, иначе бар не влезет и строка
// переносится, разваливая картинку.
func writeHistogram(w io.Writer, buckets []stats.Bucket, p palette, width int) {
	labels := make([]string, len(buckets))
	counts := make([]string, len(buckets))
	labelW, countW, maxCount := 0, 0, 0

	for i, b := range buckets {
		labels[i] = b.Upper.Round(time.Microsecond).String()
		counts[i] = strconv.Itoa(b.Count)
		labelW = max(labelW, len(labels[i]))
		countW = max(countW, len(counts[i]))
		maxCount = max(maxCount, b.Count)
	}
	if maxCount == 0 {
		return
	}

	// «  <label> [<count>] » — всё, что занято не баром
	barW := width - labelW - countW - len("  ") - len(" [") - len("] ")
	if barW < 1 {
		barW = 1
	}

	for i, b := range buckets {
		// непустой бакет обязан быть виден: при линейной шкале и длинном хвосте
		// сотня замеров рядом с десятью тысячами округляется в ноль столбиков,
		// и «мало» становится неотличимо от «пусто»
		n := b.Count * barW / maxCount
		if n == 0 && b.Count > 0 {
			n = 1
		}
		bar := strings.Repeat("█", n)
		fmt.Fprintf(w, "  %*s [%*s] %s\n", labelW, labels[i], countW, counts[i], p.cyan(bar))
	}
}

func writeCodes(w io.Writer, s stats.Summary, p palette) {
	if len(s.Codes) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", p.bold("Коды ответов"))
	for _, code := range sortedKeys(s.Codes) {
		fmt.Fprintf(w, "  %s %d\n", colorCode(p, code), s.Codes[code])
	}
}

func colorCode(p palette, code int) string {
	s := strconv.Itoa(code)
	switch {
	case code >= 500:
		return p.red(s)
	case code >= 400:
		return p.yellow(s)
	case code >= 200 && code < 300:
		return p.green(s)
	}
	return s
}

func writeErrors(w io.Writer, s stats.Summary, p palette) {
	if len(s.Errors) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", p.bold("Ошибки"))
	for _, kind := range sortedKeys(s.Errors) {
		fmt.Fprintf(w, "  %s %d\n", p.red(string(kind)), s.Errors[kind])
	}
}

// sortedKeys нужен, чтобы два одинаковых прогона печатались одинаково:
// обход map в Go намеренно рандомизирован.
func sortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Одна строка формата на все три места — заголовок, пустую фазу и заполненную.
// Разъехавшись, они ломают выравнивание молча, а заметно это только глазами.
const tracePhaseRow = "  %-14s %8s %10s %10s %10s"

// writeTrace печатает разбивку по фазам соединения. Колонка «замеров» тут
// не украшение: при keep-alive резолв и рукопожатие делает только первый
// запрос на соединение, и без неё p99 по трём замерам выглядел бы так же
// солидно, как p99 по десяти тысячам.
func writeTrace(w io.Writer, t *stats.TraceSummary, p palette) {
	fmt.Fprintf(w, "\n%s\n", p.bold("Фазы соединения"))
	fmt.Fprintf(w, "%s\n", p.dim(fmt.Sprintf(tracePhaseRow, "", "замеров", "p50", "p99", "max")))

	dur := func(d time.Duration) string { return d.Round(time.Microsecond).String() }

	row := func(name string, ph stats.PhaseStats) {
		// Фаза без единого замера получает прочерк, а не нули: ноль читался бы
		// как «прошла мгновенно», хотя её попросту не было.
		if ph.Count == 0 {
			fmt.Fprintf(w, tracePhaseRow+"\n", name, "0", "—", "—", "—")
			return
		}
		fmt.Fprintf(w, tracePhaseRow+"\n", name, strconv.Itoa(ph.Count),
			dur(ph.P50), dur(ph.P99), dur(ph.Max))
	}

	row("DNS", t.DNS)
	row("TCP connect", t.Connect)
	row("TLS handshake", t.TLS)
	row("TTFB", t.TTFB)

	fmt.Fprintf(w, "\n  %s\n", p.dim(reuseNote(t)))
}

// reuseNote меняет фразу целиком, а не подставляет ноль: «0 из 200 запросов
// взяли соединение из пула — фазы им не понадобились» читается как бессмыслица.
func reuseNote(t *stats.TraceSummary) string {
	if t.Reused == 0 {
		return fmt.Sprintf("ни один из %d запросов не переиспользовал соединение — каждый устанавливал своё", t.Traced)
	}
	return fmt.Sprintf("%d из %d запросов взяли соединение из пула — фазы DNS, TCP и TLS им не понадобились",
		t.Reused, t.Traced)
}
