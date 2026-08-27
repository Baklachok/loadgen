package report

import (
	"fmt"
	"io"

	"github.com/Baklachok/loadgen/internal/runner"
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
	Header(w io.Writer, cfg runner.Config, opt Options) error

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
	}
	return nil, fmt.Errorf("неизвестный формат вывода %q, доступны text и json", format)
}

type textRenderer struct{}

func (textRenderer) Header(w io.Writer, cfg runner.Config, opt Options) error {
	ew := &errWriter{w: w}
	writeHeader(ew, cfg, opt)
	return ew.err
}

func (textRenderer) Render(w io.Writer, s stats.Summary, opt Options) error {
	ew := &errWriter{w: w}
	writeReport(ew, s, opt)
	return ew.err
}

type jsonRenderer struct{}

// Header ничего не печатает: любая лишняя строка на stdout ломает `| jq`.
func (jsonRenderer) Header(io.Writer, runner.Config, Options) error { return nil }

func (jsonRenderer) Render(w io.Writer, s stats.Summary, opt Options) error {
	return writeJSON(w, s, opt)
}
