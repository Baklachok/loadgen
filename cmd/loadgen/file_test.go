package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Baklachok/loadgen/internal/runner"
)

func yamlFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fromFile прогоняет файл через тот же путь, что run: applyFile до Parse.
func fromFile(t *testing.T, body string, args ...string) (runner.Config, error) {
	t.Helper()
	return parseConfig(t, append([]string{"-f", yamlFile(t, body)}, args...)...)
}

// Файл — это те же флаги: каждый ключ обязан дать ровно то, что дал бы флаг.
func TestFileEqualsFlags(t *testing.T) {
	const url = "http://localhost:8080/"

	tests := []struct {
		name string
		yaml string
		args []string
	}{
		{"n", "n: 300", []string{"-n", "300"}},
		{"z и длинный синоним", "duration: 5s", []string{"-z", "5s"}},
		{"c", "c: 7", []string{"-c", "7"}},
		{"method поднимается в регистр", "method: post", []string{"-m", "post"}},
		// Тело без метода отвергает матрица несовместимостей — она общая для
		// файла и флагов, поэтому method здесь обязателен с обеих сторон.
		{"body", "method: POST\nd: '{\"a\":1}'", []string{"-m", "POST", "-d", `{"a":1}`}},
		{"timeout", "timeout: 3s", []string{"-t", "3s"}},
		{"rate", "rate: 250", []string{"-rate", "250"}},
		{"warmup по числу", "warmup: 10", []string{"-warmup", "10"}},
		{"warmup по времени", "warmup: 2s", []string{"-warmup", "2s"}},
		{"trace", "trace: true", []string{"-trace"}},
		{"список заголовков", "headers:\n  - \"X-A: 1\"\n  - \"X-B: 2\"", []string{"-H", "X-A: 1", "-H", "X-B: 2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := fromFile(t, tt.yaml+"\n", url)
			if err != nil {
				t.Fatalf("из файла: %v", err)
			}
			want, err := parseConfig(t, append(tt.args, url)...)
			if err != nil {
				t.Fatalf("из флагов: %v", err)
			}
			if !sameConfig(got, want) {
				t.Errorf("файл дал %+v\nфлаги дали %+v", got, want)
			}
		})
	}
}

func sameConfig(a, b runner.Config) bool {
	// Body — слайс, Headers — карта: reflect.DeepEqual тут честнее, чем ==,
	// а user-agent проставляется обоим одинаково.
	return a.URL == b.URL && a.Method == b.Method && string(a.Body) == string(b.Body) &&
		a.Requests == b.Requests && a.Duration == b.Duration && a.Concurrency == b.Concurrency &&
		a.Timeout == b.Timeout && a.Rate == b.Rate && a.Trace == b.Trace &&
		a.WarmupRequests == b.WarmupRequests && a.WarmupDuration == b.WarmupDuration &&
		len(a.Headers) == len(b.Headers) && a.Headers.Get("X-A") == b.Headers.Get("X-A")
}

// Три правила config() смотрят, задан ли флаг явно. Файл обязан выглядеть
// для них как явно заданный — иначе они молча перестают работать.
func TestFileValuesCountAsExplicit(t *testing.T) {
	const url = "http://localhost:8080/"

	t.Run("n и z в файле — противоречие", func(t *testing.T) {
		if _, err := fromFile(t, "n: 100\nz: 5s\n", url); err == nil {
			t.Error("n вместе с z из файла приняты")
		}
	})

	t.Run("c из файла — явное, не подгоняется", func(t *testing.T) {
		if _, err := fromFile(t, "n: 10\nc: 50\n", url); err == nil {
			t.Error("c: 50 при n: 10 из файла принято — правило «явное противоречие» не сработало")
		}
		cfg, err := fromFile(t, "n: 10\n", url)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Concurrency != 10 {
			t.Errorf("умолчание c не подогнано под n: %d", cfg.Concurrency)
		}
	})

	// «slo-error-rate: 0» — это «ни одной ошибки», а не «порог не задан».
	// Отличить одно от другого может только FlagSet, и только если Set был.
	t.Run("slo-error-rate 0 из файла — порог задан", func(t *testing.T) {
		fs, f := newTestFlags(t)
		if _, err := applyFile(fs, yamlFile(t, "slo-error-rate: 0\n")); err != nil {
			t.Fatal(err)
		}
		if thr := f.slo(fs); thr.ErrorRate != 0 {
			t.Errorf("ErrorRate = %v, ожидался 0 (задан); -1 значило бы «не задан»", thr.ErrorRate)
		}
	})
}

func TestFilePrecedence(t *testing.T) {
	const url = "http://localhost:8080/"

	t.Run("флаг перекрывает файл", func(t *testing.T) {
		cfg, err := fromFile(t, "rate: 500\n", "-rate", "1000", url)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Rate != 1000 {
			t.Errorf("rate = %v, ожидалось 1000 из флага", cfg.Rate)
		}
	})

	t.Run("url из файла, аргумент перекрывает", func(t *testing.T) {
		code, stdout, _ := capture(t, "-f", yamlFile(t, "url: http://example.invalid:9/\nn: 1\nt: 100ms\n"))
		if code == exitUsage || !strings.Contains(stdout, "example.invalid") {
			t.Errorf("url из файла не подставлен: код %d\n%s", code, stdout)
		}
		_, stdout, _ = capture(t, "-f", yamlFile(t, "url: http://example.invalid:9/\nn: 1\nt: 100ms\n"), "http://other.invalid:9/")
		if !strings.Contains(stdout, "other.invalid") {
			t.Errorf("аргумент не перекрыл url из файла:\n%s", stdout)
		}
	})
}

// Один и тот же флаг в файле и в строке — не конфликт: строка побеждает
// молча. Раньше файл шёл до Parse и этот случай не различал; теперь Visit
// его пропускает, и это надо закрепить, а не предполагать.
func TestFileAndFlagAgree(t *testing.T) {
	cfg, err := fromFile(t, "rate: 500\nc: 20\n", "-rate", "500", "http://localhost:8080/")
	if err != nil {
		t.Fatalf("одинаковое значение в файле и флаге отвергнуто: %v", err)
	}
	if cfg.Rate != 500 || cfg.Concurrency != 20 {
		t.Errorf("rate=%v c=%d, ожидалось 500 и 20", cfg.Rate, cfg.Concurrency)
	}
}

func TestFileRefusals(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{"неизвестный ключ", []string{"-f", yamlFile(t, "bogus: 1\n"), "http://x/"}, "bogus"},
		{"битый YAML", []string{"-f", yamlFile(t, "n: [\n"), "http://x/"}, "-f"},
		{"файла нет", []string{"-f", filepath.Join(t.TempDir(), "нет.yaml"), "http://x/"}, "-f"},
		{"-f с -compare", []string{"-compare", "-f", yamlFile(t, "n: 1\n"), "a", "b"}, "-compare"},
		// YAML «.inf»/«.nan» через fmt.Sprint дают «+Inf»/«NaN» — тот же Set,
		// то же правило.
		{"rate: .inf в файле", []string{"-f", yamlFile(t, "rate: .inf\n"), "http://x/"}, "rate"},
		{"slo-error-rate: .nan в файле", []string{"-f", yamlFile(t, "slo-error-rate: .nan\n"), "http://x/"}, "slo-error-rate"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := capture(t, tt.args...)
			if code != exitUsage {
				t.Errorf("код %d, ожидался %d", code, exitUsage)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("причина не названа (%q):\n%s", tt.want, stderr)
			}
		})
	}
}
