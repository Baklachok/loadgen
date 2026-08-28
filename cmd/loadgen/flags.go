// Объявление набора флагов и сборка конфигурации прогона. Держатся вместе
// намеренно: при добавлении флага правится и то, и другое.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/slo"
)

// flags — объявленные флаги одним типом: иначе run таскает полтора десятка
// указателей через все свои шаги.
type flags struct {
	n, c        *int
	z, timeout  *time.Duration
	method      *string
	body        *string
	output      *string
	rate        *float64
	trace       *bool
	insecure    *bool
	noKeepAlive *bool
	http2       *bool
	yes         *bool
	showVersion *bool

	sloP99       *time.Duration
	sloErrorRate *float64

	headers headerFlag
	warmup  warmupFlag
}

func newFlags(fs *flag.FlagSet, stderr io.Writer) *flags {
	f := &flags{
		n:           fs.Int("n", 200, "количество запросов"),
		c:           fs.Int("c", 50, "конкурентность; в open-loop — потолок запросов в полёте"),
		z:           fs.Duration("z", 0, "длительность прогона (взаимоисключающе с -n)"),
		method:      fs.String("m", "GET", "HTTP-метод"),
		body:        fs.String("d", "", "тело запроса"),
		timeout:     fs.Duration("t", 10*time.Second, "таймаут запроса"),
		rate:        fs.Float64("rate", 0, "постоянный RPS, режим open-loop (0 — closed-loop)"),
		output:      fs.String("o", "text", "формат вывода: text или json"),
		trace:       fs.Bool("trace", false, "разбить latency по фазам: DNS, TCP, TLS, TTFB"),
		insecure:    fs.Bool("insecure", false, "не проверять TLS-сертификат"),
		noKeepAlive: fs.Bool("disable-keepalive", false, "новое соединение на каждый запрос"),
		http2:       fs.Bool("http2", false, "разрешить HTTP/2"),
		yes:         fs.Bool("yes", false, "не спрашивать подтверждения для не-локальной цели"),
		showVersion: fs.Bool("version", false, "показать версию"),

		sloP99:       fs.Duration("slo-p99", 0, "порог приёмки: p99 не выше указанного, иначе код 3"),
		sloErrorRate: fs.Float64("slo-error-rate", 0, "порог приёмки: доля не-2xx в процентах, иначе код 3"),
	}
	fs.Var(&f.headers, "H", "заголовок в формате 'Key: Value' (можно несколько раз)")
	fs.Var(&f.warmup, "warmup", "прогрев: длительность (5s) или число запросов (100), в статистику не идёт")

	fs.Usage = func() {
		fmt.Fprintf(stderr, "loadgen — нагрузочный тестер HTTP\n\n")
		fmt.Fprintf(stderr, "Использование:\n  loadgen [флаги] URL\n\nФлаги:\n")
		fs.PrintDefaults()

		fmt.Fprintf(stderr, "\nПримеры:\n")
		for _, ex := range []string{
			"loadgen -n 1000 -c 50 http://localhost:8080",
			"loadgen -z 30s -rate 500 -c 200 http://localhost:8080   # open-loop",
			"loadgen -n 1000 -o json http://localhost:8080 | jq .latency.p99_ms",
			`loadgen -m POST -d '{"a":1}' -H 'Content-Type: application/json' http://localhost:8080/api`,
		} {
			fmt.Fprintf(stderr, "  %s\n", ex)
		}
	}
	return f
}

// setFlags — имена флагов, заданных в командной строке. Нужен там, где
// умолчание неотличимо от осознанного выбора.
func setFlags(fs *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// conflicts — правила, которых Validate увидеть не может: он смотрит на
// значения, а «называл ли человек флаг» в значении не отражается. Сюда же
// пойдут следующие правила матрицы несовместимостей.
func (f *flags) conflicts(set map[string]bool) error {
	if set["n"] && set["z"] {
		return errors.New("-n и -z взаимоисключающи")
	}
	// Тело без метода — это GET с телом. По HTTP законно, но человек почти
	// наверняка имел в виду POST и молча получит не тот прогон.
	if *f.body != "" && !set["m"] {
		return errors.New("-d задан без -m: тело уйдёт методом GET; укажите -m POST")
	}
	return nil
}

// config собирает конфиг прогона: отвергает противоречия, приводит значения
// к тому, что человек имел в виду, и отдаёт остальное на проверку Validate.
func (f *flags) config(fs *flag.FlagSet) (runner.Config, error) {
	// Заданы ли флаги явно, знает только FlagSet: сравнение с умолчанием
	// не годится — «-n 200 -z 5s» тоже противоречие, хотя 200 это дефолт,
	// а «-slo-error-rate 0» это осмысленное «ни одной ошибки».
	set := setFlags(fs)
	if err := f.conflicts(set); err != nil {
		return runner.Config{}, err
	}

	// -n обнуляется при -z, иначе Validate решит, что заданы оба.
	requests := *f.n
	if *f.z > 0 {
		requests = 0
	}

	// Регистр метода поднимаем: HTTP их различает, а все зарегистрированные
	// записаны в верхнем. «-m get» уходило дословно и получало 400 или 501 —
	// причём Go-сервер отдаёт запрос хендлеру с любым методом, так что
	// локально прогон выглядел рабочим и ломался ровно при переезде на стенд.
	method := strings.ToUpper(*f.method)

	// Подпись обязательна, но явный -H 'User-Agent: ...' её побеждает:
	// сервис может ключеваться на UA, и запретить это значило бы сломать
	// законный сценарий ради формальности.
	headers := f.headers.h
	if headers.Get("User-Agent") == "" {
		if headers == nil {
			headers = make(http.Header)
		}
		headers.Set("User-Agent", userAgent())
	}

	// Умолчание -c не должно спорить с -n: без этого «-n 10» отчитывается
	// о пятидесяти потоках, из которых работали десять. Явное -c не трогаем —
	// там человек сказал противоречие вслух, и Validate ответит ошибкой.
	concurrency := *f.c
	if !set["c"] && *f.rate == 0 && requests > 0 && concurrency > requests {
		concurrency = requests
	}

	cfg := runner.Config{
		URL:              fs.Arg(0),
		Method:           method,
		Body:             []byte(*f.body),
		Headers:          headers,
		Requests:         requests,
		Duration:         *f.z,
		Concurrency:      concurrency,
		Timeout:          *f.timeout,
		Rate:             *f.rate,
		Trace:            *f.trace,
		WarmupRequests:   f.warmup.requests,
		WarmupDuration:   f.warmup.duration,
		DisableKeepAlive: *f.noKeepAlive,
		Insecure:         *f.insecure,
		HTTP2:            *f.http2,
	}
	return cfg, cfg.Validate()
}

// slo собирает пороги приёмки. Не заданный порог остаётся нулевым, и Check
// его пропускает; ноль у error-rate осмыслен сам по себе, поэтому «задан ли»
// спрашивается у FlagSet, а не угадывается по значению.
func (f *flags) slo(fs *flag.FlagSet) slo.Thresholds {
	out := slo.Thresholds{P99: *f.sloP99, ErrorRate: -1}
	if setFlags(fs)["slo-error-rate"] {
		out.ErrorRate = *f.sloErrorRate / 100
	}
	return out
}
