package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/prom"
)

func assertPromFormat(t *testing.T, doc string) {
	t.Helper()
	if bad, ok := prom.Valid(doc); !ok {
		t.Errorf("строка не по формату экспозиции: %q", bad)
	}
}

func TestPromOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	code, stdout, _ := capture(t, "-n", "30", "-c", "3", "-o", "prom", srv.URL)
	if code != exitOK {
		t.Fatalf("код %d, ожидался %d", code, exitOK)
	}
	assertPromFormat(t, stdout)
	if !strings.Contains(stdout, `loadgen_requests_total{outcome="ok"} 30`) {
		t.Errorf("30 успешных не видно:\n%s", stdout)
	}
	// Ни строки шапки на stdout: документ обязан парситься целиком.
	if strings.Contains(stdout, "Запуск") {
		t.Error("шапка попала в prom-вывод")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	// Сервер медленный, чтобы прогон точно шёл, пока мы скребём.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
	}))
	defer srv.Close()

	// stderr печатает фактический адрес — по нему и найдём порт при «:0».
	l := launch(t, "-z", "2s", "-c", "4", "-metrics", "127.0.0.1:0", srv.URL)

	addr := waitForAddr(t, l.stderrLive)
	first := scrape(t, addr)
	time.Sleep(300 * time.Millisecond)
	second := scrape(t, addr)

	if first.contentType != prom.ContentType {
		t.Errorf("Content-Type %q, ожидался %q", first.contentType, prom.ContentType)
	}
	assertPromFormat(t, first.body)
	if second.ok <= first.ok {
		t.Errorf("счётчик не растёт по ходу прогона: %d → %d", first.ok, second.ok)
	}

	code := <-l.code
	<-l.stdout
	<-l.stderr
	if code != exitOK {
		t.Errorf("код %d, ожидался %d", code, exitOK)
	}
	// После прогона порт закрыт: сервер живёт ровно столько, сколько прогон.
	if resp, err := http.Get("http://" + addr + "/metrics"); err == nil {
		resp.Body.Close()
		t.Error("/metrics отвечает после конца прогона")
	}
}

type scrapeResult struct {
	contentType string
	body        string
	ok          int
}

func scrape(t *testing.T, addr string) scrapeResult {
	t.Helper()
	resp, err := http.Get("http://" + addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	r := scrapeResult{contentType: resp.Header.Get("Content-Type"), body: string(body)}
	if m := regexp.MustCompile(`loadgen_requests_total\{outcome="ok"\} (\d+)`).FindStringSubmatch(r.body); m != nil {
		r.ok, _ = strconv.Atoi(m[1])
	}
	return r
}

// waitForAddr читает stderr до строки «метрики: http://…/metrics».
func waitForAddr(t *testing.T, r io.Reader) string {
	t.Helper()
	buf := make([]byte, 4096)
	var acc string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		n, _ := r.Read(buf)
		acc += string(buf[:n])
		if m := regexp.MustCompile(`метрики: http://([^/\s]+)/metrics`).FindStringSubmatch(acc); m != nil {
			return m[1]
		}
	}
	t.Fatalf("адрес метрик не напечатан: %q", acc)
	return ""
}

func TestMetricsRefusals(t *testing.T) {
	t.Run("занятый порт — код 1 до старта", func(t *testing.T) {
		busy := httptest.NewServer(http.NotFoundHandler())
		defer busy.Close()
		addr := strings.TrimPrefix(busy.URL, "http://")

		code, stdout, stderr := capture(t, "-n", "5", "-metrics", addr, busy.URL)
		if code != exitUsage {
			t.Errorf("код %d, ожидался %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "-metrics") {
			t.Errorf("причина не названа: %s", stderr)
		}
		if strings.Contains(stdout, "Всего") {
			t.Error("прогон состоялся при занятом порте")
		}
	})

	t.Run("-metrics с -compare", func(t *testing.T) {
		code, _, _ := capture(t, "-compare", "-metrics", ":0", "a", "b")
		if code != exitUsage {
			t.Errorf("код %d, ожидался %d", code, exitUsage)
		}
	})
}
