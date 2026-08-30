package compare

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Baklachok/loadgen/internal/report"
)

// doc — минимальный отчёт текущей схемы. Пишем сырым JSON, а не через
// report: сравниватель обязан читать то, что лежит на диске, а не то,
// что мы умеем собрать в памяти. Числа в config целые — подмена поля
// в TestCompareRefusesMismatch на это опирается.
func doc(t *testing.T, dir, name string, edit func(map[string]any)) string {
	t.Helper()

	m := map[string]any{
		"schema":  report.SchemaVersion,
		"partial": false,
		"config": map[string]any{
			"url": "http://localhost:8080/", "method": "GET",
			"requests": 1000, "duration_ms": 0, "concurrency": 20, "rate": 0,
		},
		"rps": 1000.0, "success_rate": 1.0, "throughput_mb_s": 1.0,
		"non_2xx": 0, "failed": 0, "truncated": 0,
		"latency": map[string]any{"p50_ms": 5.0, "p90_ms": 9.0, "p95_ms": 10.0, "p99_ms": 20.0},
	}
	if edit != nil {
		edit(m)
	}

	path := filepath.Join(dir, name)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func latency(m map[string]any) map[string]any { return m["latency"].(map[string]any) }
func config(m map[string]any) map[string]any  { return m["config"].(map[string]any) }

// load — одна сторона из одного отчёта; ошибка загрузки здесь не предмет
// теста, а помеха.
func load(t *testing.T, edit func(map[string]any)) Side {
	t.Helper()
	side, err := Load(doc(t, t.TempDir(), "r.json", edit))
	if err != nil {
		t.Fatal(err)
	}
	return side
}

// compare — сравнение, которое обязано состояться; отказ проверяет
// TestCompareRefusesMismatch.
func compare(t *testing.T, before, after Side, thr Thresholds) Result {
	t.Helper()
	res, err := Compare(before, after, thr)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func row(t *testing.T, res Result, label string) Row {
	t.Helper()
	for _, r := range res.Rows {
		if r.Label == label {
			return r
		}
	}
	t.Fatalf("в таблице нет строки %q", label)
	return Row{}
}

func pct(v float64) *float64 { return &v }

func TestLoad(t *testing.T) {
	t.Run("каталог даёт медиану", func(t *testing.T) {
		dir := t.TempDir()
		for i, p99 := range []float64{10, 30, 20} {
			doc(t, dir, string(rune('a'+i))+".json", func(m map[string]any) {
				latency(m)["p99_ms"] = p99
			})
		}

		side, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if side.Runs != 3 {
			t.Errorf("прогонов %d, ожидалось 3", side.Runs)
		}
		if got := side.values["p99"]; got == nil || *got != 20 {
			t.Errorf("медиана p99 = %v, ожидалось 20", got)
		}
	})

	t.Run("один файл — один прогон", func(t *testing.T) {
		if side := load(t, nil); side.Runs != 1 {
			t.Errorf("прогонов %d, ожидался 1", side.Runs)
		}
	})

	// Медиана по неполному набору тише соврала бы, чем прочерк.
	t.Run("нет метрики хотя бы в одном — нет и медианы", func(t *testing.T) {
		dir := t.TempDir()
		doc(t, dir, "a.json", nil)
		doc(t, dir, "b.json", func(m map[string]any) { latency(m)["p99_ms"] = nil })

		side, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if side.values["p99"] != nil {
			t.Error("медиана посчитана по набору, где p99 был не везде")
		}
	})

	t.Run("прерванный прогон отвергается", func(t *testing.T) {
		path := doc(t, t.TempDir(), "cut.json", func(m map[string]any) { m["partial"] = true })
		if _, err := Load(path); err == nil {
			t.Error("прерванный прогон принят к сравнению")
		}
	})

	t.Run("разнобой внутри каталога отвергается", func(t *testing.T) {
		dir := t.TempDir()
		doc(t, dir, "a.json", nil)
		doc(t, dir, "b.json", func(m map[string]any) { config(m)["concurrency"] = 99 })

		if _, err := Load(dir); err == nil {
			t.Error("каталог с разными конфигами принят за одну сторону")
		}
	})

	t.Run("пустой каталог и несуществующий путь", func(t *testing.T) {
		if _, err := Load(t.TempDir()); err == nil {
			t.Error("пустой каталог принят")
		}
		if _, err := Load(filepath.Join(t.TempDir(), "нет")); err == nil {
			t.Error("несуществующий путь принят")
		}
	})
}

// Сравнить несравнимое хуже, чем отказаться: цифры сойдутся, а вывод
// будет про разные эксперименты.
func TestCompareRefusesMismatch(t *testing.T) {
	for _, field := range []string{"url", "method", "requests", "duration_ms", "concurrency", "rate"} {
		t.Run(field, func(t *testing.T) {
			before := load(t, nil)
			after := load(t, func(m map[string]any) {
				switch v := config(m)[field].(type) {
				case string:
					config(m)[field] = v + "-другое"
				case int:
					config(m)[field] = v + 1
				}
			})

			_, err := Compare(before, after, Thresholds{})
			if err == nil {
				t.Fatalf("%s разошёлся, а сравнение состоялось", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Errorf("ошибка не называет поле: %v", err)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	t.Run("порог не задан — регрессии нет ни при какой дельте", func(t *testing.T) {
		after := load(t, func(m map[string]any) { latency(m)["p99_ms"] = 200.0 })

		res := compare(t, load(t, nil), after, Thresholds{})
		if res.Regressed() {
			t.Error("без порога сравнение обязано оставаться отчётом")
		}
		if got := row(t, res, "p99").Change; !strings.Contains(got, "+900") {
			t.Errorf("изменение p99 = %q, ожидалось +900%%", got)
		}
	})

	// Явный ноль — ни на процент: названный ноль не должен молча значить
	// «порога нет», как и -slo-error-rate 0.
	t.Run("явный ноль ловит любое ухудшение", func(t *testing.T) {
		after := load(t, func(m map[string]any) { latency(m)["p99_ms"] = 20.2 }) // +1%

		if !row(t, compare(t, load(t, nil), after, Thresholds{P99: pct(0)}), "p99").Regressed {
			t.Error("рост p99 на 1% при нулевом пороге не признан регрессией")
		}
		if compare(t, load(t, nil), load(t, nil), Thresholds{P99: pct(0)}).Regressed() {
			t.Error("без изменений нулевой порог сработал")
		}
	})

	t.Run("порог сработал", func(t *testing.T) {
		after := load(t, func(m map[string]any) { latency(m)["p99_ms"] = 30.0 }) // +50%

		res := compare(t, load(t, nil), after, Thresholds{P99: pct(10)})
		if !res.Regressed() || !row(t, res, "p99").Regressed {
			t.Error("рост p99 на 50% при пороге 10% не признан регрессией")
		}
	})

	t.Run("улучшение регрессией не считается", func(t *testing.T) {
		after := load(t, func(m map[string]any) { latency(m)["p99_ms"] = 5.0 })

		if compare(t, load(t, nil), after, Thresholds{P99: pct(10)}).Regressed() {
			t.Error("падение p99 засчитано за регрессию")
		}
	})

	// RPS считается в другую сторону: хуже — когда меньше.
	t.Run("направление у RPS обратное", func(t *testing.T) {
		slower := load(t, func(m map[string]any) { m["rps"] = 500.0 })
		faster := load(t, func(m map[string]any) { m["rps"] = 2000.0 })

		if !row(t, compare(t, load(t, nil), slower, Thresholds{RPS: pct(10)}), "RPS").Regressed {
			t.Error("падение RPS вдвое не признано регрессией")
		}
		if compare(t, load(t, nil), faster, Thresholds{RPS: pct(10)}).Regressed() {
			t.Error("рост RPS засчитан за регрессию")
		}
	})

	t.Run("недостоверный перцентиль — прочерк, а не ноль", func(t *testing.T) {
		before := load(t, func(m map[string]any) { latency(m)["p99_ms"] = nil })

		r := row(t, compare(t, before, load(t, nil), Thresholds{P99: pct(10)}), "p99")
		if r.Before != noData || r.Change != noData {
			t.Errorf("p99 без данных показан как %q → %q (%s)", r.Before, r.After, r.Change)
		}
		if r.Regressed {
			t.Error("порог сработал на метрике, которой не было")
		}
	})

	// По двум одиночным прогонам разницу в проценты интерпретировать нельзя,
	// и умолчать об этом значит выдать анекдот за измерение.
	t.Run("одиночные прогоны названы анекдотом", func(t *testing.T) {
		var b bytes.Buffer
		compare(t, load(t, nil), load(t, nil), Thresholds{}).Write(&b)
		if !strings.Contains(b.String(), "анекдот") {
			t.Errorf("оговорки про одиночный прогон нет:\n%s", b.String())
		}
	})
}
