package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
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
