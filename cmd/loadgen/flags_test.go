package main

import (
	"strings"
	"testing"
)

// Правила, которые Validate увидеть не может: он смотрит на значения,
// а «задан ли флаг» в значении не отражается.
func TestConfigRejectsContradictions(t *testing.T) {
	// Тело с методом GET законно по HTTP, но человек, пишущий -d, имеет
	// в виду POST. Промолчать здесь — дать ему не тот прогон.
	t.Run("-d без -m", func(t *testing.T) {
		if _, err := parseConfig(t, "-d", `{"a":1}`, localURL); err == nil {
			t.Fatal("тело без -m принято: уйдёт методом GET")
		}
		if _, err := parseConfig(t, "-m", "POST", "-d", `{"a":1}`, localURL); err != nil {
			t.Errorf("-m POST вместе с -d отвергнут: %v", err)
		}
	})

	t.Run("-n вместе с -z", func(t *testing.T) {
		if _, err := parseConfig(t, "-n", "200", "-z", "5s", localURL); err == nil {
			t.Fatal("-n и -z приняты вместе")
		}
	})

	// Для Validate нулевая длительность — «не задано»: «-z 0s» и «-z -1s»
	// молча становились планом на 200 запросов. Ошибка обязана назвать -z,
	// а не «duration» из недр runner.
	t.Run("-z не больше нуля", func(t *testing.T) {
		for _, z := range []string{"0s", "-1s"} {
			_, err := parseConfig(t, "-z", z, localURL)
			if err == nil {
				t.Errorf("-z %s принят: прогон ушёл бы на -n 200", z)
			} else if !strings.Contains(err.Error(), "-z") {
				t.Errorf("-z %s: ошибка не называет флаг: %v", z, err)
			}
		}
		if _, err := parseConfig(t, "-z", "1ms", localURL); err != nil {
			t.Errorf("-z 1ms отвергнут: %v", err)
		}
	})

	// Явное «-c 50 -n 10» — противоречие, сказанное вслух, и отвергается.
	// То же самое из умолчания -c человек не говорил, и ошибка была бы
	// хамством: -c подгоняется, чтобы шапка не врала про пятьдесят потоков.
	t.Run("-c больше -n", func(t *testing.T) {
		if _, err := parseConfig(t, "-n", "10", "-c", "50", localURL); err == nil {
			t.Fatal("явное -c больше -n принято")
		}

		cfg, err := parseConfig(t, "-n", "10", localURL)
		if err != nil {
			t.Fatalf("умолчание -c отвергнуто на -n 10: %v", err)
		}
		if cfg.Concurrency != 10 {
			t.Errorf("потоков %d, ожидалось 10: шапка отчитается о том, чего не было", cfg.Concurrency)
		}

		// В open-loop -c это потолок запросов в полёте, и он обязан быть
		// больше -n, иначе частоту не удержать.
		if _, err := parseConfig(t, "-n", "10", "-c", "50", "-rate", "500", localURL); err != nil {
			t.Errorf("open-loop с потолком выше -n отвергнут: %v", err)
		}
	})
}

