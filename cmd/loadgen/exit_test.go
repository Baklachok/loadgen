package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestExitCodes(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	tests := []struct {
		name string
		args []string
		want int
	}{
		{"успешный прогон", []string{"-n", "5", "-c", "1", ok.URL}, exitOK},

		// Сервер отдаёт одни пятисотки — инструмент отработал, он ровно для
		// того и нужен, чтобы это показать. Не его ошибка.
		{"сервер отвечает 500", []string{"-n", "5", "-c", "1", failing.URL}, exitOK},

		{"неизвестный флаг", []string{"-нет-такого", ok.URL}, exitUsage},
		{"URL не указан", []string{"-n", "5"}, exitUsage},
		{"два URL", []string{ok.URL, ok.URL}, exitUsage},
		{"несовместимые -n и -z", []string{"-n", "5", "-z", "1s", ok.URL}, exitUsage},
		{"неизвестный формат вывода", []string{"-o", "yaml", "-n", "1", ok.URL}, exitUsage},
		{"кривой заголовок", []string{"-H", "без-двоеточия", "-n", "1", ok.URL}, exitUsage},

		// Ни одного ответа: измерить не удалось, это другое, чем «измерили плохо»
		{"никто не слушает", []string{"-n", "5", "-c", "1", "-t", "300ms", "http://127.0.0.1:1"}, exitNoRun},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := capture(t, tt.args...)
			if code != tt.want {
				t.Errorf("код %d, ожидался %d\nstderr: %s", code, tt.want, stderr)
			}
		})
	}
}

// Разбор флагов идёт через ContinueOnError именно ради этого: ExitOnError
// вышел бы с кодом 2, который здесь занят под «измерить не удалось».
func TestUsageErrorIsNotConfusedWithFailedRun(t *testing.T) {
	usage, _, _ := capture(t, "-нет-такого")
	noRun, _, _ := capture(t, "-n", "2", "-c", "1", "-t", "200ms", "http://127.0.0.1:1")

	if usage == noRun {
		t.Fatalf("ошибка использования и несостоявшийся прогон дали один код %d", usage)
	}
	if usage != exitUsage || noRun != exitNoRun {
		t.Errorf("usage=%d (ожидался %d), noRun=%d (ожидался %d)", usage, exitUsage, noRun, exitNoRun)
	}
}

func TestVersionExitsOK(t *testing.T) {
	code, stdout, _ := capture(t, "-version")

	if code != exitOK {
		t.Errorf("код %d, ожидался %d", code, exitOK)
	}
	if !strings.Contains(stdout, "loadgen") {
		t.Errorf("версия не напечатана в stdout: %q", stdout)
	}
}

// Пороги приёмки — то, ради чего вообще нужен код 3: без них прогон
// «успешен» даже когда сервис стал вдвое медленнее.
func TestSLOExitCodes(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
	}))
	defer slow.Close()

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	t.Run("порог выдержан", func(t *testing.T) {
		code, _, _ := capture(t, "-n", "1000", "-c", "50", "-slo-error-rate", "1", slow.URL)
		if code != exitOK {
			t.Errorf("код %d, ожидался %d", code, exitOK)
		}
	})

	t.Run("доля ошибок нарушена", func(t *testing.T) {
		code, _, stderr := capture(t, "-n", "1000", "-c", "50", "-slo-error-rate", "1", failing.URL)
		if code != exitSLO {
			t.Errorf("код %d, ожидался %d", code, exitSLO)
		}
		if !strings.Contains(stderr, "SLO error-rate") {
			t.Errorf("нарушение не названо в stderr: %q", stderr)
		}
	})

	t.Run("p99 нарушен", func(t *testing.T) {
		code, _, stderr := capture(t, "-n", "1000", "-c", "50", "-slo-p99", "1ms", slow.URL)
		if code != exitSLO {
			t.Errorf("код %d, ожидался %d", code, exitSLO)
		}
		if !strings.Contains(stderr, "SLO p99") {
			t.Errorf("нарушение не названо: %q", stderr)
		}
	})

	// Прогон, на котором проверку выполнить нельзя, — это «не измерили»,
	// а не «выдержали»: зелёный CI тут был бы обманом.
	t.Run("замеров не хватило на p99", func(t *testing.T) {
		code, _, stderr := capture(t, "-n", "20", "-c", "2", "-slo-p99", "1s", slow.URL)
		if code != exitNoRun {
			t.Errorf("код %d, ожидался %d", code, exitNoRun)
		}
		if !strings.Contains(stderr, "нет данных") {
			t.Errorf("причина не названа: %q", stderr)
		}
	})

	t.Run("без порогов прогон успешен", func(t *testing.T) {
		if code, _, _ := capture(t, "-n", "20", "-c", "2", failing.URL); code != exitOK {
			t.Errorf("код %d без -slo-*, ожидался %d", code, exitOK)
		}
	})
}
