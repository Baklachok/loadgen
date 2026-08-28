package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"
)

// serve поднимает настоящий сервер и сам его гасит. httptest вместо моков —
// тесты гоняют живой HTTP по петле, вместе с keep-alive и разбором заголовков.
func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// serveTLS — то же, но по HTTPS с самоподписанным сертификатом.
// Вызывающий обязан выставить Config.Insecure, иначе клиент его отвергнет.
func serveTLS(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// sleepServer отвечает пустым 200 после задержки.
func sleepServer(t *testing.T, d time.Duration) *httptest.Server {
	t.Helper()
	return serve(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(d) })
}

// capturedRequest — что долетело до сервера. Без этого флаги -m, -d и -H
// проверить нечем: клиент может молча их потерять, и тест не заметит.
type capturedRequest struct {
	Method string
	Body   string
	Header http.Header
	Host   string
}

type recorder struct {
	mu  sync.Mutex
	got []capturedRequest
}

func (rec *recorder) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)

	rec.mu.Lock()
	defer rec.mu.Unlock()
	rec.got = append(rec.got, capturedRequest{
		Method: r.Method,
		Body:   string(body),
		Header: r.Header.Clone(),
		Host:   r.Host,
	})
}

func (rec *recorder) captured() []capturedRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Clone(rec.got)
}

// baseConfig — умолчания, поверх которых тест меняет только то, что проверяет.
func baseConfig(srv *httptest.Server) Config {
	return Config{
		URL:         srv.URL,
		Method:      http.MethodGet,
		Concurrency: 1,
		Timeout:     5 * time.Second,
	}
}

// requestsConfig — прогон на фиксированное число запросов (режим -n).
func requestsConfig(srv *httptest.Server, n int) Config {
	cfg := baseConfig(srv)
	cfg.Requests = n
	return cfg
}

// durationConfig — прогон на время (режим -z). Requests остаётся нулём:
// с -n они взаимоисключающи, и Validate это проверяет.
func durationConfig(srv *httptest.Server, d time.Duration) Config {
	cfg := baseConfig(srv)
	cfg.Duration = d
	return cfg
}

// mustRun падает на ошибке конфигурации: тесты ниже проверяют поведение
// прогона, а разбор конфига проверяется отдельно.
func mustRun(t *testing.T, ctx context.Context, cfg Config) []Result {
	t.Helper()

	var out []Result
	mustRunSink(t, ctx, cfg, func(r Result) { out = append(out, r) })
	return out
}

// mustRunReport нужен тестам, которым важны не только замеры, но и окно,
// за которое они собраны.
func mustRunReport(t *testing.T, ctx context.Context, cfg Config) Report {
	t.Helper()
	return mustRunSink(t, ctx, cfg, discard)
}

func mustRunSink(t *testing.T, ctx context.Context, cfg Config, sink Sink) Report {
	t.Helper()

	rep, err := Run(ctx, cfg, sink)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// discard — sink для тестов, которым результаты не нужны. Прогон их больше
// нигде не копит, поэтому «никуда» приходится называть явно.
func discard(Result) {}

// assertNoErrors — ни один запрос не должен был провалиться.
func assertNoErrors(t *testing.T, res []Result) {
	t.Helper()

	for i, r := range res {
		if r.Err != nil {
			t.Errorf("результат %d: неожиданная ошибка %v", i, r.Err)
		}
	}
}