// Методы в HTTP регистрозависимы, и все зарегистрированные записаны в верхнем.
// «-m get» уходило дословно и получало 501 — но только от настоящего сервера:
// Go отдаёт запрос хендлеру с любым методом, поэтому локально это выглядело
// рабочим и ломалось на стенде.
func TestConfigCanonicalisesMethod(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"нижний регистр", []string{"-m", "get"}, "GET"},
		{"смешанный", []string{"-m", "Post"}, "POST"},
		// Нестандартные методы тоже: PROPFIND и MKCOL записаны в верхнем
		// ровно так же, а опечатку в них сделать проще всего.
		{"нестандартный", []string{"-m", "propfind"}, "PROPFIND"},
		{"уже канонический не портится", []string{"-m", "DELETE"}, "DELETE"},
		{"умолчание", nil, "GET"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseConfig(t, append(tt.args, localURL)...)
			if err != nil {
				t.Fatalf("конфиг отвергнут: %v", err)
			}
			if cfg.Method != tt.want {
				t.Errorf("метод %q, ожидался %q", cfg.Method, tt.want)
			}
		})
	}

	// Приведение регистра не должно чинить то, что чинить нельзя: пробел
	// внутри метода остаётся, и запрос обрывается до отправки — это
	// проверяет TestInvalidMethodFailsBeforeAnyRequest в runner.
	t.Run("невалидный метод не чинится", func(t *testing.T) {
		cfg, err := parseConfig(t, "-m", "PO ST", localURL)
		if err != nil {
			t.Fatalf("конфиг отвергнут раньше времени: %v", err)
		}
		if cfg.Method != "PO ST" {
			t.Errorf("метод %q: приведение регистра подменило невалидный метод", cfg.Method)
		}
	})
}

// Подпись обязана стоять по умолчанию: без неё на провод уходит
// Go-http-client/1.1, и админ на той стороне не поймёт, что это и откуда.
func TestConfigSignsRequests(t *testing.T) {
	t.Run("по умолчанию — имя, версия и адрес репозитория", func(t *testing.T) {
		cfg, err := parseConfig(t, localURL)
		if err != nil {
			t.Fatal(err)
		}

		ua := cfg.Headers.Get("User-Agent")
		if !strings.HasPrefix(ua, "loadgen/") {
			t.Errorf("User-Agent = %q: подписи нет", ua)
		}
		if !strings.Contains(ua, buildVersion()) {
			t.Errorf("User-Agent = %q: без версии не понять, чем прогоняли", ua)
		}
		if !strings.Contains(ua, repoURL) {
			t.Errorf("User-Agent = %q: без адреса некуда пойти за объяснением", ua)
		}
	})

	// Сервис может ключеваться на User-Agent, и запретить его подменять
	// значило бы сломать законный сценарий ради формальности.
	t.Run("явный -H побеждает", func(t *testing.T) {
		cfg, err := parseConfig(t, "-H", "User-Agent: custom/9", localURL)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.Headers.Get("User-Agent"); got != "custom/9" {
			t.Errorf("User-Agent = %q, ожидался custom/9", got)
		}
	})

	t.Run("другие заголовки подпись не отменяют", func(t *testing.T) {
		cfg, err := parseConfig(t, "-H", "X-Token: secret", localURL)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Headers.Get("X-Token") != "secret" {
			t.Errorf("заголовок потерян: %v", cfg.Headers)
		}
		if !strings.HasPrefix(cfg.Headers.Get("User-Agent"), "loadgen/") {
			t.Errorf("подпись пропала при своём -H: %v", cfg.Headers)
		}
	})
}

// strconv.ParseFloat принимает «nan» и «inf» — а дальше NaN проходит любое
// сравнение как ложь, и Inf делает расписание нулевым. Отвергаем на входе,
// одним Value на все три числовых флага.
func TestNumericFlagsRejectNonFinite(t *testing.T) {
	for _, flagName := range []string{"-rate", "-slo-error-rate", "-regress-p99"} {
		for _, bad := range []string{"nan", "NaN", "inf", "-Inf", "infinity", "1e400"} {
			t.Run(flagName+" "+bad, func(t *testing.T) {
				fs, _ := newTestFlags(t)
				if err := fs.Parse([]string{flagName, bad, localURL}); err == nil {
					t.Errorf("%s %s принято", flagName, bad)
				}
			})
		}
	}
	for _, good := range []string{"0", "0.5", "1e9", "-0"} {
		t.Run("-rate "+good+" принимается", func(t *testing.T) {
			fs, _ := newTestFlags(t)
			if err := fs.Parse([]string{"-rate", good, localURL}); err != nil {
				t.Errorf("%q отвергнуто: %v", good, err)
			}
		})
	}
}
