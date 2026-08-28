// Перцентили, пояснение к прочеркам и гистограмма распределения.
package report

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Baklachok/loadgen/internal/stats"
)

func writeLatency(w io.Writer, s stats.Summary, opt Options, p palette) {
	// Порог — полученные ответы, а не 2xx: у прогона из одних 503 латентность
	// осмысленна и показывает, как быстро сервис отказывает.
	if s.Responses() == 0 {
		return
	}

	fmt.Fprintf(w, "\n%s\n", p.bold(fmt.Sprintf("Latency (время запроса, %d замеров)", s.Latency.Samples)))
	writeLatencies(w, s.Latency, p)

	if !opt.OpenLoop {
		return
	}

	// разница между блоками — и есть coordinated omission
	fmt.Fprintf(w, "\n%s\n", p.bold("Latency с поправкой на расписание"))
	writeLatencies(w, s.Corrected, p)
	fmt.Fprintf(w, "\n%s %v\n", p.dim("Макс. отставание старта:"), s.MaxLag.Round(time.Microsecond))
}

// Перцентили, на которые не набралось замеров, печатаются прочерком.
// Число вместо прочерка было бы максимумом под именем p99 — и читатель
// принял бы по нему решение.
func writeLatencies(w io.Writer, l stats.Latencies, p palette) {
	row := func(name, value string) {
		fmt.Fprintf(w, "  %s %s\n", p.dim(fmt.Sprintf("%-4s", name)), value)
	}
	dur := func(d time.Duration) string { return d.Round(time.Microsecond).String() }

	row("min", dur(l.Min))
	row("mean", dur(l.Mean))
	for _, q := range stats.Quantiles {
		if !l.Reliable(q.Q) {
			row(q.Name, p.dim("—"))
			continue
		}
		row(q.Name, dur(q.Value(l)))
	}
	row("max", dur(l.Max))

	writeSampleNote(w, l, p)
}

// writeSampleNote объясняет прочерки: без него они выглядят поломкой,
// а не отказом отвечать по недостатку данных.
func writeSampleNote(w io.Writer, l stats.Latencies, p palette) {
	var missing []string
	for _, q := range stats.Quantiles {
		if !l.Reliable(q.Q) {
			missing = append(missing, fmt.Sprintf("%s от %d", q.Name, stats.MinSamples(q.Q)))
		}
	}
	if len(missing) == 0 {
		return
	}

	fmt.Fprintf(w, "  %s\n", p.dim(fmt.Sprintf("замеров %d, прочерк — мало данных: %s",
		l.Samples, strings.Join(missing, ", "))))
}

// writeHistogram рисует столбики, растягивая самый высокий на всю оставшуюся
// ширину строки. Подписи считаются заранее, иначе бар не влезет и строка
// переносится, разваливая картинку.
func writeHistogram(w io.Writer, s stats.Summary, opt Options, p palette) {
	buckets, width := s.Histogram, opt.Width
	if len(buckets) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s\n", p.bold("Распределение"))

	labels := make([]string, len(buckets))
	counts := make([]string, len(buckets))
	labelW, countW, maxCount := 0, 0, 0

	for i, b := range buckets {
		labels[i] = b.Upper.Round(time.Microsecond).String()
		counts[i] = strconv.Itoa(b.Count)
		labelW = max(labelW, len(labels[i]))
		countW = max(countW, len(counts[i]))
		maxCount = max(maxCount, b.Count)
	}
	if maxCount == 0 {
		return
	}

	// «  <label> [<count>] » — всё, что занято не баром
	barW := width - labelW - countW - len("  ") - len(" [") - len("] ")
	if barW < 1 {
		barW = 1
	}

	for i, b := range buckets {
		// непустой бакет обязан быть виден: при линейной шкале и длинном хвосте
		// сотня замеров рядом с десятью тысячами округляется в ноль столбиков,
		// и «мало» становится неотличимо от «пусто»
		n := b.Count * barW / maxCount
		if n == 0 && b.Count > 0 {
			n = 1
		}
		bar := strings.Repeat("█", n)
		fmt.Fprintf(w, "  %*s [%*s] %s\n", labelW, labels[i], countW, counts[i], p.cyan(bar))
	}
}
