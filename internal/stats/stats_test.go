package stats

import (
	"context"
	"io"
	"math"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

// Сквозная проверка: один смешанный прогон должен заполнить все headline-поля.
// Правила проверяются группами ниже, здесь — что они между собой соединены.
// Именно такие поломки группы и пропускают: каждая своё правило подтвердит,
// а провод между ними никто не дёрнет.
func TestComputeFillsSummary(t *testing.T) {
	results := []runner.Result{
		{Duration: ms(10), StatusCode: 200, BytesRead: 100},
		{Duration: ms(20), StatusCode: 200, BytesRead: 100},
		{Duration: ms(30), StatusCode: 500, BytesRead: 50},
		failed(context.DeadlineExceeded, ms(5)),
	}

	s := compute(results, 2*time.Second)

	if s.Total != 4 || s.OK != 2 || s.NonOK != 1 || s.Failed != 1 {
		t.Errorf("исходы: total=%d ok=%d non2xx=%d failed=%d", s.Total, s.OK, s.NonOK, s.Failed)
	}
	if s.RPS != 1.5 {
		t.Errorf("RPS = %v, ожидалось 1.5", s.RPS)
	}
	if !near(s.Latency.Mean, ms(20)) {
		t.Errorf("Mean = %v, ожидалось 20ms", s.Latency.Mean)
	}
	if s.Codes[200] != 2 || s.Codes[500] != 1 {
		t.Errorf("codes = %v", s.Codes)
	}
	if s.Errors[ErrTimeout] != 1 {
		t.Errorf("errors = %v", s.Errors)
	}
}

func TestHistogram(t *testing.T) {

	t.Run("пустой вход", func(t *testing.T) {
		if got := histogram(nil, 10); got != nil {
			t.Errorf("histogram(nil) = %v, want nil", got)
		}
	})

	t.Run("все замеры одинаковые", func(t *testing.T) {
		got := histogram([]time.Duration{ms(5), ms(5), ms(5)}, 10)
		if len(got) != 1 || got[0].Count != 3 || got[0].Upper != ms(5) {
			t.Errorf("got %+v, want один бакет на 3 замера", got)
		}
	})

	t.Run("ни один замер не потерян", func(t *testing.T) {
		sorted := make([]time.Duration, 0, 1000)
		for i := 1; i <= 1000; i++ {
			sorted = append(sorted, time.Duration(i)*time.Microsecond)
		}

		buckets := histogram(sorted, 10)
		if len(buckets) != 10 {
			t.Fatalf("бакетов %d, want 10", len(buckets))
		}

		total := 0
		for _, b := range buckets {
			total += b.Count
		}
		if total != len(sorted) {
			t.Errorf("сумма счётчиков %d, замеров %d", total, len(sorted))
		}
	})

	t.Run("границы растут и накрывают максимум", func(t *testing.T) {
		buckets := histogram([]time.Duration{ms(1), ms(3), ms(7), ms(200)}, 4)

		for i := 1; i < len(buckets); i++ {
			if buckets[i].Upper <= buckets[i-1].Upper {
				t.Errorf("граница %d (%v) не больше предыдущей (%v)", i, buckets[i].Upper, buckets[i-1].Upper)
			}
		}
		if last := buckets[len(buckets)-1].Upper; last != ms(200) {
			t.Errorf("последняя граница %v, want 200ms: максимум обязан попасть внутрь", last)
		}
	})

	t.Run("длинный хвост виден как разрыв", func(t *testing.T) {
		// 99 быстрых замеров и один медленный: линейная шкала должна оставить
		// пустые бакеты между ними, а не размазать выброс
		sorted := make([]time.Duration, 0, 100)
		for i := 0; i < 99; i++ {
			sorted = append(sorted, ms(5))
		}
		sorted = append(sorted, ms(400))

		buckets := histogram(sorted, 10)
		if buckets[0].Count != 99 {
			t.Errorf("первый бакет = %d, want 99", buckets[0].Count)
		}
		if buckets[len(buckets)-1].Count != 1 {
			t.Errorf("последний бакет = %d, want 1", buckets[len(buckets)-1].Count)
		}

		empty := 0
		for _, b := range buckets {
			if b.Count == 0 {
				empty++
			}
		}
		if empty == 0 {
			t.Error("между горбами нет пустых бакетов — разрыв не читается")
		}
	})

	// Гистограмма собирается из тех же замеров, что и перцентили: запрос
	// без полного ответа замера не даёт и в картинку попасть не может.
	t.Run("Compute кладёт в неё все полученные ответы", func(t *testing.T) {
		s := compute([]runner.Result{
			resp(200, ms(10)),
			resp(200, ms(20)),
			failed(context.DeadlineExceeded, ms(5)),
		}, time.Second)

		total := 0
		for _, b := range s.Histogram {
			total += b.Count
		}
		if total != s.Responses() {
			t.Errorf("в гистограмме %d замеров, полученных ответов %d", total, s.Responses())
		}
	})
}

func TestComputeOutcomes(t *testing.T) {
	t.Run("2xx или нет — по границам кода", func(t *testing.T) {
		tests := []struct {
			name   string
			code   int
			wantOK bool
		}{
			{"200", 200, true},
			{"204", 204, true},
			{"299 — верхняя граница", 299, true},
			{"301 — редирект не проходим, значит не успех", 301, false},
			{"400", 400, false},
			{"503", 503, false},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := compute([]runner.Result{resp(tt.code, ms(1))}, time.Second)

				if gotOK := s.OK == 1; gotOK != tt.wantOK {
					t.Errorf("код %d: OK=%d NonOK=%d, ожидалось ok=%v", tt.code, s.OK, s.NonOK, tt.wantOK)
				}
			})
		}
	})

	// Сервис, отдающий одни 429, не должен отчитываться как успешный:
	// ровно этот случай выглядел рабочим и врал.
	t.Run("поток 429 — ни одного успеха", func(t *testing.T) {
		s := compute(repeat(100, resp(429, ms(2))), time.Second)

		if s.OK != 0 || s.NonOK != 100 {
			t.Errorf("OK=%d NonOK=%d, ожидалось 0 и 100", s.OK, s.NonOK)
		}
		if s.SuccessRate() != 0 {
			t.Errorf("SuccessRate = %v, ожидался 0", s.SuccessRate())
		}
		// RPS — пропускная способность, и 100 отказов в секунду это правда
		if s.RPS != 100 {
			t.Errorf("RPS = %v, ожидалось 100: отказ тоже обслуженный запрос", s.RPS)
		}
		// Латентность отказов осмысленна: показывает, как быстро сервис отшивает
		if !near(s.Latency.P50, ms(2)) {
			t.Errorf("p50 = %v, ожидалось 2ms", s.Latency.P50)
		}
	})

	// Пустой прогон не должен делить на ноль и не должен отчитаться о 100%.
	t.Run("пустой прогон", func(t *testing.T) {
		s := compute(nil, time.Second)

		if s.SuccessRate() != 0 {
			t.Errorf("SuccessRate = %v, ожидался 0", s.SuccessRate())
		}
		if s.Responses() != 0 {
			t.Errorf("Responses = %d, ожидался 0", s.Responses())
		}
	})

	// Таймаут — не то же самое, что отказ сервера: ответа не было вовсе.
	t.Run("таймаут не смешивается с не-2xx", func(t *testing.T) {
		s := compute([]runner.Result{
			resp(503, ms(2)),
			failed(context.DeadlineExceeded, ms(10000)),
		}, time.Second)

		if s.NonOK != 1 || s.Failed != 1 {
			t.Errorf("NonOK=%d Failed=%d, ожидалось по единице", s.NonOK, s.Failed)
		}
		if !near(s.Latency.Max, ms(2)) {
			t.Errorf("max = %v: таймаут просочился в перцентили и утопил их", s.Latency.Max)
		}
	})

	// Сервис, сбрасывающий нагрузку пятисотками и рвущий соединения, раньше
	// выглядел ровно как недоступный: код терялся вместе с телом.
	t.Run("оборванное тело — четвёртый исход", func(t *testing.T) {
		s := compute([]runner.Result{
			resp(200, ms(1)),
			failed(context.DeadlineExceeded, ms(10000)),
			truncated(503, io.ErrUnexpectedEOF, 27),
		}, time.Second)

		if s.OK != 1 || s.NonOK != 0 || s.Failed != 1 || s.Truncated != 1 {
			t.Errorf("исходы: OK=%d NonOK=%d Failed=%d Truncated=%d, ожидалось 1/0/1/1",
				s.OK, s.NonOK, s.Failed, s.Truncated)
		}
		if got := s.OK + s.NonOK + s.Failed + s.Truncated; got != s.Total {
			t.Errorf("сумма исходов %d, а Total %d: шапка перестала сходиться", got, s.Total)
		}

		// Код — единственное, что отличает «сервис отказывает» от «сервиса нет»
		if s.Codes[503] != 1 {
			t.Errorf("codes = %v: код оборванного ответа потерян", s.Codes)
		}
		if s.Errors[ErrTruncated] != 1 {
			t.Errorf("errors = %v: причина обрыва потеряна", s.Errors)
		}

		// Полного ответа не было — в RPS он идти не должен
		if s.Responses() != 1 {
			t.Errorf("Responses = %d, ожидался 1: оборванный завысил бы RPS", s.Responses())
		}
		// Длительность оборванного запроса неполна, перцентилям она чужая
		if s.Latency.Samples != 1 {
			t.Errorf("замеров %d, ожидался 1: оборванный попал в перцентили", s.Latency.Samples)
		}
		// Байты, прочитанные до обрыва, прочитаны на самом деле
		if s.BytesRead != 27 {
			t.Errorf("BytesRead = %d, ожидалось 27: throughput занижен на долю обрывов", s.BytesRead)
		}
	})

	// Ненулевой ClientErrors обесценивает весь прогон: цифры описывают
	// не сервис, а нас. Поэтому это подмножество Failed считается отдельно.
	t.Run("клиентские отказы выделены из прочих", func(t *testing.T) {
		fdLimit := &net.OpError{Op: "dial", Err: os.NewSyscallError("socket", syscall.EMFILE)}

		s := compute([]runner.Result{
			failed(fdLimit, ms(1)),
			failed(fdLimit, ms(1)),
			failed(syscall.ECONNREFUSED, ms(1)),
			resp(200, ms(5)),
		}, time.Second)

		if s.Failed != 3 {
			t.Errorf("Failed = %d, ожидалось 3", s.Failed)
		}
		if s.ClientErrors != 2 {
			t.Errorf("ClientErrors = %d, ожидалось 2: отказ в соединении — не наш лимит", s.ClientErrors)
		}
		if s.Errors[ErrFDLimit] != 2 {
			t.Errorf("в разбивке %d дескрипторных ошибок", s.Errors[ErrFDLimit])
		}
	})
}

