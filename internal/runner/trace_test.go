package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// tracedConfig — прогон с включённой трассировкой.
func tracedConfig(srv *httptest.Server, n int) Config {
	cfg := requestsConfig(srv, n)
	cfg.Trace = true
	return cfg
}

func TestTraceDisabledByDefault(t *testing.T) {
	srv := sleepServer(t, 0)

	for i, r := range mustRun(t, context.Background(), requestsConfig(srv, 3)) {
		if r.Trace != nil {
			t.Errorf("результат %d: трассировка собрана без -trace", i)
		}
	}
}

// Главное свойство: при keep-alive соединение устанавливается один раз,
// остальные запросы берут его из пула. Значит фазы DNS/TCP/TLS есть ровно
// у первого, и усреднять их по всем запросам нельзя.
func TestTraceConnectionReuse(t *testing.T) {
	srv := sleepServer(t, 0)

	res := mustRun(t, context.Background(), tracedConfig(srv, 5))
	assertNoErrors(t, res)

	var connected, reused int
	for i, r := range res {
		if r.Trace == nil {
			t.Fatalf("результат %d: трассировка не собрана", i)
		}
		if r.Trace.TTFB <= 0 {
			t.Errorf("результат %d: TTFB = %v, ожидалось больше нуля", i, r.Trace.TTFB)
		}
		// httptest слушает на 127.0.0.1: резолвить нечего, DNS не выполняется
		if r.Trace.DNS != 0 {
			t.Errorf("результат %d: DNS = %v для IP-литерала", i, r.Trace.DNS)
		}

		if r.Trace.Reused {
			reused++
		}
		if r.Trace.Connect > 0 {
			connected++
		}
	}

	if connected != 1 {
		t.Errorf("TCP-соединений установлено %d, ожидалось 1: keep-alive не работает", connected)
	}
	if reused != len(res)-1 {
		t.Errorf("из пула взято %d соединений, ожидалось %d", reused, len(res)-1)
	}
}

func TestTraceRecordsTLSHandshake(t *testing.T) {
	srv := serveTLS(t, func(w http.ResponseWriter, r *http.Request) {})

	cfg := tracedConfig(srv, 2)
	cfg.Insecure = true // сертификат у httptest самоподписанный

	res := mustRun(t, context.Background(), cfg)
	assertNoErrors(t, res)

	var handshakes int
	for _, r := range res {
		if r.Trace.TLS > 0 {
			handshakes++
		}
	}
	if handshakes != 1 {
		t.Errorf("рукопожатий TLS %d, ожидалось 1 на всё соединение", handshakes)
	}
}

// Трассировка не должна ломать замер: Duration остаётся осмысленным,
// а TTFB — его частью, а не чем-то большим.
func TestTraceKeepsDurationSane(t *testing.T) {
	srv := sleepServer(t, 30*time.Millisecond)

	for i, r := range mustRun(t, context.Background(), tracedConfig(srv, 3)) {
		if r.Duration < 30*time.Millisecond {
			t.Errorf("результат %d: Duration = %v, сервер спал 30мс", i, r.Duration)
		}
		if r.Trace.TTFB > r.Duration {
			t.Errorf("результат %d: TTFB %v больше всего запроса %v", i, r.Trace.TTFB, r.Duration)
		}
	}
}
