package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Baklachok/loadgen/internal/runner"
)

// accepts — конфиг из аргументов собран; URL подставляется сам.
func accepts(t *testing.T, args ...string) runner.Config {
	t.Helper()
	cfg, err := parseConfig(t, append(args, localURL)...)
	if err != nil {
		t.Fatalf("%v отвергнуто: %v", args, err)
	}
	return cfg
}

// rejects — конфиг отвергнут, и ошибка называет want: флаг или значение,
// которое человек написал, — иначе он не поймёт, что исправлять.
func rejects(t *testing.T, want string, args ...string) {
	t.Helper()
	_, err := parseConfig(t, append(args, localURL)...)
	if err == nil {
		t.Fatalf("%v принято", args)
	}
	if !strings.Contains(err.Error(), want) {
		t.Errorf("%v: ошибка %q не называет %q", args, err, want)
	}
}

// Правила, которые Validate увидеть не может: он смотрит на значения,
// а «задан ли флаг» в значении не отражается.
func TestConfigRejectsContradictions(t *testing.T) {
	// Тело с методом GET законно по HTTP, но человек, пишущий -d, имеет
	// в виду POST. Промолчать здесь — дать ему не тот прогон.
	t.Run("-d без -m", func(t *testing.T) {
		rejects(t, "-d", "-d", `{"a":1}`)
		accepts(t, "-m", "POST", "-d", `{"a":1}`)
	})

	t.Run("-n вместе с -z", func(t *testing.T) {
		rejects(t, "-z", "-n", "200", "-z", "5s")
	})

	// Для Validate нулевая длительность — «не задано»: «-z 0s» и «-z -1s»
	// молча становились планом на 200 запросов. Ошибка обязана назвать -z,
	// а не «duration» из недр runner.
	t.Run("-z не больше нуля", func(t *testing.T) {
		rejects(t, "-z", "-z", "0s")
		rejects(t, "-z", "-z", "-1s")
		accepts(t, "-z", "1ms")
	})

	// Go отвергает такой заголовок в Transport.Do — после шапки, на каждом
	// запросе, как «other». Validate — до.
	t.Run("-H с переводом строки", func(t *testing.T) {
		rejects(t, "X-A", "-H", "X-A: 1\r\nX-B: 2")
	})

	t.Run("-rate плотнее наносекунды", func(t *testing.T) {
		rejects(t, "rate", "-rate", "1e10")
	})

	// Для slo «≤ 0» и «< 0» — «не задано», 150% не нарушить никогда: гейт
	// молча не стоял, rc=0 против любого сервера. Границы включительно:
	// 0 — «ни одной ошибки», 100 — законный, хоть и беззубый.
	t.Run("-slo-p99 не больше нуля", func(t *testing.T) {
		rejects(t, "-slo-p99", "-slo-p99", "0s")
		rejects(t, "-slo-p99", "-slo-p99", "-1ms")
		accepts(t, "-slo-p99", "1ms")
	})
	t.Run("-slo-error-rate вне 0–100", func(t *testing.T) {
		for _, bad := range []string{"-1", "101", "150"} {
			rejects(t, "-slo-error-rate", "-slo-error-rate", bad)
		}
		for _, ok := range []string{"0", "100", "1.5"} {
			accepts(t, "-slo-error-rate", ok)
		}
	})

	// Явное «-c 50 -n 10» — противоречие, сказанное вслух, и отвергается.
	// То же самое из умолчания -c человек не говорил, и ошибка была бы
	// хамством: -c подгоняется, чтобы шапка не врала про пятьдесят потоков.
	t.Run("-c больше -n", func(t *testing.T) {
		rejects(t, "потоков", "-n", "10", "-c", "50")
		if cfg := accepts(t, "-n", "10"); cfg.Concurrency != 10 {
			t.Errorf("потоков %d, ожидалось 10: шапка отчитается о том, чего не было", cfg.Concurrency)
		}
		// В open-loop -c это потолок запросов в полёте, и он обязан быть
		// больше -n, иначе частоту не удержать.
		accepts(t, "-n", "10", "-c", "50", "-rate", "500")
	})
}