// Прогрев не должен подмешиваться в статистику, но и исчезать молча тоже:
// отброшенное без следа — способ потерять доверие к отчёту.

func TestComputeExcludesWarmup(t *testing.T) {
	warm := resp(200, ms(500)) // медленный: платил за рукопожатие
	warm.Warmup = true

	s := compute([]runner.Result{
		warm, warm,
		resp(200, ms(10)),
		resp(200, ms(20)),
		resp(500, ms(30)),
	}, time.Second)

	if s.Warmup != 2 {
		t.Errorf("Warmup = %d, ожидалось 2", s.Warmup)
	}
	if s.Total != 3 {
		t.Errorf("Total = %d, ожидалось 3: прогрев не входит в измеренные", s.Total)
	}
	if s.OK != 2 || s.NonOK != 1 {
		t.Errorf("OK=%d NonOK=%d, ожидалось 2 и 1", s.OK, s.NonOK)
	}
	// Ради этого всё и затевалось: медленный прогрев не должен тянуть хвост
	if !near(s.Latency.Max, ms(30)) {
		t.Errorf("max = %v, ожидалось 30ms: прогрев просочился в перцентили", s.Latency.Max)
	}
	if s.RPS != 3 {
		t.Errorf("RPS = %v, ожидалось 3: прогрев не считается пропускной способностью", s.RPS)
	}
}

