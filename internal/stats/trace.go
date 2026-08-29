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
// PhaseStats — это Latencies и есть: число замеров теперь живёт в них самих,
// и отдельный Count только дублировал бы его, готовый разойтись при правке.
type PhaseStats = Latencies

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
	d distribution
}

func (a *phaseAcc) add(d time.Duration) {
	if d <= 0 {
		return
	}
	if a.d == nil {
		a.d = newDistribution()
	}
	a.d.add(d)
}

// stats на пустой фазе отдаёт нули — «замеров не было», как и раньше.
func (a *phaseAcc) stats() PhaseStats {
	if a.d == nil {
		return PhaseStats{}
	}
	return a.d.latencies()
}

// traceAcc копит фазы соединения по всем запросам. Отдельный тип, а не шесть
// полей в Accumulator: у них одна общая задача, и методы, которые их читают,
// живут здесь же.
type traceAcc struct {
	dns, connect, handshake, ttfb phaseAcc
	traced, reused                int
}

// add учитывает фазы одного запроса. Оборванные ответы сюда входят наравне
// с целыми: DNS, TCP и TLS на них состоялись. Терпит nil — так вызывающему
// не нужна ветка на «трассировка выключена».
func (a *traceAcc) add(t *runner.Trace) {
	if t == nil {
		return
	}

	a.traced++
	if t.Reused {
		a.reused++
	}

	a.dns.add(t.DNS)
	a.connect.add(t.Connect)
	a.handshake.add(t.TLS)
	a.ttfb.add(t.TTFB)
}

// summary возвращает nil, если трассировка была выключена: отчёту нужно
// отличать «фаз не было» от «не измеряли».
func (a *traceAcc) summary() *TraceSummary {
	if a.traced == 0 {
		return nil
	}

	return &TraceSummary{
		Traced:  a.traced,
		Reused:  a.reused,
		DNS:     a.dns.stats(),
		Connect: a.connect.stats(),
		TLS:     a.handshake.stats(),
		TTFB:    a.ttfb.stats(),
	}
}
