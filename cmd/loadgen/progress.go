// Живая строка прогресса в stderr. Только если stderr — терминал: в пайпе
// и в CI её нет, stdout она не трогает. Без неё -z 30m — тридцать минут
// тишины для человека за терминалом; /metrics закрыл это лишь для того,
// кто смотрит через Prometheus.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"github.com/Baklachok/loadgen/internal/report"
	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/stats"
)

// startProgress — фасад для боевого stderr: решает, терминал ли это,
// и запускает строку. Возвращает stop, который её стирает; звать до Render,
// иначе отчёт ляжет поверх хвоста.
func startProgress(ctx context.Context, w *os.File, cfg runner.Config, acc *stats.Accumulator) (stop func()) {
	if !term.IsTerminal(int(w.Fd())) {
		return func() {}
	}
	// Ширину знает report.TerminalWidth — с тем же fallback; второй детект
	// здесь был бы копией.
	return newProgress(ctx, w, report.TerminalWidth(w, 80), cfg, acc)
}

// newProgress — то же без детекта терминала: тесты рисуют в буфер.
func newProgress(ctx context.Context, w io.Writer, width int, cfg runner.Config, acc *stats.Accumulator) (stop func()) {
	p := makeProgress(w, width, cfg, acc)
	p.wg.Add(1)
	go p.loop(ctx)

	var once sync.Once
	return func() {
		once.Do(func() {
			close(p.done)
			p.wg.Wait()
			p.erase()
		})
	}
}

// makeProgress — объект без горутины: единственная точка создания и для
// боевого newProgress, и для тестов, которые рисуют один кадр руками.
// Литерал progress{} в тестах разъехался бы с этим при первом новом поле.
func makeProgress(w io.Writer, width int, cfg runner.Config, acc *stats.Accumulator) *progress {
	return &progress{w: w, width: width, cfg: cfg, acc: acc, start: time.Now(), done: make(chan struct{})}
}

type progress struct {
	w     io.Writer
	width int
	cfg   runner.Config
	acc   *stats.Accumulator
	start time.Time
	done  chan struct{}
	wg    sync.WaitGroup
	last  int // длина последней нарисованной строки — столько и стирать
}

func (p *progress) loop(ctx context.Context) {
	defer p.wg.Done()
	t := time.NewTicker(time.Second)
	defer t.Stop()

	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			// Сигнал напечатает «остановка…» из своей горутины; порядок двух
			// строк в stderr без общего мьютекса не гарантирован. Не боремся:
			// свой \n — и сообщение сигнала ляжет ниже в любом порядке.
			p.erase()
			fmt.Fprintln(p.w)
			return
		case <-t.C:
			p.draw()
		}
	}
}

func (p *progress) draw() {
	s := p.acc.Snapshot()
	elapsed := time.Since(p.start)

	// RPS считает сама строка: в снимке его нет — окно измерения известно
	// только по итогу. То же решение, что у /metrics.
	rps := float64(s.Total) / max(elapsed.Seconds(), 0.001)

	// p99 только когда он достоверен, иначе прочерк — та же честность,
	// что в отчёте и JSON.
	p99 := "—"
	if s.Latency.Reliable(0.99) {
		p99 = s.Latency.P99.Round(100 * time.Microsecond).String()
	}

	line := fmt.Sprintf("  %s   %s   %.1f RPS   p99 %s   отказов %d",
		p.scope(elapsed), p.count(s.Total), rps, p99, s.Failed+s.Truncated)

	// Перенос ломает \r — режем справа под ширину терминала. Длина в рунах,
	// не в байтах: кириллица в строке есть.
	runes := []rune(line)
	if len(runes) > p.width-1 {
		runes = runes[:p.width-1]
	}

	pad := ""
	if n := p.last - len(runes); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	fmt.Fprint(p.w, "\r", string(runes), pad)
	p.last = len(runes)
}

// scope — «12s / 30m» в режиме -z, «12s» в режиме -n: там предел по числу.
func (p *progress) scope(elapsed time.Duration) string {
	e := elapsed.Round(time.Second)
	if p.cfg.Duration > 0 {
		return fmt.Sprintf("%v / %v", e, p.cfg.Duration)
	}
	return e.String()
}

// count — «6 012 / 10 000 запросов» в режиме -n, «6 012 запросов» в -z.
func (p *progress) count(total int) string {
	if p.cfg.Requests > 0 {
		return fmt.Sprintf("%s / %s запросов", grouped(total), grouped(p.cfg.Requests))
	}
	return grouped(total) + " запросов"
}

// erase стирает нарисованное: \r, пробелы по длине последней строки, \r.
func (p *progress) erase() {
	if p.last == 0 {
		return
	}
	fmt.Fprint(p.w, "\r", strings.Repeat(" ", p.last), "\r")
	p.last = 0
}

// grouped — 6012 → «6 012»: на больших числах разряды читаются глазом,
// а не пересчитываются.
func grouped(n int) string {
	s := fmt.Sprint(n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}
