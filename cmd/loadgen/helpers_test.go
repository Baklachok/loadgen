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
	"go.uber.org/goleak"
)

// goleak здесь нашёл настоящее: горутина-наблюдатель из interruptible висела
// на <-signals после каждого прогона. В бою безвредно — процесс тут же
// выходит, — но это была утечка, и починить её пришлось до того, как проверка
// стала зелёной.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// localURL — своя система: по ней прогон не спрашивает подтверждения ни на
// какой нагрузке, и тесты флагов не отвлекаются на этот вопрос.
const localURL = "http://localhost:8080/"

// capture прогоняет run с подменёнными потоками и возвращает всё сразу:
// код, stdout и stderr.
func capture(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()

	l := launch(t, args...)
	return <-l.code, <-l.stdout, <-l.stderr
}

// launched — идущий run: код и потоки приходят по каналам, когда он кончится,
// а stderrLive можно читать по ходу — так тест /metrics узнаёт порт.
type launched struct {
	code           <-chan int
	stdout, stderr <-chan string
	stderrLive     *os.File
}

// launch запускает run в горутине с трубами вместо потоков. Раньше каждый
// тест, которому нужен был идущий прогон, собирал те же три трубы руками.
func launch(t *testing.T, args ...string) launched {
	t.Helper()

	outR, outW := pipe(t)
	errR, errW := pipe(t)

	// stdin — труба, а не терминал: спрашивать в тестах некого, и это ровно
	// та ветка, что работает в CI.
	inR, inW := pipe(t)
	inW.Close()

	// По каналу на поток: общий не даёт понять, чья строка пришла первой.
	// stderr раздваивается: liveR читают по ходу, teeR собирают целиком.
	// pipe отдаёт (r, w) — первая версия перепутала концы и писала
	// в read-конец: тройник молча терял всё, и адрес /metrics не приходил.
	liveR, liveW := pipe(t)
	teeR, teeW := pipe(t)
	outDone, errDone := drain(outR), drain(teeR)
	go func() {
		io.Copy(io.MultiWriter(teeW, liveW), errR) //nolint:errcheck // тестовая труба
		teeW.Close()
		liveW.Close()
	}()

	code := make(chan int, 1)
	go func() {
		code <- run(args, inR, outW, errW)
		outW.Close()
		errW.Close()
	}()
	return launched{code: code, stdout: outDone, stderr: errDone, stderrLive: liveR}
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
