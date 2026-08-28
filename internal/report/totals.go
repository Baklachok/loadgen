// Шапка со счётчиками и предупреждение о недоборе частоты.
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

// Расхождение больше пары процентов — это уже не шум планировщика.
const rateShortfallLimit = 0.02

// Доля опоздавших, начиная с которой причина недобора перестаёт быть загадкой.
const lateShareHint = 0.10

// rateValue ставит достигнутую частоту рядом с заданной. Порознь, как было
// раньше — цель в шапке, факт в сводке, — их никто не сопоставляет.
func rateValue(s stats.Summary, p palette) string {
	got := p.bold(fmt.Sprintf("%.1f", s.RPS))
	if s.TargetRate <= 0 {
		return got
	}

	value := fmt.Sprintf("%s из %.0f заданных", got, s.TargetRate)

	// Недобор в пределах шума планировщика не подсвечиваем: подсвеченная
	// норма приучает не смотреть на подсветку.
	if shortfall := s.RateShortfall(); shortfall >= rateShortfallLimit {
		value += " " + p.red(fmt.Sprintf("(−%.0f%%)", shortfall*100))
	}
	return value
}

// writeRateWarning — не косметика: пока не выяснено, кто не удержал частоту,
// сервис или сам генератор, остальные цифры прогона интерпретировать нельзя.
// Поэтому предупреждение идёт в отчёт, а не в stderr, — рядом с числами,
// достоверность которых оно ставит под вопрос.
func writeRateWarning(w io.Writer, s stats.Summary, _ Options, p palette) {
	if s.RateShortfall() < rateShortfallLimit {
		return
	}

	fmt.Fprintf(w, "\n%s\n", p.red("Заданная частота не удержана."))
	for _, line := range rateWarningCause(s) {
		fmt.Fprintf(w, "  %s\n", p.dim(line))
	}
	fmt.Fprintf(w, "  %s\n", p.dim("До выяснения остальные цифры прогона интерпретировать нельзя."))
}

// rateWarningCause сужает подозрение там, где данных достаточно. Массовые
// опоздания старта означают, что запросы не успевали уходить, — упёрлись в
// собственный потолок параллельности, а не в сервис.
func rateWarningCause(s stats.Summary) []string {
	if s.LateShare() >= lateShareHint {
		return []string{
			fmt.Sprintf("Запросы не успевали уходить: %.0f%% стартовали с опозданием.", s.LateShare()*100),
			"Похоже на потолок параллельности генератора — поднимите -c.",
		}
	}
	return []string{
		"Либо сервис не принимает такой поток, либо не тянет сам генератор:",
		"проверьте загрузку CPU клиента и ошибки сокетов, поднимите -c.",
	}
}

// highlightNonZero красит счётчик, только если он ненулевой: подсвеченный
// ноль приучает не обращать внимания на подсветку.
func highlightNonZero(n int, color func(string) string) string {
	if n == 0 {
		return strconv.Itoa(n)
	}
	return color(strconv.Itoa(n))
}

// writeClientWarning — самый громкий из возможных выводов прогона: цифры
// описывают не сервис, а нас. Печатается рядом с предупреждением о частоте
// и по той же причине — оно ставит под сомнение всё, что ниже.
func writeClientWarning(w io.Writer, s stats.Summary, _ Options, p palette) {
	if s.ClientErrors == 0 {
		return
	}

	fmt.Fprintf(w, "\n%s\n", p.red("Упёрлись в клиента, а не в сервис."))
	for _, line := range []string{
		fmt.Sprintf("%d запросов не ушли: у генератора кончились дескрипторы или порты.", s.ClientErrors),
		"Поднимите ulimit -n, уменьшите -c или разнесите нагрузку по машинам.",
		"Прогон измерил свой потолок, а не поведение сервиса.",
	} {
		fmt.Fprintf(w, "  %s\n", p.dim(line))
	}
}
