// Подтверждение для чужих целей. Инструмент по определению создаёт нагрузку,
// и опечатка в хосте — это трафик по чужому серверу: «-rate 5000» по чужому
// API неотличим от атаки, и объясняться придётся не с инструментом.
package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
	"golang.org/x/term"
)

// Пороги заметности. Ниже них прогон читается как смоук — двести запросов
// по стенду никого не разбудят, и спрашивать на них значит приучить жать y
// не читая. Выше — это нагрузка, которую на той стороне увидят в графиках.
const (
	noticeableRate     = 100
	noticeableDuration = 30 * time.Second
	noticeableRequests = 10000
)

// permitted решает, можно ли начинать. Одна точка на всё правило: раньше
// «-yes», «чужая ли цель» и сам вопрос стояли тремя условиями в run, и
// политика жила в том, кто должен был только раздать шаги.
func permitted(cfg runner.Config, yes bool, stdin *os.File, stderr io.Writer) bool {
	if yes || !needsConfirmation(cfg) {
		return true
	}
	return confirm(stdin, stderr, cfg)
}

// needsConfirmation — цель чужая и прогон заметный. Чистая функция: всё
// интерактивное живёт отдельно, иначе правило не проверить таблицей.
func needsConfirmation(cfg runner.Config) bool {
	return !parseTarget(cfg.URL).loopback() && noticeable(cfg)
}

func noticeable(cfg runner.Config) bool {
	return cfg.Rate >= noticeableRate ||
		cfg.Duration >= noticeableDuration ||
		cfg.Requests >= noticeableRequests
}

// target — куда бьём, разобранное один раз. Две части нужны разным местам:
// в вопросе показывается host:port, а петлевым проверяется имя без порта, —
// и раньше ради этого URL разбирался дважды.
type target struct {
	hostPort string // как показать человеку
	hostname string // как проверить
}

func parseTarget(raw string) target {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return target{hostPort: raw}
	}
	return target{hostPort: u.Host, hostname: u.Hostname()}
}

// loopback смотрит на написанное в URL и ничего не резолвит: DNS на старте —
// лишняя точка отказа, а ошибиться лучше в сторону лишнего вопроса, чем
// пропустить чужой хост.
func (t target) loopback() bool {
	if strings.EqualFold(t.hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(t.hostname)
	return ip != nil && ip.IsLoopback()
}

// confirm спрашивает разрешения и возвращает, можно ли продолжать.
//
// На не-терминале не спрашивает вовсе: ждать ввода, которого не будет, значит
// повесить чужой CI до таймаута. Отказ там сразу и с подсказкой про -yes.
func confirm(stdin *os.File, stderr io.Writer, cfg runner.Config) bool {
	fmt.Fprintf(stderr, "%s — не локальный хост, а прогон заметный: %s.\n",
		parseTarget(cfg.URL).hostPort, loadDescription(cfg))

	if !term.IsTerminal(int(stdin.Fd())) {
		fmt.Fprintln(stderr, "Спросить некого: ввод не терминал. Если разрешение есть — добавьте -yes.")
		return false
	}

	fmt.Fprint(stderr, "Продолжить? [y/N] ")
	answer, _ := bufio.NewReader(stdin).ReadString('\n')

	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes", "д", "да":
		return true
	}
	fmt.Fprintln(stderr, "Отменено.")
	return false
}

// loadDescription называет цифру, о которой спрашивают: без неё вопрос
// риторический, и на него отвечают не глядя.
func loadDescription(cfg runner.Config) string {
	switch {
	case cfg.Rate > 0 && cfg.Duration > 0:
		return fmt.Sprintf("%.0f RPS в течение %v", cfg.Rate, cfg.Duration)
	case cfg.Rate > 0:
		return fmt.Sprintf("%.0f RPS, %d запросов", cfg.Rate, cfg.Requests)
	case cfg.Duration > 0:
		return cfg.Duration.String()
	}
	return fmt.Sprintf("%d запросов", cfg.Requests)
}
