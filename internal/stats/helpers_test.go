// Общие для тестов пакета конструкторы: в них важен исход запроса,
// а не то, какие поля runner.Result заполнены.
package stats

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

// resp — сервер ответил, failed — ответа не было. Два конструктора вместо
// литералов runner.Result по всему файлу: в тестах важен исход, а не поля.

func resp(code int, d time.Duration) runner.Result {
	return runner.Result{Duration: d, StatusCode: code}
}

func failed(err error, d time.Duration) runner.Result {
	return runner.Result{Duration: d, Err: err}
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
	return Compute(runner.Report{Results: results, Elapsed: elapsed, Window: elapsed})
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
