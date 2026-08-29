// Флаги: собственные flag.Value, объявление набора и сборка конфигурации
// прогона. Правило разбора значения меняют вместе с флагом, который его
// использует, — поэтому они в одном файле.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"github.com/Baklachok/loadgen/internal/slo"
)

// finiteFlag — float64 без NaN и ±Inf: strconv.ParseFloat их принимает
// («nan», «inf», «infinity» в любом регистре), а «-rate inf» уходило
// в open-loop с расписанием в нулевой момент. Один Value на все числовые
// флаги: через тот же Set их ставит и -f.
type finiteFlag float64

func (f *finiteFlag) String() string {
	return strconv.FormatFloat(float64(*f), 'g', -1, 64)
}

func (f *finiteFlag) Set(s string) error {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%q — не число", s)
	}
	*f = finiteFlag(v)
	return nil
}

type headerFlag struct {
	h http.Header
}

// -H повторяется, значит в файле ему можно дать список: см. repeatable в file.go.
func (*headerFlag) repeatable() {}

func (f *headerFlag) String() string {
	parts := make([]string, 0, len(f.h))
	for k, vs := range f.h {
		for _, v := range vs {
			parts = append(parts, k+": "+v)
		}
	}
	return strings.Join(parts, ", ")
}

func (f *headerFlag) Set(value string) error {
	k, v, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("заголовок должен быть в формате 'Key: Value', получено %q", value)
	}
	k = strings.TrimSpace(k)
	if k == "" {
		return fmt.Errorf("пустое имя заголовка в %q", value)
	}
	f.h.Add(k, strings.TrimSpace(v))
	return nil
}

// warmupFlag принимает либо длительность («5s»), либо число запросов («100»).
// Формы различимы однозначно: time.ParseDuration требует единицы измерения,
// поэтому «100» ей не подходит, а «5s» не подходит strconv.Atoi.
type warmupFlag struct {
	requests int
	duration time.Duration
}

func (f *warmupFlag) String() string {
	switch {
	case f.duration > 0:
		return f.duration.String()
	case f.requests > 0:
		return strconv.Itoa(f.requests)
	}
	return ""
}

func (f *warmupFlag) Set(value string) error {
	// Повторный флаг перетирает предыдущий целиком, как это делают обычные
	// флаги: иначе «-warmup 5s -warmup 100» выставит обе формы разом.
	if d, err := time.ParseDuration(value); err == nil {
		f.duration, f.requests = d, 0
		return nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		f.requests, f.duration = n, 0
		return nil
	}
	return fmt.Errorf("прогрев %q: ожидается длительность (5s) или число запросов (100)", value)
}

// flags — объявленные флаги и их FlagSet одним типом: иначе run таскает
// полтора десятка указателей через все свои шаги, а FlagSet — рядом с ними,
// потому что «был ли флаг назван» знает только он.
type flags struct {
	fs *flag.FlagSet

	n, c        *int
	z, timeout  *time.Duration
	method      *string
	body        *string
	output      *string
	trace       *bool
	insecure    *bool
	noKeepAlive *bool
	http2       *bool
	yes         *bool
	showVersion *bool

	compare *bool
	metrics *string
	file    *string
	sloP99  *time.Duration

	// Свои Value: лежат по значению, FlagSet пишет в них через указатель.
	rate         finiteFlag
	regressP99   finiteFlag
	regressRPS   finiteFlag
	sloErrorRate finiteFlag
	headers      headerFlag
	warmup       warmupFlag
}

func newFlags(stderr io.Writer) *flags {
	fs := flag.NewFlagSet("loadgen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := &flags{
		fs:          fs,
		n:           fs.Int("n", 200, "количество запросов"),
		c:           fs.Int("c", 50, "конкурентность; в open-loop — потолок запросов в полёте"),
		z:           fs.Duration("z", 0, "длительность прогона (взаимоисключающе с -n)"),
		method:      fs.String("m", "GET", "HTTP-метод"),
		body:        fs.String("d", "", "тело запроса"),
		timeout:     fs.Duration("t", 10*time.Second, "таймаут запроса"),
		output:      fs.String("o", "text", "формат вывода: text, json или prom"),
		trace:       fs.Bool("trace", false, "разбить latency по фазам: DNS, TCP, TLS, TTFB"),
		insecure:    fs.Bool("insecure", false, "не проверять TLS-сертификат"),
		noKeepAlive: fs.Bool("disable-keepalive", false, "новое соединение на каждый запрос"),
		http2:       fs.Bool("http2", false, "разрешить HTTP/2"),
		yes:         fs.Bool("yes", false, "не спрашивать подтверждения для не-локальной цели"),
		showVersion: fs.Bool("version", false, "показать версию"),

		compare: fs.Bool("compare", false, "сравнить два отчёта: loadgen -compare ДО ПОСЛЕ (файл или каталог)"),
		metrics: fs.String("metrics", "", "адрес для /metrics на время прогона: :9090 — только loopback, 0.0.0.0:9090 — наружу"),
		file:    fs.String("f", "", "прогон из YAML-файла; флаги в строке перекрывают файл"),
		sloP99:  fs.Duration("slo-p99", 0, "порог приёмки: p99 не выше указанного, иначе код 3"),

		headers: headerFlag{h: make(http.Header)},
	}
	fs.Var(&f.rate, "rate", "постоянный RPS, режим open-loop (0 — closed-loop)")
	fs.Var(&f.regressP99, "regress-p99", "при сравнении: насколько процентов p99 позволено вырасти, иначе код 3")
	fs.Var(&f.regressRPS, "regress-rps", "при сравнении: насколько процентов RPS позволено упасть, иначе код 3")
	fs.Var(&f.sloErrorRate, "slo-error-rate", "порог приёмки: доля не-2xx в процентах (0–100), иначе код 3")
	fs.Var(&f.headers, "H", "заголовок в формате 'Key: Value' (можно несколько раз)")
	fs.Var(&f.warmup, "warmup", "прогрев: длительность (5s) или число запросов (100), в статистику не идёт")

	fs.Usage = func() { printUsage(fs, stderr) }
	return f
}

func printUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprintf(w, "loadgen — нагрузочный тестер HTTP\n\n")
	fmt.Fprintf(w, "Использование:\n  loadgen [флаги] URL\n\nФлаги:\n")
	fs.PrintDefaults()

	fmt.Fprintf(w, "\nПримеры:\n")
	for _, ex := range []string{
		"loadgen -n 1000 -c 50 http://localhost:8080",
		"loadgen -z 30s -rate 500 -c 200 http://localhost:8080   # open-loop",
		"loadgen -n 1000 -o json http://localhost:8080 | jq .latency.p99_ms",
		`loadgen -m POST -d '{"a":1}' -H 'Content-Type: application/json' http://localhost:8080/api`,
	} {
		fmt.Fprintf(w, "  %s\n", ex)
	}
}

// named — флаги, заданные явно, строкой или файлом. Нужен там, где
// умолчание неотличимо от осознанного выбора.
func (f *flags) named() map[string]bool {
	set := make(map[string]bool)
	f.fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })
	return set
}

// namedRules — правила, которых Validate увидеть не может: он смотрит на
// значения, а «называл ли человек флаг» в значении не отражается. Ниже
// по слоям ноль — «не задано», и названный ноль молча выключал бы то,
// что человек включал.
func (f *flags) namedRules(set map[string]bool) error {
	if set["n"] && set["z"] {
		return errors.New("-n и -z взаимоисключающи")
	}
	// -z 0s превращался в план на 200 запросов, -slo-p99 0s — в отсутствие гейта.
	for _, d := range []struct {
		name string
		v    time.Duration
	}{{"z", *f.z}, {"slo-p99", *f.sloP99}} {
		if set[d.name] && d.v <= 0 {
			return fmt.Errorf("-%s должен быть > 0, получено %v", d.name, d.v)
		}
	}
	// Отрицательный — «не задано», 150% не нарушить никогда: гейт молча не стоит.
	if r := float64(f.sloErrorRate); set["slo-error-rate"] && (r < 0 || r > 100) {
		return fmt.Errorf("-slo-error-rate — проценты, 0–100; получено %v", r)
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
func (f *flags) config() (runner.Config, error) {
	// Заданы ли флаги явно, знает только FlagSet: сравнение с умолчанием
	// не годится — «-n 200 -z 5s» тоже противоречие, хотя 200 это дефолт,
	// а «-slo-error-rate 0» это осмысленное «ни одной ошибки».
	set := f.named()
	if err := f.namedRules(set); err != nil {
		return runner.Config{}, err
	}

	// -n обнуляется при -z, иначе Validate решит, что заданы оба.
	requests := *f.n
	if *f.z > 0 {
		requests = 0
	}

	// Регистр метода поднимаем: HTTP их различает, а Go-сервер отдаёт хендлеру
	// любой — «-m get» работал локально и получал 501 на стенде.
	method := strings.ToUpper(*f.method)

	// Подпись обязательна, но явный -H 'User-Agent: ...' её побеждает:
	// сервис может ключеваться на UA, и запретить это значило бы сломать
	// законный сценарий ради формальности.
	headers := f.headers.h
	if headers.Get("User-Agent") == "" {
		headers.Set("User-Agent", userAgent())
	}

	// Умолчание -c не должно спорить с -n: без этого «-n 10» отчитывается
	// о пятидесяти потоках, из которых работали десять. Явное -c не трогаем —
	// там человек сказал противоречие вслух, и Validate ответит ошибкой.
	concurrency := *f.c
	if !set["c"] && f.rate == 0 && requests > 0 && concurrency > requests {
		concurrency = requests
	}

	cfg := runner.Config{
		URL:              f.fs.Arg(0),
		Method:           method,
		Body:             []byte(*f.body),
		Headers:          headers,
		Requests:         requests,
		Duration:         *f.z,
		Concurrency:      concurrency,
		Timeout:          *f.timeout,
		Rate:             float64(f.rate),
		Trace:            *f.trace,
		WarmupRequests:   f.warmup.requests,
		WarmupDuration:   f.warmup.duration,
		DisableKeepAlive: *f.noKeepAlive,
		Insecure:         *f.insecure,
		HTTP2:            *f.http2,
	}
	return cfg, cfg.Validate()
}

// slo собирает пороги приёмки. Не заданный p99 — ноль, и Check его
// пропускает; у error-rate ноль осмыслен («ни одной ошибки»), поэтому
// «не задан» там −1, а «задан ли» спрашивается у FlagSet.
func (f *flags) slo() slo.Thresholds {
	out := slo.Thresholds{P99: *f.sloP99, ErrorRate: -1}
	if f.named()["slo-error-rate"] {
		out.ErrorRate = float64(f.sloErrorRate) / 100
	}
	return out
}
