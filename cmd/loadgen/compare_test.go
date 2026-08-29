package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Baklachok/loadgen/internal/report"
)

// report кладёт на диск отчёт схемы 2 с заданным p99.
func reportFile(t *testing.T, dir, name string, p99 float64) string {
	t.Helper()

	path := filepath.Join(dir, name)
	body := `{"schema":` + strconv.Itoa(report.SchemaVersion) + `,"partial":false,
	  "config":{"url":"http://localhost:8080/","method":"GET","requests":1000,
	            "duration_ms":0,"concurrency":20,"rate":0},
	  "rps":1000,"success_rate":1,"throughput_mb_s":1,
	  "non_2xx":0,"failed":0,"truncated":0,
	  "latency":{"p50_ms":5,"p90_ms":9,"p95_ms":10,"p99_ms":` + fmtFloat(p99) + `}}`

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Сравнение — отдельный режим CLI: прогона нет, URL не нужен, и код выхода
// подчиняется тому же контракту, что у -slo-*.
func TestCompareMode(t *testing.T) {
	dir := t.TempDir()
	before := reportFile(t, dir, "before.json", 20)
	after := reportFile(t, dir, "after.json", 40) // +100%

	t.Run("без порога — отчёт и код 0", func(t *testing.T) {
		code, stdout, _ := capture(t, "-compare", before, after)

		if code != exitOK {
			t.Errorf("код %d, ожидался %d", code, exitOK)
		}
		if !strings.Contains(stdout, "+100.0%") {
			t.Errorf("изменения p99 не видно:\n%s", stdout)
		}
		// Оговорка про одиночные прогоны обязана быть видна.
		if !strings.Contains(stdout, "анекдот") {
			t.Errorf("нет оговорки про один прогон:\n%s", stdout)
		}
	})

	t.Run("порог превышен — код 3", func(t *testing.T) {
		code, _, _ := capture(t, "-compare", "-regress-p99", "10", before, after)
		if code != exitSLO {
			t.Errorf("код %d, ожидался %d", code, exitSLO)
		}
	})

	t.Run("порог не превышен — код 0", func(t *testing.T) {
		code, _, _ := capture(t, "-compare", "-regress-p99", "500", before, after)
		if code != exitOK {
			t.Errorf("код %d, ожидался %d", code, exitOK)
		}
	})

	t.Run("нужны ровно два пути", func(t *testing.T) {
		for _, args := range [][]string{
			{"-compare", before},
			{"-compare", before, after, before},
		} {
			if code, _, _ := capture(t, args...); code != exitUsage {
				t.Errorf("%v: код %d, ожидался %d", args, code, exitUsage)
			}
		}
	})

	// Молча игнорировать заданный формат нельзя, а машинного потребителя
	// у сравнения пока нет.
	t.Run("-o json отвергается", func(t *testing.T) {
		code, _, stderr := capture(t, "-compare", "-o", "json", before, after)

		if code != exitUsage {
			t.Errorf("код %d, ожидался %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "-o json") {
			t.Errorf("причина не названа: %s", stderr)
		}
	})

	t.Run("нечитаемый путь — код 1", func(t *testing.T) {
		if code, _, _ := capture(t, "-compare", filepath.Join(dir, "нет.json"), after); code != exitUsage {
			t.Errorf("код %d, ожидался %d", code, exitUsage)
		}
	})
}

// Для compare ноль — «порог не задан», и отрицательный попадал туда же:
// гейт молча выключен. Пути не существуют нарочно: отказ по флагу обязан
// быть раньше чтения файлов.
func TestCompareRejectsNegativeThreshold(t *testing.T) {
	for _, flagName := range []string{"-regress-p99", "-regress-rps"} {
		t.Run(flagName, func(t *testing.T) {
			code, _, stderr := capture(t, "-compare", flagName, "-5", "нет/такого", "и/такого")
			if code != exitUsage {
				t.Errorf("код %d, ожидался %d", code, exitUsage)
			}
			if !strings.Contains(stderr, flagName) {
				t.Errorf("отказ не про флаг: %s", stderr)
			}
		})
	}
}
