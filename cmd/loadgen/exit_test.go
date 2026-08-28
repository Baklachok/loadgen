package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// capture прогоняет run с подменёнными потоками и возвращает всё сразу:
// код, stdout и stderr.
func capture(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	outR, outW := pipe(t)
	errR, errW := pipe(t)

	// По каналу на поток: общий не даёт понять, чья строка пришла первой.
	outDone, errDone := drain(outR), drain(errR)

	code = run(args, outW, errW)
	outW.Close()
	errW.Close()

	return code, <-outDone, <-errDone
}

// pipe, а не bytes.Buffer: run принимает *os.File именно затем, чтобы
// определение TTY работало по-настоящему. Труба терминалом не является,
// и цвет в тестах выключается сам собой.
func pipe(t *testing.T) (r, w *os.File) {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, w
}

func drain(r *os.File) <-chan string {
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	return out
}

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