// Методы в HTTP регистрозависимы, и все зарегистрированные записаны в верхнем.
// «-m get» уходило дословно и получало 501 — но только от настоящего сервера:
// Go отдаёт запрос хендлеру с любым методом, поэтому локально это выглядело
// рабочим и ломалось на стенде.
func TestConfigCanonicalisesMethod(t *testing.T) {
	for _, tt := range []struct {
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			if cfg := accepts(t, tt.args...); cfg.Method != tt.want {
				t.Errorf("метод %q, ожидался %q", cfg.Method, tt.want)
			}
		})
	}

	// Приведение регистра не должно чинить то, что чинить нельзя: пробел
	// внутри метода остаётся, Validate отвергает — и в ошибке стоит то,
	// что человек написал (в верхнем регистре), а не что-то починенное.
	t.Run("невалидный метод не чинится", func(t *testing.T) {
		rejects(t, `"PO ST"`, "-m", "po st")
	})
}

// Подпись обязана стоять по умолчанию: без неё на провод уходит
// Go-http-client/1.1, и админ на той стороне не поймёт, что это и откуда.
func TestConfigSignsRequests(t *testing.T) {
	t.Run("по умолчанию — имя, версия и адрес репозитория", func(t *testing.T) {
		ua := accepts(t).Headers.Get("User-Agent")
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
		if got := accepts(t, "-H", "User-Agent: custom/9").Headers.Get("User-Agent"); got != "custom/9" {
			t.Errorf("User-Agent = %q, ожидался custom/9", got)
		}
	})

	t.Run("другие заголовки подпись не отменяют", func(t *testing.T) {
		h := accepts(t, "-H", "X-Token: secret").Headers
		if h.Get("X-Token") != "secret" {
			t.Errorf("заголовок потерян: %v", h)
		}
		if !strings.HasPrefix(h.Get("User-Agent"), "loadgen/") {
			t.Errorf("подпись пропала при своём -H: %v", h)
		}
	})
}

// strconv.ParseFloat принимает «nan» и «inf» — а дальше NaN проходит любое
// сравнение как ложь, и Inf делает расписание нулевым. Отвергаем на входе,
// одним Value на все три числовых флага. Это ошибка Parse, а не config,
// поэтому не через accepts/rejects.
func TestNumericFlagsRejectNonFinite(t *testing.T) {
	parse := func(args ...string) error {
		return newFlags(io.Discard).parse(append(args, localURL))
	}
	for _, flagName := range []string{"-rate", "-slo-error-rate", "-regress-p99"} {
		for _, bad := range []string{"nan", "NaN", "inf", "-Inf", "infinity", "1e400"} {
			t.Run(flagName+" "+bad, func(t *testing.T) {
				if err := parse(flagName, bad); err == nil {
					t.Errorf("%s %s принято", flagName, bad)
				}
			})
		}
	}
	for _, good := range []string{"0", "0.5", "1e9", "-0"} {
		t.Run("-rate "+good+" принимается", func(t *testing.T) {
			if err := parse("-rate", good); err != nil {
				t.Errorf("%q отвергнуто: %v", good, err)
			}
		})
	}
}

// -d @путь читает тело из файла, как curl: argv ограничен 128 КБ на аргумент.
// Содержимое уходит байт в байт — с переводом строки и не-ASCII.
func TestBodyFromFile(t *testing.T) {
	want := "{\"кто\": \"ёж\"}\n"
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := string(accepts(t, "-m", "POST", "-d", "@"+path).Body); got != want {
		t.Errorf("тело %q, ожидалось содержимое файла %q", got, want)
	}
	if got := string(accepts(t, "-m", "POST", "-d", "x").Body); got != "x" {
		t.Errorf("литеральное тело %q, ожидалось x", got)
	}
	err := newFlags(io.Discard).parse([]string{"-m", "POST", "-d", "@" + filepath.Join(t.TempDir(), "нет"), localURL})
	if err == nil || !strings.Contains(err.Error(), "-d") {
		t.Errorf("несуществующий файл: %v", err)
	}
}