// Частота и её знаменатель: место, где цифра легче всего расходится
// с реальностью, оставаясь правдоподобной.
func TestComputeRates(t *testing.T) {
	// Числитель RPS считается без прогрева — значит и знаменатель должен быть
	// без него, иначе цифра занижается ровно на долю прогрева.
	t.Run("RPS считается по окну измерения", func(t *testing.T) {
		warm := resp(200, ms(80))
		warm.Warmup = true

		results := append([]runner.Result{warm, warm}, repeat(100, resp(200, ms(5)))...)

		// Прогон длился 2с, из них измерялись последние 0.5с
		s := computeReport(results, runner.Report{
			Elapsed: 2 * time.Second,
			Window:  500 * time.Millisecond,
		})

		if s.Total != 100 || s.Warmup != 2 {
			t.Fatalf("Total=%d Warmup=%d, ожидалось 100 и 2", s.Total, s.Warmup)
		}
		if s.RPS != 200 {
			t.Errorf("RPS = %v, ожидалось 200 (100 запросов / 0.5с). По всему прогону вышло бы 50", s.RPS)
		}
	})

	t.Run("пустое окно — нулевой RPS", func(t *testing.T) {
		warm := resp(200, ms(5))
		warm.Warmup = true

		s := computeReport([]runner.Result{warm}, runner.Report{Elapsed: time.Second})

		if s.RPS != 0 {
			t.Errorf("RPS = %v при пустом окне, ожидался 0", s.RPS)
		}
	})

	// Заданная частота должна доезжать до отчёта, иначе сопоставлять не с чем.
	t.Run("заданная частота доезжает до отчёта", func(t *testing.T) {
		s := computeReport([]runner.Result{resp(200, ms(1))}, runner.Report{
			Elapsed:    time.Second,
			Window:     time.Second,
			TargetRate: 500,
		})

		if s.TargetRate != 500 {
			t.Errorf("TargetRate = %v, ожидалось 500", s.TargetRate)
		}
	})

	t.Run("недобор частоты", func(t *testing.T) {
		tests := []struct {
			name   string
			target float64
			rps    float64
			want   float64
		}{
			{"closed-loop — цели не было", 0, 1500, 0},
			{"цель достигнута", 1000, 1000, 0},
			{"цель превышена — не недобор", 1000, 1050, 0},
			{"недобор вдвое", 1000, 500, 0.5},
			{"недобор на процент", 1000, 990, 0.01},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := Summary{TargetRate: tt.target, RPS: tt.rps}

				if got := s.RateShortfall(); math.Abs(got-tt.want) > 1e-9 {
					t.Errorf("RateShortfall() = %v, ожидалось %v", got, tt.want)
				}
			})
		}
	})
}

