package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

// Правило подтверждения — чистая функция, и проверяется таблицей: цена
// ошибки здесь не техническая, а порог, подобранный на глаз и не закреплённый,
// уедет при первой же правке.
func TestNeedsConfirmation(t *testing.T) {
	tests := []struct {
		name string
		cfg  runner.Config
		want bool
	}{
		// Свои системы не спрашивают ни на какой нагрузке: иначе привыкаешь
		// жать y не читая, и вопрос перестаёт работать там, где он нужен.
		{"localhost, тяжёлый", runner.Config{URL: "http://localhost:8080/", Duration: time.Minute, Rate: 500}, false},
		{"127.0.0.1", runner.Config{URL: "http://127.0.0.1:8080/", Requests: 100000}, false},
		{"127.0.0.53 — весь /8 петлевой", runner.Config{URL: "http://127.0.0.53/", Requests: 100000}, false},
		{"[::1]", runner.Config{URL: "http://[::1]:8080/", Requests: 100000}, false},
		{"LOCALHOST в другом регистре", runner.Config{URL: "http://LOCALHOST:8080/", Requests: 100000}, false},

		// Смоук по чужому стенду проходит молча
		{"внешний, -n 200", runner.Config{URL: "http://example.com/", Requests: 200}, false},
		{"внешний, -z 5s", runner.Config{URL: "http://example.com/", Duration: 5 * time.Second}, false},

		{"внешний, -rate 100", runner.Config{URL: "http://example.com/", Rate: 100, Requests: 300}, true},
		{"внешний, -z 30s", runner.Config{URL: "http://example.com/", Duration: 30 * time.Second}, true},
		{"внешний, -n 10000", runner.Config{URL: "http://example.com/", Requests: 10000}, true},

		// Границы: порог включающий, на шаг ниже — молчим
		{"внешний, -n 9999", runner.Config{URL: "http://example.com/", Requests: 9999}, false},
		{"внешний, -rate 99", runner.Config{URL: "http://example.com/", Rate: 99, Requests: 300}, false},
		{"внешний, -z 29s", runner.Config{URL: "http://example.com/", Duration: 29 * time.Second}, false},

		// Приватный адрес — такой же чужой: за 192.168.0.5 может быть
		// чей угодно сервис, и петлевым он от этого не становится.
		{"адрес в локальной сети", runner.Config{URL: "http://192.168.0.5:8080/", Requests: 50000}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := needsConfirmation(tt.cfg); got != tt.want {
				t.Errorf("needsConfirmation = %v, ожидалось %v", got, tt.want)
			}
		})
	}
}

// Без терминала спрашивать некого, и ждать ввода нельзя — это повесило бы
// чужой CI до таймаута. Отказ должен быть немедленным и до первого запроса.
func TestConfirmationWithoutTerminal(t *testing.T) {
	// Хост не существует: если отказ работает, до сети дело не дойдёт,
	// а если сломается — тест не станет ждать сетевого таймаута.
	const target = "http://example.invalid:9/"

	t.Run("отказывает и называет -yes", func(t *testing.T) {
		code, stdout, stderr := capture(t, "-n", "50000", "-c", "50", target)

		if code != exitUsage {
			t.Errorf("код %d, ожидался %d", code, exitUsage)
		}
		if !strings.Contains(stderr, "-yes") {
			t.Errorf("в отказе не сказано, что делать:\n%s", stderr)
		}
		// Шапка не должна опережать вопрос: «Запуск 50000 запросов»
		// читается как «уже стреляем».
		if strings.Contains(stdout, "Запуск") {
			t.Errorf("шапка напечатана до отказа:\n%s", stdout)
		}
	})

	t.Run("-yes снимает вопрос", func(t *testing.T) {
		// Прогон дойдёт до сети и там провалится — важно лишь, что до неё
		// он дошёл, то есть подтверждение больше не мешает.
		_, stdout, _ := capture(t, "-yes", "-n", "1", "-c", "1", "-t", "100ms", target)

		if !strings.Contains(stdout, "Запуск") {
			t.Errorf("-yes не пропустил прогон:\n%s", stdout)
		}
	})
}
