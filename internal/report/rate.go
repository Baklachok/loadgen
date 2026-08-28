// Частота: строка «RPS (ответов)» в шапке и предупреждение о недоборе.
// Вместе, потому что делят порог и меняются парой — цифра без предупреждения
// вводит в заблуждение, предупреждение без цифры не проверить.
package report

import (
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/stats"
)

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
