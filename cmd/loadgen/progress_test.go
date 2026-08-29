package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

// Строка рисуется в буфер через newProgress — pty ради теста не тащим.
func drawOnce(t *testing.T, total int, cfg runner.Config) (drawn string, after string) {
	t.Helper()
	acc := stats.NewAccumulator(0)
	for i := 0; i < total; i++ {
		acc.Add(runner.Result{Duration: time.Millisecond, StatusCode: 200})
	}

	var buf bytes.Buffer
	p := makeProgress(&buf, 120, cfg, acc)
	p.start = time.Now().Add(-2 * time.Second) // чтобы «прошло 2s» было детерминировано
	p.draw()
	drawn = buf.String()
	buf.Reset()
	p.erase()
	return drawn, buf.String()
}

func TestProgressLine(t *testing.T) {
	t.Run("режим -z: прошло / всего и число запросов", func(t *testing.T) {
		line, _ := drawOnce(t, 6012, runner.Config{Duration: 30 * time.Minute})
		for _, want := range []string{"\r", "2s / 30m", "6 012 запросов", "RPS", "отказов 0"} {
			if !strings.Contains(line, want) {
				t.Errorf("нет %q в %q", want, line)
			}
		}
		if strings.Contains(line, "\n") {
			t.Error("перевод строки ломает \\r-перерисовку")
		}
	})

	t.Run("режим -n: сделано / заказано", func(t *testing.T) {
		line, _ := drawOnce(t, 6012, runner.Config{Requests: 10000})
		if !strings.Contains(line, "6 012 / 10 000 запросов") {
			t.Errorf("нет счёта до предела в %q", line)
		}
	})

	// Та же честность, что в отчёте: p99 по пятидесяти замерам — максимум
	// под красивым именем, и в строке ему не место.
	t.Run("недостоверный p99 — прочерк", func(t *testing.T) {
		line, _ := drawOnce(t, 50, runner.Config{Requests: 100})
		if !strings.Contains(line, "p99 —") {
			t.Errorf("на 50 замерах напечатано число: %q", line)
		}
		line, _ = drawOnce(t, 1500, runner.Config{Requests: 2000})
		if strings.Contains(line, "p99 —") {
			t.Errorf("на 1500 замерах прочерк: %q", line)
		}
	})

	t.Run("стирание покрывает нарисованное", func(t *testing.T) {
		line, erase := drawOnce(t, 100, runner.Config{Requests: 100})
		drawnLen := len([]rune(strings.TrimPrefix(line, "\r")))
		if erase != "\r"+strings.Repeat(" ", drawnLen)+"\r" {
			t.Errorf("стирание %q не покрывает строку длиной %d", erase, drawnLen)
		}
	})

	t.Run("узкий терминал режет, а не переносит", func(t *testing.T) {
		acc := stats.NewAccumulator(0)
		var buf bytes.Buffer
		p := makeProgress(&buf, 30, runner.Config{Duration: time.Hour}, acc)
		p.draw()
		if got := len([]rune(strings.TrimPrefix(buf.String(), "\r"))); got > 29 {
			t.Errorf("строка в %d символов при ширине 30", got)
		}
	})
}

// В пайпе строки нет: stderr — труба, а не терминал, и это ровно ветка CI.
func TestProgressSilentWithoutTTY(t *testing.T) {
	srv := newTestServer(t)
	_, _, stderr := capture(t, "-z", "1200ms", "-c", "2", srv)
	if strings.Contains(stderr, "\r") {
		t.Errorf("в stderr без терминала есть \\r:\n%q", stderr)
	}
}

// stop ждёт горутину и стирает; повторный stop безвреден.
func TestProgressStop(t *testing.T) {
	var buf bytes.Buffer
	acc := stats.NewAccumulator(0)
	stop := newProgress(context.Background(), &buf, 80, runner.Config{Duration: time.Hour}, acc)
	stop()
	stop()
	if buf.Len() != 0 {
		t.Errorf("ничего не рисовалось, но что-то напечатано: %q", buf.String())
	}
}

// По Ctrl+C строка сама уходит на новую: сообщение сигнала ляжет ниже
// в любом порядке, без гонки за \r.
func TestProgressOnInterrupt(t *testing.T) {
	var buf bytes.Buffer
	acc := stats.NewAccumulator(0)
	ctx, cancel := context.WithCancel(context.Background())
	stop := newProgress(ctx, &buf, 80, runner.Config{Duration: time.Hour}, acc)
	cancel()
	// stop ждёт горутину; та обязана выйти по ctx.Done(), а не по done —
	// первая версия теста звала stop сразу, и select мог выбрать done
	// первым: тест плавал. Даём отмене дойти до select.
	time.Sleep(50 * time.Millisecond)
	stop()
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("после отмены строка не ушла на новую: %q", buf.String())
	}
}