// Расписание: кто ушёл позже слота и во что это обошлось клиенту. Отдельно
// от частоты — предмет другой. RPS отвечает на «сколько прошло», расписание
// на «когда именно и с каким опозданием»; под одним именем они разъезжались.
func TestComputeSchedule(t *testing.T) {
	// Порог опоздания — интервал расписания, а не абсолютная константа:
	// при 1000 RPS «поздно» это 1мс, при 10 RPS — 100мс.
	t.Run("опоздания считаются по интервалу расписания", func(t *testing.T) {
		onTime := resp(200, ms(5))
		onTime.Lag = 300 * time.Microsecond // джиттер планировщика

		late := resp(200, ms(5))
		late.Lag = 8 * time.Millisecond

		s := computeReport([]runner.Result{onTime, onTime, late, late, late}, runner.Report{
			Elapsed:    time.Second,
			Window:     time.Second,
			TargetRate: 1000, // интервал 1мс
		})

		if s.Late != 3 {
			t.Errorf("Late = %d, ожидалось 3: опоздавшими считаются те, кто вышел позже слота", s.Late)
		}
		if s.LateShare() != 0.6 {
			t.Errorf("LateShare = %v, ожидалось 0.6", s.LateShare())
		}
		if s.MaxLag != 8*time.Millisecond {
			t.Errorf("MaxLag = %v, ожидалось 8ms", s.MaxLag)
		}
	})

	t.Run("в closed-loop опозданий не бывает", func(t *testing.T) {
		late := resp(200, ms(5))
		late.Lag = time.Second // мусор в поле, расписания не было

		s := computeReport([]runner.Result{late}, runner.Report{
			Elapsed: time.Second,
			Window:  time.Second,
		})

		if s.Late != 0 {
			t.Errorf("Late = %d без заданной частоты", s.Late)
		}
		if s.LateShare() != 0 {
			t.Errorf("LateShare = %v без заданной частоты", s.LateShare())
		}
	})

	// На этом держится отказ от второго накопителя: в closed-loop поправленные
	// замеры до единого совпадают с исходными, и копить их отдельно — лишний
	// слайс на миллионы значений и лишняя сортировка.
	t.Run("поправка на расписание", func(t *testing.T) {
		t.Run("в closed-loop совпадает с замерами", func(t *testing.T) {
			// Lag в поле есть, но расписания не было: поправлять не на что
			r := resp(200, ms(10))
			r.Lag = time.Second

			s := compute(repeat(20, r), time.Second)

			if s.Corrected != s.Latency {
				t.Errorf("Corrected=%+v, Latency=%+v: без расписания они обязаны совпасть", s.Corrected, s.Latency)
			}
		})

		t.Run("в open-loop прибавляет опоздание", func(t *testing.T) {
			r := resp(200, ms(10))
			r.Lag = ms(40)

			s := computeReport(repeat(20, r), runner.Report{
				Elapsed: time.Second, Window: time.Second, TargetRate: 100,
			})

			if !near(s.Latency.P50, ms(10)) {
				t.Errorf("Latency.p50 = %v, ожидалось 10ms: в замер запроса опоздание не входит", s.Latency.P50)
			}
			if !near(s.Corrected.P50, ms(50)) {
				t.Errorf("Corrected.p50 = %v, ожидалось 50ms: это то, что почувствовал бы клиент по часам", s.Corrected.P50)
			}
		})
	})
}

