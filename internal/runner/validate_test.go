package runner

import (
	"math"
	"strings"
	"testing"
	"time"
)

// valid — конфиг, который Validate принимает. Каждый случай ниже портит
// в нём ровно одно поле: так видно, что ошибку вызвало именно оно.
func valid() Config {
	return Config{
		URL:         "http://localhost:8080/",
		Method:      "GET",
		Requests:    100,
		Concurrency: 10,
		Timeout:     5 * time.Second,
	}
}

// Противоречивые флаги обязаны отвергаться на старте. Иначе прогон уходит
// в работу и разваливается на N одинаковых отказов — вместо одной строки,
// которую можно прочитать и исправить.
func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		fix  func(*Config)
		want string // подстрока сообщения; пусто — конфиг верный
	}{
		{"верный конфиг", func(*Config) {}, ""},

		{"URL не задан", func(c *Config) { c.URL = "" }, "URL не задан"},
		{"URL без схемы", func(c *Config) { c.URL = "localhost/api" }, "без схемы"},
		// url.Parse принимает это молча, считая "localhost" схемой:
		// самый коварный случай, потому что похоже на рабочий адрес.
		{"хост с портом без схемы", func(c *Config) { c.URL = "localhost:8080/api" }, "без схемы"},
		{"чужая схема", func(c *Config) { c.URL = "ftp://localhost/x" }, "без схемы"},
		// gRPC — граница проекта; ошибка обязана назвать его, а не отправить
		// дописывать http:// к gRPC-серверу.
		{"grpc — по имени, не «без схемы»", func(c *Config) { c.URL = "grpc://localhost:50051/pkg.Svc/M" }, "gRPC"},
		{"grpcs — тоже", func(c *Config) { c.URL = "grpcs://localhost:50051" }, "gRPC"},
		{"схема без хоста", func(c *Config) { c.URL = "http:///api" }, "нет хоста"},
		{"https принимается", func(c *Config) { c.URL = "https://example.com" }, ""},

		{"нет ни -n, ни -z", func(c *Config) { c.Requests = 0 }, "либо -n, либо -z"},
		{"-n вместе с -z", func(c *Config) { c.Duration = time.Second }, "взаимоисключающи"},
		{"только -z", func(c *Config) { c.Requests, c.Duration = 0, time.Second }, ""},

		{"потоков ноль", func(c *Config) { c.Concurrency = 0 }, "concurrency"},
		// Лишние потоки в closed-loop не сделают ни одного запроса: поток
		// берёт по запросу за раз, а запросы кончились.
		{"потоков больше, чем запросов", func(c *Config) { c.Concurrency = 200 }, "больше, чем запросов"},
		{"потоков ровно столько же", func(c *Config) { c.Concurrency = 100 }, ""},
		// В open-loop -c это потолок запросов в полёте, и он обязан быть
		// больше числа запросов, иначе частоту не удержать.
		{"в open-loop потолок выше не мешает", func(c *Config) { c.Concurrency, c.Rate = 200, 500 }, ""},
		{"в -z потолок выше не мешает", func(c *Config) {
			c.Requests, c.Duration, c.Concurrency = 0, time.Second, 200
		}, ""},

		{"таймаут ноль", func(c *Config) { c.Timeout = 0 }, "timeout"},
		{"отрицательная частота", func(c *Config) { c.Rate = -1 }, "отрицательн"},
		// Граница пакета: Config может прийти не из флагов, и «< 0» NaN/Inf
		// не ловит.
		{"частота NaN", func(c *Config) { c.Rate = math.NaN() }, "числом"},
		{"частота Inf", func(c *Config) { c.Rate = math.Inf(1) }, "числом"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid()
			tt.fix(&cfg)

			err := cfg.Validate()
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("верный конфиг отвергнут: %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("конфиг принят, ожидалась ошибка про %q", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Errorf("ошибка %q, ожидалось упоминание %q", err, tt.want)
			}
		})
	}
}
