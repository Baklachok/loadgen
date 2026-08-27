package runner

import (
	"context"
	"testing"
	"time"
)

// countWarmup — разметка прогрева проверяется в трёх тестах, и каждый раз
// это один и тот же проход по результатам.
func countWarmup(res []Result) (warm, measured int) {
	for _, r := range res {
		if r.Warmup {
			warm++
			continue
		}
		measured++
	}
	return warm, measured
}

func TestWarmupByCount(t *testing.T) {
	srv := sleepServer(t, 0)

	cfg := requestsConfig(srv, 50)
	cfg.WarmupRequests = 10

	res := mustRun(t, context.Background(), cfg)
	assertNoErrors(t, res)

	// Прогрев входит в прогон, а не добавляется к нему
	if len(res) != cfg.Requests {
		t.Fatalf("получено %d результатов, ожидалось %d", len(res), cfg.Requests)
	}

	if warm, measured := countWarmup(res); warm != 10 || measured != 40 {
		t.Errorf("прогрев=%d измерено=%d, ожидалось 10 и 40", warm, measured)
	}
}

func TestWarmupByDuration(t *testing.T) {
	srv := sleepServer(t, 10*time.Millisecond)

	cfg := durationConfig(srv, 600*time.Millisecond)
	cfg.Concurrency = 4
	cfg.WarmupDuration = 200 * time.Millisecond

	res := mustRun(t, context.Background(), cfg)
	if len(res) == 0 {
		t.Fatal("нет результатов")
	}

	// Треть прогона — прогрев, поэтому обе части обязаны быть непустыми.
	// Точные числа тут проверять нечего: они зависят от планировщика.
	warm, measured := countWarmup(res)
	if warm == 0 {
		t.Error("прогрев не пометил ни одного запроса")
	}
	if measured == 0 {
		t.Error("после прогрева не осталось измеренных запросов")
	}
}

func TestNoWarmupByDefault(t *testing.T) {
	srv := sleepServer(t, 0)

	res := mustRun(t, context.Background(), requestsConfig(srv, 5))
	if warm, _ := countWarmup(res); warm != 0 {
		t.Errorf("помечено прогревом %d запросов, хотя флаг не задан", warm)
	}
}

// Прогрев, съедающий весь прогон, оставляет пустую статистику: молчаливо
// бесполезный результат хуже честной ошибки на старте.
func TestWarmupValidation(t *testing.T) {
	srv := sleepServer(t, 0)

	tests := []struct {
		name string
		cfg  func() Config
	}{
		{"обе формы разом", func() Config {
			c := requestsConfig(srv, 100)
			c.WarmupRequests, c.WarmupDuration = 10, time.Second
			return c
		}},
		{"отрицательное число", func() Config {
			c := requestsConfig(srv, 100)
			c.WarmupRequests = -1
			return c
		}},
		{"отрицательная длительность", func() Config {
			c := requestsConfig(srv, 100)
			c.WarmupDuration = -time.Second
			return c
		}},
		{"прогрев съедает весь -n", func() Config {
			c := requestsConfig(srv, 100)
			c.WarmupRequests = 100
			return c
		}},
		{"прогрев съедает весь -z", func() Config {
			c := durationConfig(srv, time.Second)
			c.WarmupDuration = time.Second
			return c
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Run(context.Background(), tt.cfg()); err == nil {
				t.Error("ожидалась ошибка конфигурации")
			}
		})
	}
}

// Окно измерения — единственный честный знаменатель для RPS: числитель
// считается без прогрева, значит и знаменатель обязан быть без него.
func TestWarmupShortensMeasurementWindow(t *testing.T) {
	srv := sleepServer(t, 5*time.Millisecond)

	cfg := durationConfig(srv, 600*time.Millisecond)
	cfg.Concurrency = 4
	cfg.WarmupDuration = 200 * time.Millisecond

	rep := mustRunReport(t, context.Background(), cfg)

	if rep.Elapsed < 500*time.Millisecond {
		t.Fatalf("Elapsed = %v, прогон должен был длиться ~600мс", rep.Elapsed)
	}
	// Окно короче прогона примерно на прогрев; допуск широкий,
	// потому что точные границы зависят от планировщика
	if rep.Window >= rep.Elapsed {
		t.Errorf("Window = %v при Elapsed = %v: прогрев не вычтен", rep.Window, rep.Elapsed)
	}
	if gap := rep.Elapsed - rep.Window; gap < 150*time.Millisecond || gap > 300*time.Millisecond {
		t.Errorf("прогон длиннее окна на %v, ожидалось около 200мс", gap)
	}
}

func TestWindowEqualsElapsedWithoutWarmup(t *testing.T) {
	srv := sleepServer(t, time.Millisecond)

	rep := mustRunReport(t, context.Background(), requestsConfig(srv, 20))

	// Первый запрос стартует не мгновенно, поэтому окно чуть короче прогона
	if gap := rep.Elapsed - rep.Window; gap > 50*time.Millisecond {
		t.Errorf("окно короче прогона на %v без всякого прогрева", gap)
	}
	if rep.Window <= 0 {
		t.Error("окно измерения нулевое")
	}
}
