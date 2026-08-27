package stats

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

type Summary struct {
	Total  int // измеренных запросов, без прогрева
	Warmup int // отброшено как прогрев

	// Три исхода, а не два. 429 и таймаут — разные события: первый означает,
	// что сервер работает и отказывает, второй — что ответа не было вовсе.
	// Слепить их вместе значит либо отчитаться об успехе там, где сервис
	// отдавал одни отказы, либо утопить перцентили в значении таймаута.
	OK     int // ответ 2xx
	NonOK  int // ответ получен, но не 2xx
	Failed int // ответа не было: таймаут, обрыв, отказ в соединении

	// Elapsed — весь прогон, Window — окно измерения. С прогревом они
	// расходятся, и RPS обязан считаться по Window: иначе числитель без
	// прогрева делится на знаменатель с ним, и цифра занижается втрое.
	Elapsed time.Duration
	Window  time.Duration

	// RPS — полученных ответов в секунду, то есть пропускная способность.
	// Не «успешных в секунду»: 500-ка это тоже обслуженный запрос.
	RPS float64

	// TargetRate — заданная частота open-loop, 0 в closed-loop.
	TargetRate float64

	// Late — запросы, ушедшие позже расписания больше чем на один интервал,
	// то есть отставшие минимум на целый слот. Меньший разброс — джиттер
	// планировщика, и считать его опозданием значит зашуметь отчёт.
	Late int

	// Latency — время самих запросов, Corrected — оно же плюс отставание
	// старта от расписания. В closed-loop они совпадают; в open-loop расходятся
	// ровно настолько, насколько генератор не успевал за собственным планом,
	// и именно Corrected показывает, что видел бы клиент, шлющий по часам.
	Latency   Latencies
	Corrected Latencies
	MaxLag    time.Duration

	BytesRead  int64
	Throughput float64 // МБ/с

	// Histogram — распределение Latency по равным бакетам, для картинки в отчёте
	Histogram []Bucket

	// Trace — разбивка по фазам соединения; nil, если трассировку не включали
	Trace *TraceSummary

	Codes  map[int]int
	Errors map[ErrorKind]int
}

// Responses — сколько запросов получили ответ, любой.
func (s Summary) Responses() int { return s.OK + s.NonOK }

// schedulePeriod — сколько времени отведено расписанием на один запрос.
func schedulePeriod(rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rate)
}

// LateShare — доля запросов, ушедших с опозданием больше чем на слот.
func (s Summary) LateShare() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Late) / float64(s.Total)
}

// RateShortfall — насколько достигнутая частота ниже заданной, долей единицы.
// Ноль означает «цель достигнута или превышена», а также closed-loop, где
// цели не было вовсе.
//
// Расхождение здесь — не косметика: пока не выяснено, кто не тянет, сервис
// или сам генератор, остальные цифры прогона интерпретировать нельзя.
func (s Summary) RateShortfall() float64 {
	if s.TargetRate <= 0 || s.RPS >= s.TargetRate {
		return 0
	}
	return (s.TargetRate - s.RPS) / s.TargetRate
}

// SuccessRate — доля 2xx от всех запросов. Считается, а не хранится: поле
// рядом со счётчиками рано или поздно разойдётся с ними при правке.
func (s Summary) SuccessRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.OK) / float64(s.Total)
}

// isOK: успех — это 2xx. 3xx сюда не входит осознанно — редиректы мы не
// проходим, и 301 означает, что запрошенного ресурса по этому адресу нет.
func isOK(code int) bool { return code >= 200 && code < 300 }

func Compute(rep runner.Report) Summary {
	s := Summary{
		Elapsed:    rep.Elapsed,
		Window:     rep.Window,
		TargetRate: rep.TargetRate,
		Codes:      make(map[int]int),
		Errors:     make(map[ErrorKind]int),
	}

	// Порог опоздания — один интервал расписания: при 2000 RPS это 500мкс,
	// при 10 RPS — 100мс. Абсолютная константа тут не годится, потому что
	// «поздно» определяется частотой, а не часами.
	period := schedulePeriod(rep.TargetRate)

	var service, corrected samples

	for _, r := range rep.Results {
		// Прогрев считаем, но нигде больше не учитываем: отброшенное молча —
		// способ потерять доверие к отчёту.
		if r.Warmup {
			s.Warmup++
			continue
		}

		s.Total++
		s.MaxLag = max(s.MaxLag, r.Lag)
		if period > 0 && r.Lag > period {
			s.Late++
		}

		if r.Err != nil {
			s.recordError(r.Err)
			continue
		}

		s.recordResponse(r)
		service.add(r.Duration)
		corrected.add(r.Lag + r.Duration)
	}

	// Перцентили — по всем полученным ответам: 503 за 2мс это настоящая работа
	// сервера, и прятать её нельзя. А вот таймауты сюда не попадают, иначе p99
	// схлопнется в значение -t и деградацию станет не видно.
	if s.Responses() > 0 {
		s.Latency = service.latencies()
		s.Corrected = corrected.latencies()
		s.Histogram = histogram(service.sorted(), histogramBuckets)
	}

	s.Trace = computeTrace(rep.Results)

	// Знаменатель — окно измерения, а не весь прогон: и запросы, и байты
	// в числителе посчитаны без прогрева.
	if s.Window > 0 {
		s.RPS = float64(s.Responses()) / s.Window.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / s.Window.Seconds()
	}

	return s
}

// recordError — ответа не было вовсе: таймаут, обрыв, отказ в соединении.
func (s *Summary) recordError(err error) {
	s.Failed++
	s.Errors[Classify(err)]++
}

// recordResponse — сервер ответил, и это результат независимо от кода.
func (s *Summary) recordResponse(r runner.Result) {
	if isOK(r.StatusCode) {
		s.OK++
	} else {
		s.NonOK++
	}

	s.Codes[r.StatusCode]++
	s.BytesRead += r.BytesRead
}
