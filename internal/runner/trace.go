package runner

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

// Trace — разбивка времени запроса по фазам соединения.
//
// Нулевая длительность означает «фазы не было», а не «прошла мгновенно»:
// при живом keep-alive соединение берётся из пула, и ни резолва, ни TCP,
// ни TLS-рукопожатия не выполняется. Поэтому усреднять фазы по всем
// запросам нельзя — статистика считает их только по тем, где они были.
type Trace struct {
	DNS     time.Duration // резолв имени
	Connect time.Duration // установка TCP-соединения
	TLS     time.Duration // рукопожатие TLS
	TTFB    time.Duration // от отправленного запроса до первого байта ответа
	Reused  bool          // соединение взято из пула
}

// tracer собирает тайминги по коллбэкам httptrace. Документация пакета прямо
// предупреждает, что коллбэки могут вызываться из разных горутин, поэтому
// всё под мьютексом, а не «и так последовательно».
type tracer struct {
	mu sync.Mutex

	dnsStart     time.Time
	connectStart time.Time
	tlsStart     time.Time
	requestSent  time.Time

	trace Trace
}

// begin и finish существуют, чтобы блокировка была написана дважды, а не
// девять раз: каждый коллбэк — это одна строчка про свою фазу.
func (t *tracer) begin(at *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	*at = time.Now()
}

func (t *tracer) finish(phase *time.Duration, at *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Нулевой старт означает, что парного Start-коллбэка не было: фазы
	// не случилось, а не «длилась от начала эпохи».
	if at.IsZero() {
		return
	}
	*phase = time.Since(*at)
}

func (t *tracer) setReused(reused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.trace.Reused = reused
}

func (t *tracer) hooks() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.begin(&t.dnsStart) },
		DNSDone:  func(httptrace.DNSDoneInfo) { t.finish(&t.trace.DNS, &t.dnsStart) },

		ConnectStart: func(network, addr string) { t.begin(&t.connectStart) },
		ConnectDone:  func(network, addr string, err error) { t.finish(&t.trace.Connect, &t.connectStart) },

		TLSHandshakeStart: func() { t.begin(&t.tlsStart) },
		TLSHandshakeDone:  func(tls.ConnectionState, error) { t.finish(&t.trace.TLS, &t.tlsStart) },

		GotConn: func(info httptrace.GotConnInfo) { t.setReused(info.Reused) },

		WroteRequest:         func(httptrace.WroteRequestInfo) { t.begin(&t.requestSent) },
		GotFirstResponseByte: func() { t.finish(&t.trace.TTFB, &t.requestSent) },
	}
}

// snapshot терпит nil-приёмник: так вызывающему не нужна ветка на
// «трассировка выключена» в каждой из трёх точек возврата.
func (t *tracer) snapshot() *Trace {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	trace := t.trace
	return &trace
}
