// Сборка сводки из потока результатов. Отдельно от stats.go: там описано,
// что прогон дал, здесь — как это считается. Последние правки меняли ровно
// одно из двух: переход на потоковый приём переписал накопитель целиком
// и не тронул ни одного поля Summary.
package stats

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

// schedulePeriod — сколько времени отведено расписанием на один запрос.
func schedulePeriod(rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / rate)
}

// isOK: успех — это 2xx. 3xx сюда не входит осознанно — редиректы мы не
// проходим, и 301 означает, что запрошенного ресурса по этому адресу нет.
func isOK(code int) bool { return code >= 200 && code < 300 }

// Accumulator собирает Summary из результатов, не храня их. Результаты
// приходят по одному и забываются: runner.Result это 64 байта плюс сорок
// на трассировку, а статистике из них нужны восемь.
//
// Он же — тот единственный шов, через который замеры уходят в накопитель:
// замена точного хранения на HDR-гистограмму меняет только его, не расползаясь
// по коду.
type Accumulator struct {
	sum                Summary
	service, corrected distribution

	// period — сколько времени расписание отводит на один запрос, то есть
	// порог опоздания. При 2000 RPS это 500мкс, при 10 RPS — 100мс.
	// Абсолютная константа тут не годится: «поздно» определяется частотой,
	// а не часами. Ноль означает closed-loop: расписания нет.
	period time.Duration

	// Фазы соединения считаются в том же проходе. Отдельный проход по всем
	// результатам был возможен, только пока они где-то лежали.
	trace traceAcc
}

func NewAccumulator(rate float64) *Accumulator {
	return &Accumulator{
		sum: Summary{
			Codes:  make(map[int]int),
			Errors: make(map[ErrorKind]int),
		},
		service:   newDistribution(),
		corrected: newDistribution(),
		period:    schedulePeriod(rate),
	}
}

// Add учитывает один результат прогона.
func (a *Accumulator) Add(r runner.Result) {
	// Прогрев считаем, но нигде больше не учитываем: отброшенное молча —
	// способ потерять доверие к отчёту. Фазы соединения тоже мимо: именно
	// прогрев делает рукопожатия, и оставить их значило бы отчитаться о том,
	// что шапка назвала отброшенным.
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

	a.trace.add(r.Trace)

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
func (a *Accumulator) recordResponse(r runner.Result) {
	if isOK(r.StatusCode) {
		a.sum.OK++
	} else {
		a.sum.NonOK++
	}
	a.sum.Codes[r.StatusCode]++

	a.service.add(r.Duration)

	// В closed-loop Lag всегда ноль, и поправленные замеры до единого совпали
	// бы с исходными. Второй слайс на миллионы значений и вторая сортировка —
	// ради побайтовой копии; в Summary она делается присваиванием.
	if a.period > 0 {
		a.corrected.add(r.Lag + r.Duration)
	}
}

// recordFailure — ответа не было вовсе: таймаут до заголовков, отказ
// в соединении, сброс.
func (a *Accumulator) recordFailure(err error) {
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
func (a *Accumulator) recordTruncated(r runner.Result) {
	a.sum.Truncated++
	a.sum.Codes[r.StatusCode]++
	a.sum.Errors[Classify(r.Err)]++
}

// Summary досчитывает производные величины и добавляет то, что известно
// только по окончании прогона. Отдельно от Add, потому что считать это можно
// лишь когда виден весь прогон.
func (a *Accumulator) Summary(rep runner.Report) Summary {
	s := a.sum

	s.Elapsed = rep.Elapsed
	s.Window = rep.Window
	s.TargetRate = rep.TargetRate
	s.Partial = rep.Interrupted

	if s.Responses() > 0 {
		s.Latency = a.service.latencies()
		s.Histogram = a.service.histogram(histogramBuckets)

		// Без расписания поправлять нечего: Lag был нулём у каждого запроса.
		s.Corrected = s.Latency
		if a.period > 0 {
			s.Corrected = a.corrected.latencies()
		}
	}

	s.Trace = a.trace.summary()

	// Знаменатель — окно измерения, а не весь прогон: и запросы, и байты
	// в числителе посчитаны без прогрева.
	if s.Window > 0 {
		s.RPS = float64(s.Responses()) / s.Window.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / s.Window.Seconds()
	}
	return s
}
