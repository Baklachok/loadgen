package stats

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

// PhaseStats — перцентили одной фазы соединения и число запросов, которые
// её реально выполняли. Count важен не меньше самих перцентилей: при живом
// keep-alive резолв и рукопожатие делает только первый запрос на соединение,
// и «p50 DNS» по трём замерам из десяти тысяч — совсем другое утверждение,
// чем по всем десяти тысячам.
type PhaseStats struct {
	Latencies
	Count int
}

// TraceSummary — разбивка времени запроса по фазам соединения.
type TraceSummary struct {
	DNS     PhaseStats
	Connect PhaseStats
	TLS     PhaseStats
	TTFB    PhaseStats

	Traced int // запросов с собранной трассировкой
	Reused int // из них получили соединение из пула
}

// phaseAcc — те же samples, но с одним правилом сверху: нулевые замеры
// отбрасываются. Ноль здесь означает, что фазы не было вовсе, и в
// перцентилях ему делать нечего.
type phaseAcc struct {
	samples
}

func (a *phaseAcc) add(d time.Duration) {
	if d <= 0 {
		return
	}
	a.samples.add(d)
}

func (a *phaseAcc) stats() PhaseStats {
	return PhaseStats{Latencies: a.latencies(), Count: len(a.values)}
}

// computeTrace возвращает nil, если трассировка была выключена: отчёту нужно
// отличать «фаз не было» от «не измеряли».
func computeTrace(results []runner.Result) *TraceSummary {
	var dns, connect, handshake, ttfb phaseAcc
	summary := &TraceSummary{}

	for _, r := range results {
		if r.Trace == nil {
			continue
		}

		summary.Traced++
		if r.Trace.Reused {
			summary.Reused++
		}

		dns.add(r.Trace.DNS)
		connect.add(r.Trace.Connect)
		handshake.add(r.Trace.TLS)
		ttfb.add(r.Trace.TTFB)
	}

	if summary.Traced == 0 {
		return nil
	}

	summary.DNS = dns.stats()
	summary.Connect = connect.stats()
	summary.TLS = handshake.stats()
	summary.TTFB = ttfb.stats()
	return summary
}
