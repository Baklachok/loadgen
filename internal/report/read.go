// Чтение отчёта обратно. Рядом с json.go намеренно: имена полей должны
// меняться в одном месте. Читатель в другом пакете разошёлся бы с писателем
// молча — без единой ошибки компиляции.
package report

import (
	"encoding/json"
	"fmt"
	"io"
)

// RunSetup — то, чем два прогона обязаны совпадать, чтобы их сравнение
// что-то значило. Версия, время старта, протокол и GOMAXPROCS сюда не входят:
// они обязаны отличаться.
type RunSetup struct {
	URL         string  `json:"url"`
	Method      string  `json:"method"`
	Requests    int     `json:"requests"`
	DurationMs  float64 `json:"duration_ms"`
	Concurrency int     `json:"concurrency"`
	Rate        float64 `json:"rate"`
}

// RunSummary — прочитанный отчёт: только те числа, которыми прогоны
// сравнивают. Восстанавливать stats.Summary целиком незачем — обратные
// преобразования всего документа тут не нужны.
//
// Перцентили указатели: в документе они могут быть null, когда выборки
// не хватило. Подставить туда ноль значило бы вернуть ровно ту ложь,
// ради устранения которой null и появился.
type RunSummary struct {
	Schema  int      `json:"schema"`
	Partial bool     `json:"partial"`
	Setup   RunSetup `json:"config"`

	RPS         float64 `json:"rps"`
	SuccessRate float64 `json:"success_rate"`
	Throughput  float64 `json:"throughput_mb_s"`

	NonOK     int `json:"non_2xx"`
	Failed    int `json:"failed"`
	Truncated int `json:"truncated"`

	Latency struct {
		P50 *float64 `json:"p50_ms"`
		P90 *float64 `json:"p90_ms"`
		P95 *float64 `json:"p95_ms"`
		P99 *float64 `json:"p99_ms"`
	} `json:"latency"`

	// Corrected появляется только в open-loop: в closed-loop расписания
	// не было, и поправлять нечего.
	Corrected *struct {
		P99 *float64 `json:"p99_ms"`
	} `json:"corrected"`
}

// ReadSummary разбирает отчёт и отвергает документ чужой версии.
//
// Именно ради этого схема и заводилась: между версиями 1 и 2 у failed
// сменился смысл, и сравнение таких отчётов сопоставляло бы разные величины
// под одним именем.
func ReadSummary(r io.Reader) (RunSummary, error) {
	var s RunSummary
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return RunSummary{}, fmt.Errorf("не разобрать отчёт: %w", err)
	}
	if s.Schema != schemaVersion {
		return RunSummary{}, fmt.Errorf("схема %d, а понимаем %d: отчёт снят другой версией", s.Schema, schemaVersion)
	}
	return s, nil
}
