// Package prom пишет сводку в текстовом формате экспозиции Prometheus.
//
// Отдельный пакет по тому же основанию, что slo и compare: он меняется, когда
// меняется набор экспонируемых метрик, — не когда меняется документ JSON
// или текстовый отчёт. Без client_golang: два десятка строк «имя{метки} число»
// пишутся Fprintf, а библиотека принесла бы protobuf и второй реестр со своими
// мьютексами поверх нашей синхронизации.
//
// Один и тот же Write обслуживает и /metrics по ходу прогона, и -o prom
// в конце: общее у них — набор метрик, разное — момент чтения.
package prom

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

// ContentType — то, что Prometheus ждёт в ответе на скрейп. Без version
// он примет документ, но предупредит.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Write пишет сводку. Единицы — секунды, не миллисекунды: у Prometheus это
// правило, и _ms в имени провалит любой линтер метрик.
func Write(w io.Writer, s stats.Summary) error {
	p := &printer{w: w}

	p.family("loadgen_requests_total", "counter", "Запросы по исходу.")
	p.line("loadgen_requests_total", s.OK, label{"outcome", "ok"})
	p.line("loadgen_requests_total", s.NonOK, label{"outcome", "non_2xx"})
	p.line("loadgen_requests_total", s.Failed, label{"outcome", "failed"})
	p.line("loadgen_requests_total", s.Truncated, label{"outcome", "truncated"})

	p.family("loadgen_client_errors_total", "counter", "Отказы из-за исчерпания ресурсов самого генератора.")
	p.line("loadgen_client_errors_total", s.ClientErrors)

	p.family("loadgen_late_total", "counter", "Запросы, ушедшие позже расписания больше чем на слот.")
	p.line("loadgen_late_total", s.Late)

	p.summary("loadgen_latency_seconds", "Время запроса.", s.Latency)
	// Поправка есть только в open-loop; в closed-loop поле Corrected
	// совпадает с Latency, и печатать его — обещать расписание, которого не было.
	if s.TargetRate > 0 {
		p.summary("loadgen_corrected_latency_seconds", "Время запроса с поправкой на расписание.", s.Corrected)
		p.family("loadgen_target_rate", "gauge", "Заданная частота open-loop.")
		p.line("loadgen_target_rate", s.TargetRate)
	}

	// RPS считается по окну измерения, а оно известно только по окончании:
	// в живом снимке его нет, и gauge с нулём врал бы. По ходу прогона
	// частоту берёт сам Prometheus — rate(loadgen_requests_total[1m]).
	if s.Window > 0 {
		p.family("loadgen_rps", "gauge", "Полученных ответов в секунду за окно измерения; только по итогу прогона.")
		p.line("loadgen_rps", s.RPS)
	}

	p.family("loadgen_bytes_read_total", "counter", "Прочитано байт тел ответов.")
	p.line("loadgen_bytes_read_total", s.BytesRead)

	p.family("loadgen_responses", "counter", "Ответы по HTTP-коду.")
	for _, code := range stats.SortedKeys(s.Codes) {
		p.line("loadgen_responses", s.Codes[code], label{"code", fmt.Sprint(code)})
	}

	p.family("loadgen_errors", "counter", "Отказы по причине.")
	for _, kind := range stats.SortedKeys(s.Errors) {
		p.line("loadgen_errors", s.Errors[kind], label{"kind", string(kind)})
	}

	p.family("loadgen_partial", "gauge", "1, если прогон прерван и цифры собраны не по всему плану.")
	p.line("loadgen_partial", boolInt(s.Partial))

	return p.err
}

// printer запоминает первую ошибку записи — тот же приём, что errWriter
// в report: три десятка Fprintf не должны тонуть в проверках.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) family(name, typ, help string) {
	p.printf("# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
}

// label — одна пара ключ/значение; больше одной метки на строку тут нет.
type label struct{ key, value string }

// line печатает выборку. Без меток — голая строка «имя число»; раньше это
// были два метода, line и plain, отличавшиеся одним форматом.
func (p *printer) line(name string, v any, labels ...label) {
	if len(labels) == 0 {
		p.printf("%s %v\n", name, num(v))
		return
	}
	l := labels[0]
	p.printf("%s{%s=\"%s\"} %v\n", name, l.key, escape(l.value), num(v))
}

// summary — тип summary Prometheus: квантили, сумма, число. Недостоверный
// перцентиль не пишется вовсе, как null в JSON: число там было бы ложью,
// которую машина не переспросит. sum и count пишутся всегда — они точные.
func (p *printer) summary(name, help string, l stats.Latencies) {
	p.family(name, "summary", help)
	for _, q := range stats.Quantiles {
		if !l.Reliable(q.Q) {
			continue
		}
		p.printf("%s{quantile=\"%g\"} %v\n", name, q.Q, seconds(q.Value(l)))
	}
	p.printf("%s_sum %v\n", name, seconds(l.Mean*time.Duration(l.Samples)))
	p.printf("%s_count %d\n", name, l.Samples)
}

func (p *printer) printf(format string, a ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, a...)
}

func seconds(d time.Duration) float64 { return d.Seconds() }

// num печатает float без экспоненты и без хвоста нулей: «49» а не «4.9e+01».
func num(v any) string {
	switch x := v.(type) {
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", x), "0"), ".")
	default:
		return fmt.Sprint(x)
	}
}

// escape — по спецификации: обратный слэш, кавычка и перевод строки.
func escape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Строка формата экспозиции: либо комментарий HELP/TYPE, либо
// «имя{метка="значение"}? число». Знание о формате живёт здесь — оба теста,
// и пакета, и CLI, сверяются с ним, а не держат по своей регулярке.
var validLine = regexp.MustCompile(`^(# (HELP|TYPE) [a-z_]+ .+|[a-z_]+(\{[a-z_]+="(?:[^"\\]|\\.)*"\})? -?[0-9]+(\.[0-9]+)?)$`)

// Valid сообщает, что каждая строка документа — по формату. Регулярка вместо
// expfmt: тот тянет protobuf и паникует без явной схемы валидации —
// тридцать пакетов ради двадцати строк.
func Valid(doc string) (bad string, ok bool) {
	for _, line := range strings.Split(strings.TrimRight(doc, "\n"), "\n") {
		if !validLine.MatchString(line) {
			return line, false
		}
	}
	return "", true
}
