// Фикстуры и рендереры, общие для текстовых и JSON-тестов пакета.
package report

import (
	"bytes"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

func millis(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// render отдаёт весь текстовый отчёт строкой: тестам интересна не запись
// в io.Writer, а то, что в ней оказалось.

func render(s stats.Summary, opt Options) string {
	var buf bytes.Buffer
	Text(&buf, s, opt)
	return buf.String()
}

func renderJSON(t *testing.T, s stats.Summary, opt Options) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := JSON(&buf, s, opt); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sample() stats.Summary {
	return stats.Summary{
		Total: 100, OK: 90, NonOK: 8, Failed: 2,
		Elapsed: 2 * time.Second, RPS: 49,
		Latency:   stats.Latencies{Min: millis(1), Mean: millis(5), Max: millis(90), P50: millis(4), P90: millis(9), P95: millis(30), P99: millis(80)},
		Corrected: stats.Latencies{Min: millis(1), Mean: millis(9), Max: millis(120), P50: millis(5), P90: millis(40), P95: millis(70), P99: millis(110)},
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
		DNS:     stats.PhaseStats{Count: 2, Latencies: stats.Latencies{P50: millis(1), P99: millis(3), Max: millis(3)}},
		Connect: stats.PhaseStats{Count: 2, Latencies: stats.Latencies{P50: millis(1), P99: millis(2), Max: millis(2)}},
		TTFB:    stats.PhaseStats{Count: 1000, Latencies: stats.Latencies{P50: millis(6), P99: millis(400), Max: millis(450)}},
	}
	return s
}
