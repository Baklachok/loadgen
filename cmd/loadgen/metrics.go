// Эндпоинт /metrics на время прогона: окно в идущий прогон для Prometheus.
// Без него -z 30m — тридцать минут тишины, потому что отчёт печатается
// только в конце.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/Baklachok/loadgen/internal/prom"
	"github.com/Baklachok/loadgen/internal/stats"
)

// metricsServer живёт ровно столько, сколько идёт прогон.
type metricsServer struct {
	srv *http.Server
	ln  net.Listener
}

// listenMetrics занимает порт до старта прогона: занятый порт обязан дать
// код 1 сразу, а не после тридцати минут.
//
// Адреса по умолчанию нет намеренно: слушать порт без просьбы — не дело
// нагрузочника, у которого и так есть правило «не стрелять куда не просили».
func listenMetrics(addr string, acc *stats.Accumulator) (*metricsServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("-metrics: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		// Без version в Content-Type Prometheus документ примет, но предупредит.
		w.Header().Set("Content-Type", prom.ContentType)
		if err := prom.Write(w, acc.Snapshot()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})

	m := &metricsServer{
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
		ln:  ln,
	}
	go func() {
		if err := m.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Serve возвращает ошибку только если слушатель сломался под ногами;
			// прогон при этом продолжает, а отчёт напечатается как обычно.
			_ = err
		}
	}()
	return m, nil
}

// Addr — фактический адрес: при «:0» порт выбирает система.
func (m *metricsServer) Addr() string { return m.ln.Addr().String() }

// Close даёт последнему скрейпу дочитаться, но не задерживает отчёт:
// секунды хватает, чтобы дописать ответ, и мало, чтобы это заметить.
func (m *metricsServer) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = m.srv.Shutdown(ctx)
}
