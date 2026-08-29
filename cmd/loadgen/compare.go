// Режим сравнения: прогона нет, есть два готовых отчёта.
package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/compare"
)

// runCompare отвечает на вопрос «стало лучше или хуже» и ничего не запускает.
// Отдельная ветка в run: URL здесь не нужен, подтверждение не спрашивается,
// накопитель не заводится.
func runCompare(f *flags, fs *flag.FlagSet, stdout, stderr io.Writer) int {
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "ошибка: -compare ждёт два пути — до и после; каждый файл или каталог с *.json")
		return exitUsage
	}
	// Молча игнорировать заданный формат нельзя, а машинного потребителя
	// у сравнения пока нет.
	if *f.output != "text" {
		fmt.Fprintf(stderr, "ошибка: -compare печатает текстом, -o %s к нему не применяется\n", *f.output)
		return exitUsage
	}

	before, err := compare.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}
	after, err := compare.Load(fs.Arg(1))
	if err != nil {
		fmt.Fprintln(stderr, "ошибка:", err)
		return exitUsage
	}

	res, err := compare.Compare(before, after, compare.Thresholds{P99: *f.regressP99, RPS: *f.regressRPS})
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
