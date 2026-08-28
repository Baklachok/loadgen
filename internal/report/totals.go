// Шапка со счётчиками: сколько запросов ушло и чем кончились.
package report

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

// Ширина колонки подписей. Раньше отступы были вбиты пробелами в сами
// подписи, и правка любой из них требовала пересчитать остальные руками.
const totalsLabel = 14

func writeTotals(w io.Writer, s stats.Summary, _ Options, p palette) {
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

	// Только когда обрывы были: вечное «Оборвано: 0» приучает не читать шапку,
	// как приучал бы «Прогрев: 0» выше. Скобка объясняет, почему сумма
	// по кодам ниже больше числа ответов.
	if s.Truncated > 0 {
		row("Оборвано:", p.red(fmt.Sprintf("%d (код получен, тело — нет)", s.Truncated)))
	}

	row("Время:", s.Elapsed.Round(time.Millisecond).String())

	// Условие — «был ли прогрев», а не «отличаются ли длительности»: окно
	// всегда начинается на микросекунды позже прогона, так что сравнение
	// на равенство печатало бы эту строку всегда.
	if s.Warmup > 0 && s.Window > 0 {
		row("Измерялось:", s.Window.Round(time.Millisecond).String())
	}
	row("RPS (ответов):", rateValue(s, p))

	// Опоздания печатаем только когда они были: в closed-loop расписания нет,
	// а в уложившемся open-loop эта строка была бы вечным нулём.
	if s.Late > 0 {
		row("Опоздали:", p.yellow(fmt.Sprintf("%d (%.0f%%), макс. на %v",
			s.Late, s.LateShare()*100, s.MaxLag.Round(time.Millisecond))))
	}
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
