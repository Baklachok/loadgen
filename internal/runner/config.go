// Конфигурация прогона и её проверка. Отдельно от runner.go: этот файл
// меняется, когда добавляется флаг, а тот — когда меняется механика прогона.
package runner

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	URL         string
	Method      string
	Body        []byte
	Headers     http.Header
	Requests    int
	Duration    time.Duration // режим -z, взаимоисключающе с Requests
	Concurrency int
	Timeout     time.Duration
	Rate        float64 // >0 — open-loop с постоянной частотой; 0 — closed-loop
	Trace       bool    // собирать разбивку latency по фазам соединения

	// Прогрев: первые запросы платят за TCP, TLS и холодные кэши сервера,
	// к установившейся нагрузке это отношения не имеет. Задаётся либо числом
	// запросов, либо длительностью — не тем и другим сразу. Прогрев входит
	// в прогон, а не добавляется к нему: -n 1000 -warmup 100 шлёт 1000
	// запросов, из которых измеряются 900.
	WarmupRequests int
	WarmupDuration time.Duration

	DisableKeepAlive bool
	Insecure         bool
	HTTP2            bool
}

// Validate отвергает противоречивый прогон до первого запроса: иначе
// ошибка конфигурации выглядит как N одинаковых отказов сервера.
//
// Проверки сгруппированы по предмету, а не свалены в список: правила про
// одно и то же поле должны стоять рядом, иначе следующее правило про
// потоки припишут в третье место.
func (c Config) Validate() error {
	if err := c.validateTarget(); err != nil {
		return err
	}
	if err := c.validateHeaders(); err != nil {
		return err
	}
	if err := c.validateWorkload(); err != nil {
		return err
	}
	// Таймаут не относится ни к цели, ни к объёму: это предел одного запроса.
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout должен быть > 0, получено %v", c.Timeout)
	}
	return c.validateWarmup()
}

// validateTarget — куда бить. url.Parse принимает "localhost:8080/api" молча,
// считая "localhost" схемой: без этой проверки прогон разваливался на N
// одинаковых отказов с бесполезным "other" вместо одной строки на старте.
func (c Config) validateTarget() error {
	// Метод — token по RFC 7230; http.NewRequest проверит то же, но уже
	// после шапки. Пустой Go молча превращает в GET — это не «не задано»,
	// а ошибка: человек написал -m.
	if !isToken(c.Method) {
		return fmt.Errorf("метод %q — не HTTP-токен", c.Method)
	}
	if c.URL == "" {
		return errors.New("URL не задан")
	}

	u, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("некорректный URL: %w", err)
	}
	// gRPC — граница проекта, не забытая схема: «без схемы http://» отправил
	// бы человека дописывать http и получать N отказов от gRPC-сервера.
	// Причина в README «Границы»: успех, коды и фазы — HTTP-семантика,
	// и она же в JSON-контракте.
	if u.Scheme == "grpc" || u.Scheme == "grpcs" {
		return errors.New("gRPC не поддерживается: loadgen — нагрузочник для HTTP, см. README «Границы»")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL %q без схемы http:// или https://", c.URL)
	}
	if u.Host == "" {
		return fmt.Errorf("в URL %q нет хоста", c.URL)
	}
	return nil
}

// tchar — символы token по RFC 7230 сверх букв и цифр.
const tchar = "!#$%&'*+-.^_`|~"

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte(tchar, c) >= 0:
		default:
			return false
		}
	}
	return true
}

// validateHeaders — имя это token, значение без управляющих символов
// (RFC 7230). Go проверяет то же в Transport.Do — после шапки и на каждом
// запросе: CR/LF в -H доезжали до статистики как «other», rc=2.
func (c Config) validateHeaders() error {
	for name, values := range c.Headers {
		if !isToken(name) {
			return fmt.Errorf("заголовок %q: имя — не HTTP-токен", name)
		}
		for _, v := range values {
			if !validFieldValue(v) {
				return fmt.Errorf("заголовок %q: значение содержит управляющий символ", name)
			}
		}
	}
	return nil
}

// validFieldValue — VCHAR, SP, HTAB и obs-text; управляющие и DEL — нет.
func validFieldValue(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

// validateWorkload — сколько работы и в сколько рук.
func (c Config) validateWorkload() error {
	// Ноль — «не задано», отрицательное — ошибка, как у timeout и rate.
	if c.Duration < 0 {
		return fmt.Errorf("duration не может быть отрицательной, получено %v", c.Duration)
	}
	if c.Requests < 1 && c.Duration <= 0 {
		return errors.New("нужен либо -n, либо -z")
	}
	if c.Requests > 0 && c.Duration > 0 {
		return errors.New("-n и -z взаимоисключающи")
	}

	if c.Concurrency < 1 {
		return fmt.Errorf("concurrency должен быть >= 1, получено %d", c.Concurrency)
	}
	// Поток в closed-loop берёт по запросу за раз, поэтому потоков больше,
	// чем запросов, не бывает: лишние не сделают ни одного. В open-loop
	// правило не действует — там -c это потолок запросов в полёте, и он
	// обязан быть больше, чтобы держать частоту.
	if c.Rate == 0 && c.Requests > 0 && c.Concurrency > c.Requests {
		return fmt.Errorf("потоков (%d) больше, чем запросов (%d): лишние не сделают ничего",
			c.Concurrency, c.Requests)
	}

	// Не дубль проверки в cmd: Validate — публичный вход runner, и Config
	// может прийти не из флагов. NaN и Inf проходят через «< 0» молча.
	if math.IsNaN(c.Rate) || math.IsInf(c.Rate, 0) {
		return fmt.Errorf("rate должен быть числом, получено %v", c.Rate)
	}
	if c.Rate < 0 {
		return fmt.Errorf("rate не может быть отрицательным, получено %v", c.Rate)
	}
	return nil
}

func (c Config) validateWarmup() error {
	if c.WarmupRequests < 0 || c.WarmupDuration < 0 {
		return errors.New("прогрев не может быть отрицательным")
	}
	if c.WarmupRequests > 0 && c.WarmupDuration > 0 {
		return errors.New("прогрев задаётся либо числом запросов, либо длительностью, но не обоими")
	}

	// Прогрев, съедающий весь прогон, оставляет пустую статистику —
	// это молчаливо бесполезный результат, лучше сказать сразу.
	if c.Requests > 0 && c.WarmupRequests >= c.Requests {
		return fmt.Errorf("прогрев в %d запросов не оставляет что измерять из %d", c.WarmupRequests, c.Requests)
	}
	if c.Duration > 0 && c.WarmupDuration >= c.Duration {
		return fmt.Errorf("прогрев в %v не оставляет времени на измерение из %v", c.WarmupDuration, c.Duration)
	}
	return nil
}
