// Конфигурация прогона и её проверка. Отдельно от runner.go: этот файл
// меняется, когда добавляется флаг, а тот — когда меняется механика прогона.
package runner

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
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
	Rate        float64 // >0 — open-loop с постоянной частотой, не выше maxRate; 0 — closed-loop
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

// maxRate — один запрос в наносекунду: плотнее расписание непредставимо
// в time.Duration, offset() схлопывается в ноль, а stats теряет поправку.
const maxRate = float64(time.Second)

// Validate отвергает противоречивый прогон до первого запроса: иначе
// ошибка конфигурации выглядит как N одинаковых отказов сервера. Это
// вход runner, а не дубль проверок в cmd: Config может прийти не из флагов.
//
// Проверки сгруппированы по предмету: куда бить, чем, сколько и как долго.
// Правила про одно поле стоят рядом, иначе следующее припишут в третье место.
func (c Config) Validate() error {
	for _, check := range []func() error{
		c.validateTarget, c.validateRequest, c.validateWorkload, c.validateWarmup,
	} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// validateTarget — куда бить. url.Parse принимает "localhost:8080/api" молча,
// считая "localhost" схемой.
func (c Config) validateTarget() error {
	if c.URL == "" {
		return errors.New("URL не задан")
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return fmt.Errorf("некорректный URL: %w", err)
	}
	// gRPC — граница проекта, не забытая схема: «без схемы http://» отправил
	// бы человека дописывать http и получать N отказов от gRPC-сервера.
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

// validateRequest — чем бить. Go проверяет метод в NewRequest, а заголовки
// в Transport.Do — оба после шапки; здесь — до. Пустой метод Go молча
// превращает в GET, но человек написал -m: это ошибка, а не «не задано».
func (c Config) validateRequest() error {
	if !isToken(c.Method) {
		return fmt.Errorf("метод %q — не HTTP-токен", c.Method)
	}
	for name, values := range c.Headers {
		if !isToken(name) {
			return fmt.Errorf("заголовок %q: имя — не HTTP-токен", name)
		}
		for _, v := range values {
			if !isFieldValue(v) {
				return fmt.Errorf("заголовок %q: значение содержит управляющий символ", name)
			}
		}
	}
	return nil
}

// validateWorkload — сколько работы, как долго и в сколько рук.
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
	// Таймаут — предел одного запроса; ноль сделал бы каждый запрос отказом.
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout должен быть > 0, получено %v", c.Timeout)
	}

	// Одна проверка на всё: NaN не проходит ни одно сравнение, Inf и
	// отрицательное — диапазон.
	if !(c.Rate >= 0 && c.Rate <= maxRate) {
		return fmt.Errorf("rate должен быть числом от 0 до %g, получено %v", maxRate, c.Rate)
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
