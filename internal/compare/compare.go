// Сравнение двух наборов отчётов: стало лучше или хуже.
//
// Отдельный пакет от report по тому же основанию, что и slo: report меняется,
// когда меняется документ, compare — когда меняется правило сравнения.
//
//	compare.go   пороги, сверка, вердикт
//	side.go      чтение стороны с диска и медианы
//	metrics.go   что сравниваем и в какую сторону хорошо
//	text.go      печать таблицы
package compare

import "fmt"

// Thresholds — насколько метрике позволено ухудшиться, в процентах.
// Ноль означает «порог не задан»: без него сравнение остаётся отчётом
// и кода выхода не меняет.
type Thresholds struct {
	P99 float64
	RPS float64
}

func (t Thresholds) forGate(gate string) float64 {
	switch gate {
	case "p99":
		return t.P99
	case "rps":
		return t.RPS
	}
	return 0
}

// Row — одна метрика в таблице сравнения.
type Row struct {
	Label       string
	Before      string // отформатированное значение либо прочерк
	After       string
	Change      string
	Regressed   bool // ухудшилась сверх заданного порога
	comparable_ bool
}

// Result — что вышло из сравнения.
type Result struct {
	Before, After Side
	Rows          []Row
}

// Regressed — сработал ли хоть один заданный порог.
func (r Result) Regressed() bool {
	for _, row := range r.Rows {
		if row.Regressed {
			return true
		}
	}
	return false
}

// Compare сверяет две стороны. Ошибка означает, что сравнивать нечего:
// сравнить несравнимое хуже, чем отказаться.
func Compare(before, after Side, thr Thresholds) (Result, error) {
	if err := sameSetup(before.Setup, after.Setup); err != nil {
		return Result{}, err
	}

	res := Result{Before: before, After: after}
	for _, m := range metrics {
		res.Rows = append(res.Rows, compareMetric(m, before, after, thr.forGate(m.gate)))
	}
	return res, nil
}

func compareMetric(m metric, before, after Side, limit float64) Row {
	b, a := before.values[m.label], after.values[m.label]

	row := Row{Label: m.label, Before: noData, After: noData, Change: noData}
	if b != nil {
		row.Before = m.format(*b)
	}
	if a != nil {
		row.After = m.format(*a)
	}
	if b == nil || a == nil {
		return row
	}
	row.comparable_ = true

	// Процент от нуля не считается: показываем, что было и что стало.
	if *b == 0 {
		row.Change = "с нуля"
		if *a == 0 {
			row.Change = "без изменений"
		}
		return row
	}

	delta := (*a - *b) / *b * 100
	row.Change = fmt.Sprintf("%+.1f%%", delta)

	worse := delta
	if m.better == higher {
		worse = -delta
	}
	if limit > 0 && worse > limit {
		row.Regressed = true
		row.Change += fmt.Sprintf(" — хуже порога в %.0f%%", limit)
	}
	return row
}