// Перцентили: сам расчёт, достаточность выборки и таблица квантилей.
func TestPercentiles(t *testing.T) {
	t.Run("расчёт по ближайшему рангу", func(t *testing.T) {

		tests := []struct {
			name   string
			sorted []time.Duration
			p      float64
			want   time.Duration
		}{
			{"empty", nil, 0.5, 0},
			{"single p50", []time.Duration{ms(5)}, 0.50, ms(5)},
			{"single p99", []time.Duration{ms(5)}, 0.99, ms(5)},
			{"ten p50", tenMs(), 0.50, ms(5)},
			{"ten p90", tenMs(), 0.90, ms(9)},
			{"ten p95", tenMs(), 0.95, ms(10)},
			{"ten p99", tenMs(), 0.99, ms(10)},
			{"p0 is min", tenMs(), 0.0, ms(1)},
			{"p1 is max", tenMs(), 1.0, ms(10)},
			{"identical", []time.Duration{ms(3), ms(3), ms(3)}, 0.99, ms(3)},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := Percentile(tt.sorted, tt.p)
				if got != tt.want {
					t.Errorf("Percentile(%v, %.2f) = %v, want %v", tt.sorted, tt.p, got, tt.want)
				}
			})
		}
	})

	// Перцентиль осмыслен примерно при n >= 10/(1-p): десять наблюдений в самом
	// хвосте. Иначе p99 — это просто максимум, названный красивым словом.
	t.Run("сколько замеров нужно", func(t *testing.T) {
		t.Run("порог по правилу 10/(1-p)", func(t *testing.T) {
			tests := []struct {
				p    float64
				want int
			}{
				{0.50, 20}, {0.90, 100}, {0.95, 200}, {0.99, 1000}, {0.999, 10000},
			}
			for _, tt := range tests {
				if got := MinSamples(tt.p); got != tt.want {
					t.Errorf("MinSamples(%v) = %d, ожидалось %d", tt.p, got, tt.want)
				}
			}
		})

		t.Run("на пятидесяти замерах достоверен только p50", func(t *testing.T) {
			s := compute(repeat(50, resp(200, ms(5))), time.Second)

			if s.Latency.Samples != 50 {
				t.Fatalf("Samples = %d, ожидалось 50", s.Latency.Samples)
			}
			if !s.Latency.Reliable(0.50) {
				t.Error("p50 объявлен недостоверным на 50 замерах")
			}
			for _, q := range []float64{0.90, 0.95, 0.99} {
				if s.Latency.Reliable(q) {
					t.Errorf("p%.0f объявлен достоверным на 50 замерах", q*100)
				}
			}
		})

		t.Run("на тысяче достоверно всё", func(t *testing.T) {
			s := compute(repeat(1000, resp(200, ms(5))), time.Second)

			for _, q := range []float64{0.50, 0.90, 0.95, 0.99} {
				if !s.Latency.Reliable(q) {
					t.Errorf("p%.0f недостоверен на 1000 замерах", q*100)
				}
			}
		})
	})

	// Четыре почти одинаковых замыкания в таблице Quantiles — идеальное место
	// для копипасты: перепутать P95 и P99 глазами почти невозможно заметить.
	t.Run("аксессоры таблицы не перепутаны", func(t *testing.T) {
		l := Latencies{P50: ms(1), P90: ms(2), P95: ms(3), P99: ms(4)}
		want := map[string]time.Duration{"p50": ms(1), "p90": ms(2), "p95": ms(3), "p99": ms(4)}

		if len(Quantiles) != len(want) {
			t.Fatalf("в таблице %d перцентилей, в проверке %d", len(Quantiles), len(want))
		}

		for _, q := range Quantiles {
			expected, known := want[q.Name]
			if !known {
				t.Errorf("неизвестное имя %q в таблице", q.Name)
				continue
			}
			if got := q.Value(l); got != expected {
				t.Errorf("%s достаёт %v, ожидалось %v — перепутан аксессор", q.Name, got, expected)
			}
			if q.Q <= 0 || q.Q >= 1 {
				t.Errorf("%s: квантиль %v вне (0,1)", q.Name, q.Q)
			}
		}
	})
}
