// Всё, что зависит от терминала на том конце: можно ли красить, какой
// он ширины и как выглядит сама раскраска. Отчёт об этом не знает —
// он получает готовую palette и число колонок.
package report

import (
	"os"

	"golang.org/x/term"
)

// ColorEnabled решает, можно ли красить. Пайп, редирект в файл и CI — нельзя:
// ANSI-коды уедут в данные. NO_COLOR — общепринятый способ выключить цвет руками.
func ColorEnabled(f *os.File) bool {
	if _, set := os.LookupEnv("NO_COLOR"); set {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// TerminalWidth возвращает ширину терминала, а для не-терминала — fallback.
func TerminalWidth(f *os.File, fallback int) int {
	w, _, err := term.GetSize(int(f.Fd()))
	if err != nil || w <= 0 {
		return fallback
	}
	return w
}

type palette bool

func (p palette) wrap(code, s string) string {
	if !p {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p palette) dim(s string) string    { return p.wrap("2", s) }
func (p palette) bold(s string) string   { return p.wrap("1", s) }
func (p palette) red(s string) string    { return p.wrap("31", s) }
func (p palette) green(s string) string  { return p.wrap("32", s) }
func (p palette) yellow(s string) string { return p.wrap("33", s) }
func (p palette) cyan(s string) string   { return p.wrap("36", s) }
