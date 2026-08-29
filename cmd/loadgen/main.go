// Команда loadgen. Десять файлов — та структура, которую дали бы папки,
// без их цены: в Go папка это пакет, а всё здесь замкнуто на load.go,
// flags и коды выхода, и папки экспортировали бы это ради одного вызова.
//
// Вход:
//
//	flags.go     флаги, их типы, сборка Config; правило «задан ли флаг»
//	file.go      -f: тот же набор флагов, но из YAML
//
// Диспетчер:
//
//	main.go      коды выхода и run
//
// Режимы:
//
//	load.go      прогон и его итог
//	compare.go   сравнение двух отчётов
//
// Живёт ровно столько, сколько прогон:
//
//	confirm.go   подтверждение для чужой цели — до старта
//	metrics.go   /metrics — от шапки до последнего запроса
//	progress.go  живая строка в stderr, только на терминале
//	signal.go    Ctrl+C: остановка и немедленный выход
//
// Обвязка:
//
//	version.go   версия сборки и User-Agent
//
// Карту сторожит TestPackageMap: новый файл без строки здесь — красный тест.
package main

import (
	"flag"
	"fmt"
	"os"
)

// Контракт кодов выхода. Без него loadgen нельзя поставить в пайплайн:
// он всегда «успешен», даже когда сто процентов запросов упали.
//
// Отступление от stdlib осознанное. flag.ExitOnError выходит с кодом 2 на
// любой ошибке разбора, но здесь 2 занят под «измерить не удалось» — это
// разные вещи, и CI должен краснеть по-разному. Поэтому флаги разбираются
// с ContinueOnError, а подсказка печатается вручную.
const (
	exitOK        = 0   // прогон состоялся; SLO, если заданы, выдержаны
	exitUsage     = 1   // флаги, URL, несовместимые опции
	exitNoRun     = 2   // измерить не удалось: ни одного ответа
	exitSLO       = 3   // SLO нарушен; появится вместе с --slo-*
	exitInterrupt = 130 // прервано пользователем, результат частичный
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run разбирает флаги и выбирает режим — и больше ничего: сами режимы
// живут в load.go и compare.go. Раньше диспетчер был длиннее того, что
// он выбирал, потому что прогон нагрузки оставался внутри него.
//
// Аргументы и потоки приходят параметрами, а не читаются из глобальных:
// только так контракт кодов выхода проверяется тестом, а не руками.
func run(args []string, stdin, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := newFlags(fs, stderr)

	// Ошибку Parse FlagSet печатает сам вместе с подсказкой; ошибку файла —
	// нет, она наша и помечена типом.
	if err := parseArgs(fs, f, args); err != nil {
		if isFileError(err) {
			fmt.Fprintln(stderr, "ошибка:", err)
		}
		return exitUsage
	}
	if *f.showVersion {
		fmt.Fprintln(stdout, "loadgen", buildVersion())
		return exitOK
	}
	// Сравнение — отдельный режим: прогона нет, и всё, что ниже, ему чуждо.
	if *f.compare {
		return runCompare(f, fs, stdout, stderr)
	}

	return runLoad(f, fs, stdin, stdout, stderr)
}
