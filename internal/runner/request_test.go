package runner

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
)

func TestRequestCarriesMethodBodyAndHeaders(t *testing.T) {
	var rec recorder
	srv := serve(t, rec.handler)

	cfg := requestsConfig(srv, 3)
	cfg.Method = http.MethodPost
	cfg.Body = []byte(`{"a":1}`)
	cfg.Headers = http.Header{
		"Content-Type": []string{"application/json"},
		"X-Token":      []string{"secret"},
	}

	mustRun(t, context.Background(), cfg)

	got := rec.captured()
	if len(got) != cfg.Requests {
		t.Fatalf("сервер принял %d запросов, ожидалось %d", len(got), cfg.Requests)
	}

	for i, r := range got {
		if r.Method != http.MethodPost {
			t.Errorf("запрос %d: метод %q, ожидался POST", i, r.Method)
		}
		// Тело обязано прийти в каждом запросе: один общий Reader отдал бы
		// его только первому, а остальным досталась бы пустота.
		if r.Body != `{"a":1}` {
			t.Errorf("запрос %d: тело %q, ожидалось {\"a\":1}", i, r.Body)
		}
		if v := r.Header.Get("X-Token"); v != "secret" {
			t.Errorf("запрос %d: X-Token = %q", i, v)
		}
		if v := r.Header.Get("Content-Type"); v != "application/json" {
			t.Errorf("запрос %d: Content-Type = %q", i, v)
		}
	}
}

// Заголовок Host особый: net/http берёт его из req.Host, а не из карты
// заголовков. Подмена Host — обычный приём при тестировании вирт-хостов
// за балансировщиком, и молча терять его нельзя.
func TestHostHeaderReachesServer(t *testing.T) {
	var rec recorder
	srv := serve(t, rec.handler)

	cfg := requestsConfig(srv, 1)
	cfg.Headers = http.Header{"Host": []string{"api.internal"}}

	mustRun(t, context.Background(), cfg)

	got := rec.captured()
	if len(got) != 1 {
		t.Fatalf("сервер принял %d запросов, ожидался 1", len(got))
	}
	if got[0].Host != "api.internal" {
		t.Errorf("Host = %q, ожидался api.internal", got[0].Host)
	}
}

// Запрос собирается один раз на весь прогон, поэтому кривой метод должен
// отвалиться до старта, а не превратиться в тысячи одинаковых ошибок.
func TestInvalidMethodFailsBeforeAnyRequest(t *testing.T) {
	var rec recorder
	srv := serve(t, rec.handler)

	cfg := requestsConfig(srv, 5000)
	cfg.Method = "PO ST"

	if _, err := Run(context.Background(), cfg, discard); err == nil {
		t.Fatal("ожидалась ошибка на некорректном методе")
	}
	if n := len(rec.captured()); n != 0 {
		t.Errorf("сервер получил %d запросов, ожидалось 0", n)
	}
}

func TestStatusCodesRecorded(t *testing.T) {
	codes := []int{200, 404, 500}

	var mu sync.Mutex
	var i int

	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		code := codes[i%len(codes)]
		i++
		mu.Unlock()

		w.WriteHeader(code)
	})

	res := mustRun(t, context.Background(), requestsConfig(srv, 9))
	assertNoErrors(t, res)

	seen := make(map[int]int)
	for _, r := range res {
		seen[r.StatusCode]++
	}
	for _, c := range codes {
		if seen[c] != 3 {
			t.Errorf("код %d встретился %d раз, ожидалось 3 (всего: %v)", c, seen[c], seen)
		}
	}
}

// Клиент намеренно не ходит по редиректам: мерить надо тот ответ, что отдал
// сервер, а не то, куда он перенаправил.
// Сервис, сбрасывающий нагрузку пятисотками и рвущий соединения, обязан
// отличаться от недоступного: код ответа — единственное, что их различает,
// и раньше он терялся вместе с оборванным телом.
func TestStatusCodeSurvivesTruncatedBody(t *testing.T) {
	// Content-Length обещает больше, чем будет отдано, после чего соединение
	// закрывается: заголовки с кодом клиент получает, тело — нет.
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err) // не Fatal: чужая горутина
			return
		}
		defer conn.Close()

		fmt.Fprint(buf, "HTTP/1.1 503 Service Unavailable\r\nContent-Length: 100\r\n\r\n")
		buf.WriteString("частичное тело")
		buf.Flush()
	})

	res := mustRun(t, context.Background(), requestsConfig(srv, 5))

	for i, r := range res {
		if r.Err == nil {
			t.Errorf("результат %d: обрыв тела не дал ошибки", i)
		}
		if r.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("результат %d: код %d, ожидался 503 — иначе сервис неотличим от недоступного", i, r.StatusCode)
		}
		if r.BytesRead == 0 {
			t.Errorf("результат %d: байты до обрыва потеряны", i)
		}
	}
}

func TestRedirectsNotFollowed(t *testing.T) {
	var rec recorder

	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		rec.handler(w, r)
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusTeapot)
	})

	cfg := requestsConfig(srv, 1)
	cfg.URL = srv.URL + "/"

	res := mustRun(t, context.Background(), cfg)

	if len(res) != 1 || res[0].StatusCode != http.StatusFound {
		t.Errorf("получено %+v, ожидался один результат с кодом 302", res)
	}
	if n := len(rec.captured()); n != 1 {
		t.Errorf("сервер получил %d запросов — редирект всё-таки прошли", n)
	}
}
