// Разбивки по кодам ответов, ошибкам и фазам соединения.
package report

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

func writeCodes(w io.Writer, s stats.Summary, _ Options, p palette) {
	if len(s.Codes) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", p.bold("Коды ответов"))
	for _, code := range stats.SortedKeys(s.Codes) {
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

func writeErrors(w io.Writer, s stats.Summary, _ Options, p palette) {
	if len(s.Errors) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", p.bold("Ошибки"))
	for _, kind := range stats.SortedKeys(s.Errors) {
		fmt.Fprintf(w, "  %s %d\n", p.red(string(kind)), s.Errors[kind])
	}
}

// Одна строка формата на все три места — заголовок, пустую фазу и заполненную.
// Разъехавшись, они ломают выравнивание молча, а заметно это только глазами.
const tracePhaseRow = "  %-14s %8s %10s %10s %10s"

// writeTrace печатает разбивку по фазам соединения. Колонка «замеров» тут
// не украшение: при keep-alive резолв и рукопожатие делает только первый
// запрос на соединение, и без неё p99 по трём замерам выглядел бы так же
// солидно, как p99 по десяти тысячам.
func writeTrace(w io.Writer, s stats.Summary, _ Options, p palette) {
	t := s.Trace
	if t == nil {
		return
	}

	fmt.Fprintf(w, "\n%s\n", p.bold("Фазы соединения"))
	fmt.Fprintf(w, "%s\n", p.dim(fmt.Sprintf(tracePhaseRow, "", "замеров", "p50", "p99", "max")))

	dur := func(d time.Duration) string { return d.Round(time.Microsecond).String() }

	row := func(name string, ph stats.PhaseStats) {
		// Фаза без единого замера получает прочерк, а не нули: ноль читался бы
		// как «прошла мгновенно», хотя её попросту не было.
		if ph.Samples == 0 {
			fmt.Fprintf(w, tracePhaseRow+"\n", name, "0", "—", "—", "—")
			return
		}
		fmt.Fprintf(w, tracePhaseRow+"\n", name, strconv.Itoa(ph.Samples),
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
