// Режим сравнения: прогона нет, есть два готовых отчёта.
package main

import (
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/compare"
)

// runCompare отвечает на вопрос «стало лучше или хуже» и ничего не запускает.
// Отдельная ветка в run: URL здесь не нужен, подтверждение не спрашивается,
// накопитель не заводится.
func runCompare(f *flags, stdout, stderr io.Writer) int {
	if f.fs.NArg() != 2 {
		fmt.Fprintln(stderr, "ошибка: -compare ждёт два пути — до и после; каждый файл или каталог с *.json")
		return exitUsage
	}
	// Молча игнорировать заданный формат нельзя, а машинного потребителя
	// у сравнения пока нет.
	if *f.file != "" {
		fmt.Fprintln(stderr, "ошибка: -f описывает прогон, а -compare прогона не делает")
		return exitUsage
	}
	if *f.metrics != "" {
		fmt.Fprintln(stderr, "ошибка: -metrics открывает окно в идущий прогон, а -compare прогона не делает")
		return exitUsage
	}
	if *f.output != "text" {
		fmt.Fprintf(stderr, "ошибка: -compare печатает текстом, -o %s к нему не применяется\n", *f.output)
		return exitUsage
	}

	// Порог стоит, только если назван: для compare nil — «не задан», а явный
	// ноль — ни на процент. Отрицательный — ошибка, и до чтения файлов:
	// отказ по флагу от файлов не зависит.
	set := f.named()
	var thr compare.Thresholds
	for _, g := range []struct {
		name string
		v    finiteFlag
		dst  **float64
	}{{"regress-p99", f.regressP99, &thr.P99}, {"regress-rps", f.regressRPS, &thr.RPS}} {
		if !set[g.name] {
			continue
		}
		if g.v < 0 {
			fmt.Fprintf(stderr, "ошибка: -%s — насколько позволено ухудшиться, отрицательным не бывает; получено %v\n", g.name, float64(g.v))
			return exitUsage
		}
		limit := float64(g.v)
		*g.dst = &limit
	}

	before, err := compare.Load(f.fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}
	after, err := compare.Load(f.fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	res, err := compare.Compare(before, after, thr)
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	res.Write(stdout)

	// Тот же код, что у -slo-*: порог приёмки остаётся порогом приёмки,
	// абсолютный он или относительный.
	if res.Regressed() {
		return exitSLO
	}
	return exitOK
}
