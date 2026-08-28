package report

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

var update = flag.Bool("update", false, "перезаписать golden-файлы")

// gomaxprocs зависит от машины: на CI ядер не восемь. Подменяем значение
// в сыром JSON, а не пересобираем документ через map, — иначе потеряется
// порядок ключей, ради которого golden и заводится.
var gomaxprocs = regexp.MustCompile(`"gomaxprocs": \d+`)

// Golden-файл ловит то, чего типизированные тесты не видят по устройству:
// они читают поля, которые сами и назвали, а неизвестные молча игнорируют.
// Добавленное поле, переименованное поле, съехавший порядок ключей — всё это
// проходит мимо них. Порядок здесь контракт: "schema" обязан идти первым,
// чтобы чужой парсер мог решить, понимает ли он документ, не разбирая целиком.
func TestJSONGolden(t *testing.T) {
	opt := Options{
		OpenLoop: true,
		Run: RunInfo{
			Version:   "v0.2.0",
			Proto:     "HTTP/1.1",
			StartedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
			Config: runner.Config{
				URL: "http://localhost:8080/", Method: "GET",
				Requests: 1000, Concurrency: 50, Rate: 500,
				Timeout: 10 * time.Second,
			},
		},
	}

	// Фикстура нарочно с дробными миллисекундами. На целых значениях ms()
	// неотличима от деления на time.Millisecond, и любая порча округления
	// прошла бы мимо: проверено — golden на прежней фикстуре её не заметил.
	// 1234567ns это 1.235ms после округления до микросекунды.
	s := traced()
	s.Latency.Min = 1234567 * time.Nanosecond
	s.Latency.Mean = 5500 * time.Microsecond
	s.Corrected.P99 = 110_499 * time.Microsecond

	got := gomaxprocs.ReplaceAll(renderJSON(t, s, opt), []byte(`"gomaxprocs": 8`))
	path := filepath.Join("testdata", "summary.golden.json")

	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("обновлён %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v\nсоздать: go test ./internal/report -update", err)
	}
	if string(got) != string(want) {
		t.Errorf("документ разошёлся с %s.\nЕсли изменение осознанное: go test ./internal/report -update\n\n--- было\n%s\n--- стало\n%s",
			path, want, got)
	}
}
