package prom

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

var update = flag.Bool("update", false, "перезаписать golden-файлы")

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func sample() stats.Summary {
	return stats.Summary{
		Total: 100, OK: 90, NonOK: 8, Failed: 2, ClientErrors: 1, Late: 7,
		RPS: 49, TargetRate: 50, BytesRead: 4096, Window: 2 * time.Second,
		Latency:   stats.Latencies{Samples: 2000, Min: ms(1), Mean: ms(5), Max: ms(90), P50: ms(4), P90: ms(9), P95: ms(30), P99: ms(80)},
		Corrected: stats.Latencies{Samples: 2000, Min: ms(1), Mean: ms(9), Max: ms(120), P50: ms(5), P90: ms(40), P95: ms(70), P99: ms(110)},
		Codes:     map[int]int{200: 90, 404: 5, 503: 3},
		Errors:    map[stats.ErrorKind]int{stats.ErrTimeout: 2},
	}
}

func render(t *testing.T, s stats.Summary) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, s); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestFormat(t *testing.T) {
	if bad, ok := Valid(render(t, sample())); !ok {
		t.Errorf("строка не по формату экспозиции: %q", bad)
	}
}

// Сторож формата обязан ловить битые строки, иначе он ничего не сторожит:
// первая проверка поломкой это и показала — Valid, пропускающий всё,
// оставлял оба теста зелёными, потому что валидный документ и так валиден.
func TestValidRejects(t *testing.T) {
	for _, bad := range []string{
		`loadgen_x{kind=timeout} 1`, // метка без кавычек
		`loadgen_x{kind="a"} 1 2`,   // два значения
		`Loadgen_x 1`,               // заглавная в имени
		`loadgen_x{kind="a"}`,       // без значения
		`# HELP loadgen_x`,          // комментарий без текста
		`loadgen_x{kind="a"b"} 1`,   // незакрытая кавычка внутри
	} {
		if _, ok := Valid("loadgen_ok 1\n" + bad + "\n"); ok {
			t.Errorf("пропущена битая строка: %q", bad)
		}
	}
}

func TestGolden(t *testing.T) {
	got := render(t, sample())
	path := filepath.Join("testdata", "summary.golden.prom")

	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v\nсоздать: go test ./internal/prom -update", err)
	}
	if got != string(want) {
		t.Errorf("документ разошёлся с %s (обновить: -update)\n--- было\n%s\n--- стало\n%s", path, want, got)
	}
}

func TestWrite(t *testing.T) {
	t.Run("секунды, не миллисекунды", func(t *testing.T) {
		out := render(t, sample())
		if !strings.Contains(out, `loadgen_latency_seconds{quantile="0.99"} 0.08`) {
			t.Errorf("p99 80ms должен быть 0.08 секунды:\n%s", out)
		}
		if strings.Contains(out, "_ms") {
			t.Error("в именах метрик _ms — это провалит любой линтер Prometheus")
		}
	})

	// Число на месте недостоверного перцентиля было бы ложью, которую машина
	// не переспросит; отсутствие строки — не ложь.
	t.Run("недостоверный перцентиль не пишется, count пишется", func(t *testing.T) {
		s := sample()
		s.Latency.Samples = 50 // порог p99 — 1000
		out := render(t, s)
		if strings.Contains(out, `loadgen_latency_seconds{quantile="0.99"}`) {
			t.Error("p99 напечатан на 50 замерах")
		}
		if !strings.Contains(out, `loadgen_latency_seconds{quantile="0.5"}`) {
			t.Error("p50 пропал, хотя порог для него 20")
		}
		if !strings.Contains(out, "loadgen_latency_seconds_count 50") {
			t.Error("count обязан быть — он точный")
		}
	})

	t.Run("closed-loop без поправки и цели", func(t *testing.T) {
		s := sample()
		s.TargetRate = 0
		out := render(t, s)
		for _, forbidden := range []string{"loadgen_corrected_latency_seconds", "loadgen_target_rate"} {
			if strings.Contains(out, forbidden) {
				t.Errorf("%s напечатан в closed-loop: обещает расписание, которого не было", forbidden)
			}
		}
	})

	t.Run("метки экранируются", func(t *testing.T) {
		s := sample()
		s.Errors = map[stats.ErrorKind]int{`кавычка " и \ слэш`: 1}
		out := render(t, s)
		if !strings.Contains(out, `kind="кавычка \" и \\ слэш"`) {
			t.Errorf("метка не экранирована:\n%s", out)
		}
		if bad, ok := Valid(out); !ok {
			t.Errorf("после экранирования строка не по формату: %q", bad)
		}
	})

	t.Run("порядок ключей детерминирован", func(t *testing.T) {
		first, second := render(t, sample()), render(t, sample())
		if first != second {
			t.Error("два вызова дали разный документ — golden не завести")
		}
	})

	// Живой снимок не знает окна измерения — RPS в нём нулевой не потому,
	// что запросов нет, а потому, что делить не на что. Gauge с нулём врал бы;
	// Prometheus сам возьмёт rate() по счётчику. Это нашёл настоящий
	// Prometheus: loadgen_rps=0 при ok=763 по ходу прогона.
	t.Run("rps только по итогу, не в живом снимке", func(t *testing.T) {
		live := sample()
		live.Window = 0
		if strings.Contains(render(t, live), "loadgen_rps") {
			t.Error("rps напечатан без окна измерения")
		}
		if !strings.Contains(render(t, sample()), "loadgen_rps 49") {
			t.Error("rps пропал из итогового документа")
		}
	})

	t.Run("прерванный прогон", func(t *testing.T) {
		s := sample()
		s.Partial = true
		if !strings.Contains(render(t, s), "loadgen_partial 1") {
			t.Error("partial не отражён")
		}
	})
}
