package runner

import (
	"context"
	"net/http"
	"runtime"
	"testing"
	"time"
)

func TestRunNoLostResults(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

	cfg := requestsConfig(srv, 1000)
	cfg.Concurrency = 50

	// Пять прогонов подряд: потеря результата на отмене плавающая,
	// один прогон её может не поймать.
	for i := 0; i < 5; i++ {
		if res := mustRun(t, context.Background(), cfg); len(res) != cfg.Requests {
			t.Errorf("прогон %d: получено %d результатов, ожидалось %d", i, len(res), cfg.Requests)
		}
	}
}

func TestRunCancellation(t *testing.T) {
	srv := sleepServer(t, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cfg := requestsConfig(srv, 100000)
	cfg.Concurrency = 10
	cfg.Timeout = time.Second

	start := time.Now()
	res := mustRun(t, ctx, cfg)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Run не остановился вовремя: %v", elapsed)
	}
	t.Logf("собрано %d результатов до отмены", len(res))
}

// Дедлайн прогона не должен рубить запросы в полёте: иначе они попадут
// в статистику как таймауты, которых сервер не допускал.
func TestDurationModeNoPhantomTimeouts(t *testing.T) {
	srv := sleepServer(t, 20*time.Millisecond)

	cfg := durationConfig(srv, 2*time.Second)
	cfg.Concurrency = 20

	assertNoErrors(t, mustRun(t, context.Background(), cfg))
}

// В closed-loop частота определяется сервером: 20 воркеров по 100мс дают ~200 RPS.
// В open-loop она наша: сколько попросили, столько и уходит.
func TestOpenLoopHoldsRate(t *testing.T) {
	srv := sleepServer(t, 100*time.Millisecond)

	cfg := durationConfig(srv, time.Second)
	cfg.Rate = 30
	cfg.Concurrency = 20

	res := mustRun(t, context.Background(), cfg)

	// closed-loop на тех же 20 воркерах выдал бы ~200 запросов
	if len(res) < 20 || len(res) > 45 {
		t.Errorf("выдано %d запросов, ожидалось ~30 (расписание 30 RPS за 1с)", len(res))
	}
	assertNoErrors(t, res)
}

// Если генератор не успевает за собственным расписанием, отставание должно
// осесть в Lag, а не исчезнуть — иначе получим coordinated omission.
func TestOpenLoopRecordsLag(t *testing.T) {
	srv := sleepServer(t, 200*time.Millisecond)

	// 5 слотов по 200мс = потолок 25 RPS, а расписание просит 100
	cfg := durationConfig(srv, time.Second)
	cfg.Rate = 100
	cfg.Concurrency = 5

	res := mustRun(t, context.Background(), cfg)
	if len(res) == 0 {
		t.Fatal("нет результатов")
	}

	var maxLag, maxDur time.Duration
	for _, r := range res {
		maxLag = max(maxLag, r.Lag)
		maxDur = max(maxDur, r.Duration)
	}

	if maxLag < 300*time.Millisecond {
		t.Errorf("макс. Lag = %v, ожидалось > 300мс: отставание не зафиксировано", maxLag)
	}
	// время самого запроса при этом остаётся честными ~200мс — в этом и суть поправки
	if maxDur > time.Second {
		t.Errorf("макс. Duration = %v, ожидалось ~200мс", maxDur)
	}
}

func TestOpenLoopNoLagWhenKeepingUp(t *testing.T) {
	srv := sleepServer(t, 0)

	cfg := requestsConfig(srv, 50)
	cfg.Rate = 100
	cfg.Concurrency = 20

	res := mustRun(t, context.Background(), cfg)
	if len(res) != cfg.Requests {
		t.Fatalf("получено %d результатов, ожидалось %d", len(res), cfg.Requests)
	}

	for i, r := range res {
		if r.Lag > 50*time.Millisecond {
			t.Errorf("результат %d: Lag = %v, генератор укладывался в расписание", i, r.Lag)
		}
	}
}

func TestNoGoroutineLeak(t *testing.T) {
	srv := sleepServer(t, 0)

	openLoopCfg := durationConfig(srv, 300*time.Millisecond)
	openLoopCfg.Rate = 200
	openLoopCfg.Concurrency = 10

	closedLoopCfg := durationConfig(srv, 300*time.Millisecond)
	closedLoopCfg.Concurrency = 10

	for name, cfg := range map[string]Config{"closed-loop": closedLoopCfg, "open-loop": openLoopCfg} {
		t.Run(name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			mustRun(t, context.Background(), cfg)

			time.Sleep(300 * time.Millisecond) // дать транспорту закрыть простаивающие соединения
			if after := runtime.NumGoroutine(); after > before {
				t.Errorf("утечка горутин: было %d, стало %d", before, after)
			}
		})
	}
}
