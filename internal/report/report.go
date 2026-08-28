// Package report отвечает только за представление: текст для человека,
// JSON для CI. Ничего не считает — все числа приходят готовыми из stats.
//
// Здесь публичная поверхность пакета и больше ничего: её импортирует cmd,
// и меняется она, когда у CLI появляется ручка, — не когда в отчёт
// добавляется блок.
package report

import (
	"time"

	"github.com/Baklachok/loadgen/internal/runner"
)

type Options struct {
	Color    bool // раскрашивать вывод ANSI-кодами
	Width    int  // ширина терминала: под неё масштабируется гистограмма
	OpenLoop bool // печатать блок с поправкой на расписание

	// Run описывает сам прогон. Отчёт живёт дольше, чем память о том, как
	// его получили, поэтому эти поля печатаются всегда.
	Run RunInfo
}

// RunInfo — всё, что нужно, чтобы повторить прогон через полгода.
type RunInfo struct {
	Version   string
	Config    runner.Config
	Proto     string // по чему реально договорились, а не что просили
	StartedAt time.Time
}
