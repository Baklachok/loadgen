// Блок происхождения: всё, что нужно, чтобы повторить прогон
// через полгода, когда память о запуске уже стёрлась.
package report

import (
	"fmt"
	"io"
	"runtime"
	"strconv"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

// writeProvenance печатается в конце, а не в начале: первый экран отчёта
// должен отвечать на вопрос, а происхождение цифр — справка, к которой
// возвращаются позже. Наверху остаётся короткая строка запуска.
func writeProvenance(w io.Writer, s stats.Summary, opt Options, p palette) {
	run := opt.Run
	cfg := run.Config

	fmt.Fprintf(w, "\n%s\n", p.bold("Прогон"))
	row := func(label, value string) {
		fmt.Fprintf(w, "  %s %s\n", p.dim(fmt.Sprintf("%-13s", label)), value)
	}

	row("loadgen", run.Version)
	row("цель", fmt.Sprintf("%s %s", cfg.Method, cfg.URL))
	row("план", runScope(cfg))
	row("режим", runMode(cfg))
	row("протокол", orUnknown(run.Proto))
	row("keep-alive", yesNo(!cfg.DisableKeepAlive))
	row("таймаут", cfg.Timeout.String())
	row("GOMAXPROCS", strconv.Itoa(runtime.GOMAXPROCS(0)))
	row("начало", run.StartedAt.Format(time.RFC3339))
	row("длительность", s.Elapsed.Round(time.Millisecond).String())
}

// orUnknown: пустой протокол значит, что ни один ответ не пришёл, — это
// другое утверждение, чем «HTTP/1.1», и выглядеть должно иначе.
func orUnknown(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func yesNo(v bool) string {
	if v {
		return "да"
	}
	return "нет"
}

// runScope — сколько было заказано. Без него блок не выполняет своей задачи:
// по «closed-loop, 20 потоков» не понять, был это -n 1000 или -z 30s, и
// повторить прогон нельзя.
func runScope(cfg runner.Config) string {
	if cfg.Duration > 0 {
		return cfg.Duration.String()
	}
	return fmt.Sprintf("%d запросов", cfg.Requests)
}
