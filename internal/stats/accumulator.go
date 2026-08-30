// Сборка сводки из потока результатов. Отдельно от stats.go: там описано,
// что прогон дал, здесь — как это считается. Последние правки меняли ровно
// одно из двух: переход на потоковый приём переписал накопитель целиком
// и не тронул ни одного поля Summary.
package stats

import (
	"maps"
	"sync"
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

// schedulePeriod — сколько времени отведено расписанием на один запрос.
// Не меньше наносекунды: выше 1e9 RPS деление округляется в ноль, и
// «расписание есть» превращалось бы в «нет» — поправка молча исчезала.
func schedulePeriod(rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	return max(time.Nanosecond, time.Duration(float64(time.Second)/rate))
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
	// mu защищает всё ниже. Add идёт из одной горутины и мьютекс ему
	// не нужен был — пока не появился /metrics, читающий по ходу прогона
	// из горутины HTTP-сервера. Незанятый мьютекс на запрос не измерим
	// на фоне сетевого вызова.
	mu sync.Mutex

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
	a.mu.Lock()
	defer a.mu.Unlock()

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

// Snapshot — сводка на текущий момент, безопасная для чтения из другой
// горутины. Единственный способ смотреть на прогон, пока он идёт.
//
// Копия глубокая: Codes и Errors клонируются. Копия заголовка map делила бы
// содержимое с Add — ровно та гонка, которую тест поймал до этой правки.
// Latencies и Bucket — чистые значения, им клон не нужен.
func (a *Accumulator) Snapshot() Summary {
	a.mu.Lock()
	defer a.mu.Unlock()

	s := a.sum
	s.Codes = maps.Clone(a.sum.Codes)
	s.Errors = maps.Clone(a.sum.Errors)

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
	return s
}

// Summary — итог прогона: снимок плюс то, что известно только по окончании.
// Отдельно от Snapshot, потому что окно измерения и частота считаются лишь
// когда виден весь прогон.
func (a *Accumulator) Summary(rep runner.Report) Summary {
	s := a.Snapshot()

	s.Elapsed = rep.Elapsed
	s.Window = rep.Window
	s.TargetRate = rep.TargetRate
	s.Partial = rep.Interrupted

	// Знаменатель — окно измерения, а не весь прогон: и запросы, и байты
	// в числителе посчитаны без прогрева.
	if s.Window > 0 {
		s.RPS = float64(s.Responses()) / s.Window.Seconds()
		s.Throughput = float64(s.BytesRead) / (1024 * 1024) / s.Window.Seconds()
	}
	return s
}
