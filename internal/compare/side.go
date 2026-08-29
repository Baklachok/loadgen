// Одна сторона сравнения: чтение отчётов с диска и сведение их к медианам.
package compare

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/Baklachok/loadgen/internal/report"
)

// Side — одна сторона сравнения: один отчёт или медиана нескольких.
type Side struct {
	Name   string
	Runs   int
	Setup  report.RunSetup
	values map[string]*float64
}

// Load читает файл или все *.json из каталога.
//
// Каталог — не украшение: README проекта говорит «один прогон — анекдот,
// три прогона, медиана», и сравниватель, умеющий только по одному файлу,
// противоречил бы этому совету.
func Load(path string) (Side, error) {
	files, err := reportFiles(path)
	if err != nil {
		return Side{}, err
	}

	runs := make([]report.RunSummary, 0, len(files))
	for _, f := range files {
		s, err := readFile(f)
		if err != nil {
			return Side{}, fmt.Errorf("%s: %w", f, err)
		}
		if s.Partial {
			return Side{}, fmt.Errorf("%s: прогон прерван, цифры собраны не по всему плану", f)
		}
		if len(runs) > 0 {
			if err := sameSetup(runs[0].Setup, s.Setup); err != nil {
				return Side{}, fmt.Errorf("%s: %w", f, err)
			}
		}
		runs = append(runs, s)
	}

	side := Side{Name: path, Runs: len(runs), Setup: runs[0].Setup, values: map[string]*float64{}}
	for _, m := range metrics {
		side.values[m.label] = median(runs, m.get)
	}
	return side, nil
}

func reportFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("нет такого файла или каталога: %s", path)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{path}, nil
	}

	files, err := filepath.Glob(filepath.Join(path, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("в каталоге %s нет ни одного *.json", path)
	}
	slices.Sort(files)
	return files, nil
}

func readFile(path string) (report.RunSummary, error) {
	// Путь называет человек в командной строке — прочитать именно его
	// и есть работа режима сравнения.
	//nolint:gosec // путь приходит аргументом CLI, это не подстановка извне
	f, err := os.Open(path)
	if err != nil {
		return report.RunSummary{}, err
	}
	defer f.Close()

	return report.ReadSummary(f)
}

// median возвращает nil, если хотя бы в одном прогоне метрики не было:
// медиана по неполному набору тише соврала бы, чем прочерк.
func median(runs []report.RunSummary, get func(report.RunSummary) *float64) *float64 {
	vs := make([]float64, 0, len(runs))
	for _, r := range runs {
		v := get(r)
		if v == nil {
			return nil
		}
		vs = append(vs, *v)
	}

	slices.Sort(vs)
	mid := len(vs) / 2
	if len(vs)%2 == 1 {
		return &vs[mid]
	}
	avg := (vs[mid-1] + vs[mid]) / 2
	return &avg
}

// sameSetup сверяет, что прогоны об одном и том же. Разный URL или разная
// нагрузка — это разные эксперименты, а не «до и после».
func sameSetup(a, b report.RunSetup) error {
	for _, f := range []struct {
		name string
		a, b any
	}{
		{"url", a.URL, b.URL},
		{"method", a.Method, b.Method},
		{"requests", a.Requests, b.Requests},
		{"duration_ms", a.DurationMs, b.DurationMs},
		{"concurrency", a.Concurrency, b.Concurrency},
		{"rate", a.Rate, b.Rate},
	} {
		if f.a != f.b {
			return fmt.Errorf("прогоны несравнимы: %s это %v против %v", f.name, f.a, f.b)
		}
	}
	return nil
}
