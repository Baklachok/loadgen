package main

import (
	"flag"
	"io"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

func TestWarmupFlagParsing(t *testing.T) {
	tests := []struct {
		in           string
		wantRequests int
		wantDuration time.Duration
		wantErr      bool
	}{
		{in: "5s", wantDuration: 5 * time.Second},
		{in: "1m30s", wantDuration: 90 * time.Second},
		{in: "100", wantRequests: 100},
		{in: "0", wantRequests: 0},
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
		{in: "5 s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var f warmupFlag
			err := f.Set(tt.in)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Set(%q) прошёл, ожидалась ошибка", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tt.in, err)
			}
			if f.requests != tt.wantRequests || f.duration != tt.wantDuration {
				t.Errorf("Set(%q) → requests=%d duration=%v, ожидалось %d и %v",
					tt.in, f.requests, f.duration, tt.wantRequests, tt.wantDuration)
			}
		})
	}
}

// Повторный флаг должен вести себя как обычный: побеждает последний,
// а не складывается с предыдущим.
func TestWarmupFlagLastWins(t *testing.T) {
	var f warmupFlag

	if err := f.Set("5s"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("100"); err != nil {
		t.Fatal(err)
	}

	if f.duration != 0 {
		t.Errorf("duration = %v, ожидался 0: форму перетёрли числом", f.duration)
	}
	if f.requests != 100 {
		t.Errorf("requests = %d, ожидалось 100", f.requests)
	}
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

// Правила, которые Validate увидеть не может: он смотрит на значения,
// а «задан ли флаг» в значении не отражается.
func TestConfigRejectsContradictions(t *testing.T) {
	const url = "http://localhost:8080/"

	// Тело с методом GET законно по HTTP, но человек, пишущий -d, имеет
	// в виду POST. Промолчать здесь — дать ему не тот прогон.
	t.Run("-d без -m", func(t *testing.T) {
		if _, err := parseConfig(t, "-d", `{"a":1}`, url); err == nil {
			t.Fatal("тело без -m принято: уйдёт методом GET")
		}
		if _, err := parseConfig(t, "-m", "POST", "-d", `{"a":1}`, url); err != nil {
			t.Errorf("-m POST вместе с -d отвергнут: %v", err)
		}
	})

	t.Run("-n вместе с -z", func(t *testing.T) {
		if _, err := parseConfig(t, "-n", "200", "-z", "5s", url); err == nil {
			t.Fatal("-n и -z приняты вместе")
		}
	})

	// Явное «-c 50 -n 10» — противоречие, сказанное вслух, и отвергается.
	// То же самое из умолчания -c человек не говорил, и ошибка была бы
	// хамством: -c подгоняется, чтобы шапка не врала про пятьдесят потоков.
	t.Run("-c больше -n", func(t *testing.T) {
		if _, err := parseConfig(t, "-n", "10", "-c", "50", url); err == nil {
			t.Fatal("явное -c больше -n принято")
		}

		cfg, err := parseConfig(t, "-n", "10", url)
		if err != nil {
			t.Fatalf("умолчание -c отвергнуто на -n 10: %v", err)
		}
		if cfg.Concurrency != 10 {
			t.Errorf("потоков %d, ожидалось 10: шапка отчитается о том, чего не было", cfg.Concurrency)
		}

		// В open-loop -c это потолок запросов в полёте, и он обязан быть
		// больше -n, иначе частоту не удержать.
		if _, err := parseConfig(t, "-n", "10", "-c", "50", "-rate", "500", url); err != nil {
			t.Errorf("open-loop с потолком выше -n отвергнут: %v", err)
		}
	})
}

// Методы в HTTP регистрозависимы, и все зарегистрированные записаны в верхнем.
// «-m get» уходило дословно и получало 501 — но только от настоящего сервера:
// Go отдаёт запрос хендлеру с любым методом, поэтому локально это выглядело
// рабочим и ломалось на стенде.
func TestConfigCanonicalisesMethod(t *testing.T) {
	const url = "http://localhost:8080/"

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"нижний регистр", []string{"-m", "get"}, "GET"},
		{"смешанный", []string{"-m", "Post"}, "POST"},
		// Нестандартные методы тоже: PROPFIND и MKCOL записаны в верхнем
		// ровно так же, а опечатку в них сделать проще всего.
		{"нестандартный", []string{"-m", "propfind"}, "PROPFIND"},
		{"уже канонический не портится", []string{"-m", "DELETE"}, "DELETE"},
		{"умолчание", nil, "GET"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseConfig(t, append(tt.args, url)...)
			if err != nil {
				t.Fatalf("конфиг отвергнут: %v", err)
			}
			if cfg.Method != tt.want {
				t.Errorf("метод %q, ожидался %q", cfg.Method, tt.want)
			}
		})
	}

	// Приведение регистра не должно чинить то, что чинить нельзя: пробел
	// внутри метода остаётся, и запрос обрывается до отправки — это
	// проверяет TestInvalidMethodFailsBeforeAnyRequest в runner.
	t.Run("невалидный метод не чинится", func(t *testing.T) {
		cfg, err := parseConfig(t, "-m", "PO ST", url)
		if err != nil {
			t.Fatalf("конфиг отвергнут раньше времени: %v", err)
		}
		if cfg.Method != "PO ST" {
			t.Errorf("метод %q: приведение регистра подменило невалидный метод", cfg.Method)
		}
	})
}
