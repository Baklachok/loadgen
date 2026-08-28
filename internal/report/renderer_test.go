package report

import (
	"errors"
	"strings"
	"testing"

	"github.com/Baklachok/loadgen/internal/runner"
)

func TestNewRenderer(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			if _, err := NewRenderer(format); err != nil {
				t.Errorf("NewRenderer(%q): %v", format, err)
			}
		})
	}

	t.Run("неизвестный формат отвергается", func(t *testing.T) {
		_, err := NewRenderer("yaml")
		if err == nil {
			t.Fatal("неизвестный формат принят")
		}
		if !strings.Contains(err.Error(), "yaml") {
			t.Errorf("в ошибке нет самого формата: %v", err)
		}
	})
}

// На stdout в машинном режиме обязан оказаться только разбираемый документ:
// одна лишняя строка шапки ломает `| jq`.
func TestJSONRendererPrintsNoHeader(t *testing.T) {
	var buf strings.Builder
	opt := Options{Run: RunInfo{Config: runner.Config{URL: "http://example", Requests: 10, Concurrency: 2}}}

	if err := (jsonRenderer{}).Header(&buf, opt); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("json-рендерер напечатал шапку: %q", buf.String())
	}
}

func TestTextRendererPrintsHeader(t *testing.T) {
	var buf strings.Builder
	opt := Options{Run: RunInfo{Config: runner.Config{URL: "http://example", Requests: 10, Concurrency: 2}}}

	if err := (textRenderer{}).Header(&buf, opt); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "http://example") {
		t.Errorf("в шапке нет цели: %q", buf.String())
	}
}

// failingWriter отказывает после n успешных записей.
type failingWriter struct {
	ok  int
	err error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.ok == 0 {
		return 0, f.err
	}
	f.ok--
	return len(p), nil
}

// Раньше текстовый отчёт молча игнорировал ошибки записи, а JSON о них
// сообщал. Оборванный пайп выглядел как успешный прогон.
func TestTextRendererReportsWriteFailure(t *testing.T) {
	boom := errors.New("сломанный пайп")
	w := &failingWriter{ok: 2, err: boom}

	err := (textRenderer{}).Render(w, sample(), Options{Width: 80})
	if !errors.Is(err, boom) {
		t.Errorf("Render вернул %v, ожидалась %v", err, boom)
	}
}

func TestErrWriterKeepsFirstError(t *testing.T) {
	first := errors.New("первая")
	ew := &errWriter{w: &failingWriter{ok: 0, err: first}}

	_, _ = ew.Write([]byte("a"))
	_, _ = ew.Write([]byte("b"))

	if !errors.Is(ew.err, first) {
		t.Errorf("сохранена ошибка %v, ожидалась %v", ew.err, first)
	}
}
