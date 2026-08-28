package stats

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

type Summary struct {
	Total  int // измеренных запросов, без прогрева
	Warmup int // отброшено как прогрев

	// Partial — прогон оборван сигналом, цифры собраны не по всему плану.
	Partial bool

	// Четыре исхода, а не два. 429 и таймаут — разные события: первый означает,
	// что сервер работает и отказывает, второй — что ответа не было вовсе.
	// Слепить их вместе значит либо отчитаться об успехе там, где сервис
	// отдавал одни отказы, либо утопить перцентили в значении таймаута.
	OK     int // ответ 2xx
	NonOK  int // ответ получен, но не 2xx
	Failed int // ответа не было вовсе: таймаут до заголовков, отказ в соединении

	// Truncated — заголовки с кодом пришли, а тело оборвалось. Отдельный исход,
	// потому что ни к одному из трёх не сводится: сервис, сбрасывающий нагрузку
	// пятисотками и рвущий соединения, иначе неотличим от недоступного, а это
	// состояния, требующие противоположных действий.
	//
	// Код такого ответа попадает в Codes — он и есть улика. Поэтому сумма
	// по кодам больше Responses(), и шапка обязана это объяснить.
	Truncated int

	// ClientErrors — сколько из Failed приходится на исчерпание ресурсов
	// самого генератора. Ненулевое значение обесценивает весь прогон.
	ClientErrors int

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

// Responses — сколько запросов получили ответ целиком, любой.
//
// Оборванные исключены намеренно: полного ответа не было, и считать их
// обслуженными значило бы завысить RPS ровно на долю обрывов.
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

// accumulator — Summary в процессе сборки.
//
// Счётчики и накопители замеров живут в одном месте, потому что каждый
// результат правит и то, и другое. Раньше половина записи была методами
// на Summary, а половина — строчками в теле цикла, и ветки switch выглядели
// равноправными, хотя одна делала втрое больше остальных.
type accumulator struct {
	sum                Summary
	service, corrected samples

	// period — сколько времени расписание отводит на один запрос, то есть
	// порог опоздания. При 2000 RPS это 500мкс, при 10 RPS — 100мс.
	// Абсолютная константа тут не годится: «поздно» определяется частотой,
	// а не часами.
	period time.Duration
}

func newAccumulator(rep runner.Report) *accumulator {
	return &accumulator{
		sum: Summary{
			Elapsed:    rep.Elapsed,
			Window:     rep.Window,
			TargetRate: rep.TargetRate,
			Partial:    rep.Interrupted,
			Codes:      make(map[int]int),
			Errors:     make(map[ErrorKind]int),
		},
		period: schedulePeriod(rep.TargetRate),
	}
}

// add учитывает один результат прогона.
func (a *accumulator) add(r runner.Result) {
	// Прогрев считаем, но нигде больше не учитываем: отброшенное молча —
	// способ потерять доверие к отчёту.
	if r.Warmup {
		a.sum.Warmup++
		return
	}

	a.sum.Total++
	a.sum.MaxLag = max(a.sum.MaxLag, r.Lag)
	if a.period > 0 && r.Lag > a.period {
		a.sum.Late++
	}

	// Байты считаем до ветвления: прочитанное до обрыва прочитано
	// на самом деле, и выбрасывать его значит занижать throughput.
	a.sum.BytesRead += r.BytesRead

	switch {
	// Код вместе с ошибкой выставляет ровно одна ветка runner.do —
	// та, где оборвалось тело. Отдельного поля в Result не нужно.
	case r.Err != nil && r.StatusCode != 0:
		a.recordTruncated(r)
	case r.Err != nil:
		a.recordFailure(r.Err)
	default:
		a.recordResponse(r)
	}
}

// recordResponse — сервер ответил целиком, и это результат независимо от кода.
// Только такие ответы дают замеры: 503 за 2мс — настоящая работа сервера,
// и прятать её нельзя, а вот таймауту в перцентилях места нет, иначе p99
// схлопнется в значение -t и деградацию станет не видно.
func (a *accumulator) recordResponse(r runner.Result) {
	if isOK(r.StatusCode) {
		a.sum.OK++
	} else {
		a.sum.NonOK++
	}
	a.sum.Codes[r.StatusCode]++

	a.service.add(r.Duration)
	a.corrected.add(r.Lag + r.Duration)
}

// recordFailure — ответа не было вовсе: таймаут до заголовков, отказ
// в соединении, сброс.
func (a *accumulator) recordFailure(err error) {
	kind := Classify(err)

	a.sum.Failed++
	a.sum.Errors[kind]++
	if kind.ClientSide() {
		a.sum.ClientErrors++
	}
}

// recordTruncated — заголовки пришли, тело оборвалось. Код записываем: ради
// него всё и затевалось. Причина уходит в Errors наравне с таймаутами —
// этот список отвечает на вопрос «почему не было полного ответа», и обрыв
// такой же ответ на него. ClientErrors не трогаем: исчерпание дескрипторов
// у генератора не может вернуть ответ с кодом.
func (a *accumulator) recordTruncated(r runner.Result) {
	a.sum.Truncated++
	a.sum.Codes[r.StatusCode]++
	a.sum.Errors[Classify(r.Err)]++
}

// summary досчитывает производные величины. Отдельно от add, потому что
// считать их можно только когда виден весь прогон.
func (a *accumulator) summary() Summary {
	s := a.sum

	if s.Responses() > 0 {
		s.Latency = a.service.latencies()
		s.Corrected = a.corrected.latencies()
		s.Histogram = histogram(a.service.sorted(), histogramBuckets)
	}

	// Знаменатель — окно измерения, а не весь прогон: и запросы, и байты
	// в числителе посчитаны без прогрева.
	if s.Window > 0 {
		s.RPS = float64(s.Responses()) / s.Window.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / s.Window.Seconds()
	}
	return s
}

func Compute(rep runner.Report) Summary {
	acc := newAccumulator(rep)
	for _, r := range rep.Results {
		acc.add(r)
	}
	s := acc.summary()

	// Отдельный проход: фазы фильтруются по своим правилам — в них попадают
	// и оборванные ответы, потому что DNS, TCP и TLS на них состоялись.
	s.Trace = computeTrace(rep.Results)
	return s
}
