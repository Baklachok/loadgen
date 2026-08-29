package report

import (
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/prom"

	"github.com/Baklachok/loadgen/internal/stats"
)

// Renderer — стратегия вывода. Форматов пока два, но выбор между ними
// делается один раз при разборе флагов, а не тремя сравнениями строки
// по ходу main: раньше «text» проверялся отдельно для валидации, отдельно
// для шапки и отдельно для отчёта, и добавление формата означало не забыть
// про все три.
type Renderer interface {
	// Header печатается до прогона. В машинных форматах его быть не должно:
	// на stdout обязан оказаться только разбираемый документ.
	Header(w io.Writer, opt Options) error

	Render(w io.Writer, s stats.Summary, opt Options) error
}

// NewRenderer заодно валидирует значение -o: неизвестный формат больше
// негде проглядеть.
func NewRenderer(format string) (Renderer, error) {
	switch format {
	case "text":
		return textRenderer{}, nil
	case "json":
		return jsonRenderer{}, nil
	case "prom":
		return promRenderer{}, nil
	}
	return nil, fmt.Errorf("неизвестный формат вывода %q, доступны text, json и prom", format)
}

type textRenderer struct{}

func (textRenderer) Header(w io.Writer, opt Options) error {
	ew := &errWriter{w: w}
	writeHeader(ew, opt)
	return ew.err
}

func (textRenderer) Render(w io.Writer, s stats.Summary, opt Options) error {
	ew := &errWriter{w: w}
	writeReport(ew, s, opt)
	return ew.err
}

type jsonRenderer struct{}

// Header ничего не печатает: любая лишняя строка на stdout ломает `| jq`.
func (jsonRenderer) Header(io.Writer, Options) error { return nil }

func (jsonRenderer) Render(w io.Writer, s stats.Summary, opt Options) error {
	return writeJSON(w, s, opt)
}

// promRenderer — текстовый формат экспозиции Prometheus, для Pushgateway
// или textfile-collector. Header пустой по той же причине, что у JSON:
// на stdout обязан оказаться только документ.
type promRenderer struct{}

func (promRenderer) Header(io.Writer, Options) error { return nil }

func (promRenderer) Render(w io.Writer, s stats.Summary, _ Options) error {
	return prom.Write(w, s)
}
