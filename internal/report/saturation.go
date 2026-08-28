// Предупреждение о том, что прогон упёрся в генератор, а не в сервис.
package report

import (
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/stats"
)

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
