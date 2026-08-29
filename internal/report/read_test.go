package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden-файл — канонический документ пакета, и читатель проверяется им же:
// разойдись читатель с писателем, тест это поймает без отдельной фикстуры.
func TestReadSummary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "summary.golden.json"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("документ разбирается целиком", func(t *testing.T) {
		s, err := ReadSummary(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}

		if s.Schema != schemaVersion {
			t.Errorf("schema = %d, ожидалось %d", s.Schema, schemaVersion)
		}
		if s.Setup.URL == "" || s.Setup.Concurrency == 0 {
			t.Errorf("config не доехал: %+v", s.Setup)
		}
		if s.RPS == 0 || s.SuccessRate == 0 {
			t.Errorf("headline-числа не доехали: rps=%v success=%v", s.RPS, s.SuccessRate)
		}
		if s.Latency.P50 == nil {
			t.Error("p50 потерян")
		}
	})

	// Ради этого схема и заводилась: между версиями 1 и 2 у failed сменился
	// смысл, и сравнение таких отчётов сопоставляло бы разные величины.
	t.Run("чужая схема отвергается", func(t *testing.T) {
		alien := strings.Replace(string(raw), `"schema": 2`, `"schema": 99`, 1)
		if !strings.Contains(alien, `"schema": 99`) {
			t.Fatal("подмена схемы не удалась — проверьте формат golden-файла")
		}

		if _, err := ReadSummary(strings.NewReader(alien)); err == nil {
			t.Error("документ чужой версии принят")
		}
	})

	// null означает «замеров не хватило». Ноль на его месте вернул бы ту
	// самую ложь, ради устранения которой null и появился.
	t.Run("недостоверный перцентиль остаётся пустым", func(t *testing.T) {
		var b strings.Builder
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, `"p99_ms"`) {
				line = strings.SplitN(line, ":", 2)[0] + `: null,`
			}
			b.WriteString(line + "\n")
		}

		s, err := ReadSummary(strings.NewReader(b.String()))
		if err != nil {
			t.Fatal(err)
		}
		if s.Latency.P99 != nil {
			t.Errorf("p99 = %v, ожидался прочерк", *s.Latency.P99)
		}
	})

	t.Run("мусор вместо документа", func(t *testing.T) {
		if _, err := ReadSummary(strings.NewReader("не json")); err == nil {
			t.Error("мусор принят за отчёт")
		}
	})
}
