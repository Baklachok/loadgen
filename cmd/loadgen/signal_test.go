package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// Сигналы посылаются самому тестовому процессу: обработчик к этому моменту
// уже установлен, поэтому процесс их переживает.
func raise(t *testing.T) {
	t.Helper()

	self, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := self.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
}

func TestInterrupt(t *testing.T) {
	// Первый сигнал сворачивает прогон, но не убивает процесс: иначе
	// пропадёт всё, что уже успели измерить, — а ради этого Ctrl+C и жмут.
	t.Run("первый сигнал даёт досчитать", func(t *testing.T) {
		var stderr bytes.Buffer
		exited := make(chan int, 1)

		ctx, stop := interruptible(&stderr, func(code int) { exited <- code })
		defer stop()

		raise(t)
		<-ctx.Done()

		select {
		case code := <-exited:
			t.Fatalf("вышли по первому сигналу с кодом %d: результат потерян", code)
		default:
		}
		if !strings.Contains(stderr.String(), "остановка") {
			t.Errorf("не предупредили, что собираем результаты: %q", stderr.String())
		}
	})

	// Без этого зависший запрос делает Ctrl+C бесполезным: контекст отменён,
	// а процесс стоит, и остаётся только kill -9.
	t.Run("второй сигнал обрывает немедленно", func(t *testing.T) {
		var stderr bytes.Buffer
		exited := make(chan int, 1)

		ctx, stop := interruptible(&stderr, func(code int) { exited <- code })
		defer stop()

		raise(t)
		<-ctx.Done()
		raise(t)

		select {
		case code := <-exited:
			if code != exitInterrupt {
				t.Errorf("код выхода %d, ожидался %d", code, exitInterrupt)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("второй сигнал проглочен: остаётся только kill -9")
		}
	})

	// Обычный выход через stop() будил горутину-наблюдателя, и «остановка,
	// собираю результаты…» печаталась там, где никто ничего не останавливал.
	t.Run("нормальный выход молчит", func(t *testing.T) {
		var stderr bytes.Buffer
		exited := make(chan int, 1)

		_, stop := interruptible(&stderr, func(code int) { exited <- code })
		stop()

		time.Sleep(50 * time.Millisecond)

		if s := stderr.String(); s != "" {
			t.Errorf("на обычном выходе напечатано %q", s)
		}
		select {
		case code := <-exited:
			t.Errorf("обычный выход позвал exit(%d)", code)
		default:
		}
	})
}
