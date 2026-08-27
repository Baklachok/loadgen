package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestRunNoLostResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := Config{
		URL:         srv.URL,
		Method:      http.MethodGet,
		Requests:    1000,
		Concurrency: 50,
		Timeout:     5 * time.Second,
	}

	for i := 0; i < 5; i++ {
		res, err := Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != cfg.Requests {
			t.Errorf("прогон %d: получено %d результатов, ожидалось %d", i, len(res), cfg.Requests)
		}
	}
}

func TestRunCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	cfg := Config{URL: srv.URL, Method: "GET", Requests: 100000, Concurrency: 10, Timeout: time.Second}

	start := time.Now()
	res, err := Run(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Run не остановился вовремя: %v", elapsed)
	}
	t.Logf("собрано %d результатов до отмены", len(res))
}

func TestDurationModeNoPhantomTimeouts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
	}))
	defer srv.Close()

	cfg := Config{
		URL:         srv.URL,
		Method:      "GET",
		Duration:    2 * time.Second,
		Concurrency: 20,
		Timeout:     5 * time.Second, // заведомо больше 20мс
	}

	results, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	for i, r := range results {
		if r.Err != nil {
			t.Errorf("результат %d: неожиданная ошибка %v", i, r.Err)
		}
	}
}

// В closed-loop частота определяется сервером: 20 воркеров по 100мс дают ~200 RPS.
// В open-loop она наша: сколько попросили, столько и уходит.
func TestOpenLoopHoldsRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	cfg := Config{
		URL:         srv.URL,
		Method:      "GET",
		Duration:    time.Second,
		Rate:        30,
		Concurrency: 20,
		Timeout:     5 * time.Second,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// closed-loop на тех же 20 воркерах выдал бы ~200 запросов
	if len(res) < 20 || len(res) > 45 {
		t.Errorf("выдано %d запросов, ожидалось ~30 (расписание 30 RPS за 1с)", len(res))
	}
	for i, r := range res {
		if r.Err != nil {
			t.Errorf("результат %d: неожиданная ошибка %v", i, r.Err)
		}
	}
}

// Если генератор не успевает за собственным расписанием, отставание должно
// осесть в Lag, а не исчезнуть — иначе получим coordinated omission.
func TestOpenLoopRecordsLag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	// 5 слотов по 200мс = потолок 25 RPS, а расписание просит 100
	cfg := Config{
		URL:         srv.URL,
		Method:      "GET",
		Duration:    time.Second,
		Rate:        100,
		Concurrency: 5,
		Timeout:     5 * time.Second,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
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
	// само время запроса при этом остаётся честными ~200мс — в этом и суть поправки
	if maxDur > time.Second {
		t.Errorf("макс. Duration = %v, ожидалось ~200мс", maxDur)
	}
}

func TestOpenLoopNoLagWhenKeepingUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	cfg := Config{
		URL:         srv.URL,
		Method:      "GET",
		Requests:    50,
		Rate:        100,
		Concurrency: 20,
		Timeout:     5 * time.Second,
	}

	res, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	cases := map[string]Config{
		"closed-loop": {URL: srv.URL, Method: "GET", Duration: 300 * time.Millisecond, Concurrency: 10, Timeout: time.Second},
		"open-loop":   {URL: srv.URL, Method: "GET", Duration: 300 * time.Millisecond, Rate: 200, Concurrency: 10, Timeout: time.Second},
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			if _, err := Run(context.Background(), cfg); err != nil {
				t.Fatal(err)
			}
			time.Sleep(300 * time.Millisecond) // дать транспорту закрыть простаивающие соединения
			if after := runtime.NumGoroutine(); after > before {
				t.Errorf("утечка горутин: было %d, стало %d", before, after)
			}
		})
	}
}
