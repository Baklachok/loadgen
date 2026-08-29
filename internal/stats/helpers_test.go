// Общие для тестов пакета конструкторы: в них важен исход запроса,
// а не то, какие поля runner.Result заполнены.
package stats

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// resp — сервер ответил целиком, failed — ответа не было вовсе,
// truncated — код пришёл, тело оборвалось. Конструкторы вместо литералов
// runner.Result по всему файлу: в тестах важен исход, а не поля.

func resp(code int, d time.Duration) runner.Result {
	return runner.Result{Duration: d, StatusCode: code}
}

func failed(err error, d time.Duration) runner.Result {
	return runner.Result{Duration: d, Err: err}
}

// truncated — заголовки с кодом пришли, тело оборвалось. Код и ошибка вместе
// и есть признак обрыва: так их выставляет runner.
func truncated(code int, err error, read int64) runner.Result {
	return runner.Result{StatusCode: code, Err: err, BytesRead: read}
}

func tenMs() []time.Duration {
	d := make([]time.Duration, 10)
	for i := range d {
		d[i] = time.Duration(i+1) * time.Millisecond
	}
	return d
}

// compute избавляет тесты от сборки runner.Report руками там, где окно
// измерения совпадает с длительностью прогона, — то есть везде без прогрева.
func compute(results []runner.Result, elapsed time.Duration) Summary {
	return computeReport(results, runner.Report{Elapsed: elapsed, Window: elapsed})
}

// computeReport прогоняет результаты через накопитель. Прогон их больше
// не хранит, поэтому кормить его приходится по одному — как в бою.
func computeReport(results []runner.Result, rep runner.Report) Summary {
	acc := NewAccumulator(rep.TargetRate)
	for _, r := range results {
		acc.Add(r)
	}
	return acc.Summary(rep)
}

// repeat — «сделать N одинаковых замеров». Встречалось дважды и двумя
// разными способами: через make с индексом и через append в цикле.
func repeat(n int, r runner.Result) []runner.Result {
	out := make([]runner.Result, n)
	for i := range out {
		out[i] = r
	}
	return out
}

// near — равенство с допуском в hdrTolerance: перцентили и среднее идут
// через HDR, и точного равенства от них ждать нельзя по устройству.
func near(got, want time.Duration) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return float64(d) <= float64(want)*hdrTolerance
}
