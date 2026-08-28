// Фикстуры и рендереры, общие для текстовых и JSON-тестов пакета.
package report

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

func millis(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// render отдаёт весь текстовый отчёт строкой: тестам интересна не запись
// в io.Writer, а то, что в ней оказалось.

func render(s stats.Summary, opt Options) string {
	var buf bytes.Buffer
	if err := (textRenderer{}).Render(&buf, s, opt); err != nil {
		panic(err) // bytes.Buffer не умеет отказывать
	}
	return buf.String()
}

func renderJSON(t *testing.T, s stats.Summary, opt Options) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := (jsonRenderer{}).Render(&buf, s, opt); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sample() stats.Summary {
	return stats.Summary{
		Total: 100, OK: 90, NonOK: 8, Failed: 2,
		Elapsed: 2 * time.Second, RPS: 49,
		// Samples заведомо выше порога p99: тесты ниже про раскраску и
		// ширину, а не про достаточность выборки — для неё отдельные.
		Latency:   stats.Latencies{Samples: 2000, Min: millis(1), Mean: millis(5), Max: millis(90), P50: millis(4), P90: millis(9), P95: millis(30), P99: millis(80)},
		Corrected: stats.Latencies{Samples: 2000, Min: millis(1), Mean: millis(9), Max: millis(120), P50: millis(5), P90: millis(40), P95: millis(70), P99: millis(110)},
		MaxLag:    millis(30),
		Histogram: []stats.Bucket{
			{Upper: millis(10), Count: 80},
			{Upper: millis(20), Count: 3},
			{Upper: millis(120), Count: 15},
		},
		BytesRead: 4096, Throughput: 0.002,
		Codes:  map[int]int{200: 90, 404: 5, 503: 3},
		Errors: map[stats.ErrorKind]int{stats.ErrTimeout: 2},
	}
}

// traced — тот же прогон, но с -trace. Соединялись двое из тысячи,
// рукопожатия TLS не было вовсе: обычная картина по HTTP с keep-alive.

func traced() stats.Summary {
	s := sample()
	s.Trace = &stats.TraceSummary{
		Traced:  1000,
		Reused:  998,
		DNS:     stats.Latencies{Samples: 2, P50: millis(1), P99: millis(3), Max: millis(3)},
		Connect: stats.Latencies{Samples: 2, P50: millis(1), P99: millis(2), Max: millis(2)},
		TTFB:    stats.Latencies{Samples: 1000, P50: millis(6), P99: millis(400), Max: millis(450)},
	}
	return s
}

// decodeJSON разбирает отчёт в переданную структуру. Каждый тест ниже
// описывает только те поля, что проверяет, — а три строки на Unmarshal
// с t.Fatal повторялись при каждом.
func decodeJSON(t *testing.T, s stats.Summary, opt Options, into any) {
	t.Helper()

	raw := renderJSON(t, s, opt)
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("невалидный JSON: %v\n%s", err, raw)
	}
}
