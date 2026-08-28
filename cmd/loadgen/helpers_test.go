// Общее для тестов пакета: запуск run с подменёнными потоками и разбор
// аргументов. Отдельным файлом, потому что capture нужен и exit_test.go,
// и confirm_test.go, — а жил у того, кто завёл его первым.
package main

import (
	"flag"
	"io"
	"os"
	"testing"

	"github.com/Baklachok/loadgen/internal/runner"
)

// localURL — своя система: по ней прогон не спрашивает подтверждения ни на
// какой нагрузке, и тесты флагов не отвлекаются на этот вопрос.
const localURL = "http://localhost:8080/"

// capture прогоняет run с подменёнными потоками и возвращает всё сразу:
// код, stdout и stderr.
func capture(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	outR, outW := pipe(t)
	errR, errW := pipe(t)

	// По каналу на поток: общий не даёт понять, чья строка пришла первой.
	outDone, errDone := drain(outR), drain(errR)

	// stdin — труба, а не терминал: спрашивать в тестах некого, и это ровно
	// та ветка, что работает в CI.
	inR, inW := pipe(t)
	inW.Close()

	code = run(args, inR, outW, errW)
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

// parseConfig прогоняет аргументы через тот же путь, что и run: сначала
// FlagSet, потом config. Иначе не проверить правила, которым нужно знать
// не значение флага, а был ли он назван вообще.
func parseConfig(t *testing.T, args ...string) (runner.Config, error) {
	t.Helper()

	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f := newFlags(fs, io.Discard)

	if err := fs.Parse(args); err != nil {
		t.Fatalf("разбор %v: %v", args, err)
	}
	return f.config(fs)
}
