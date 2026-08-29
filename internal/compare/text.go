// Печать таблицы сравнения.
//
// Без цвета: palette в report неэкспортирована, а расширять публичную
// поверхность пакета ради галочек — дорого за косметику.
package compare

import (
	"fmt"
	"io"
	"strings"
)

const noData = "—"

const (
	labelWidth = 16
	valueWidth = 14
)

// Write печатает таблицу. Без цвета: palette в report неэкспортирована,
// а расширять публичную поверхность пакета ради галочек — дорого за
// косметику.
func (r Result) Write(w io.Writer) {
	fmt.Fprintf(w, "%s\n", r.headline())
	fmt.Fprintf(w, "\n%-*s %*s %*s   %s\n", labelWidth, "", valueWidth, "было", valueWidth, "стало", "изменение")

	for _, row := range r.Rows {
		mark := ""
		if row.Regressed {
			mark = "  ✗"
		}
		fmt.Fprintf(w, "%-*s %*s %*s   %s%s\n",
			labelWidth, row.Label, valueWidth, row.Before, valueWidth, row.After, row.Change, mark)
	}
}

// headline говорит, на скольких прогонах всё держится. Строка обязательная:
// по двум одиночным прогонам разницу в проценты интерпретировать нельзя,
// и умолчать об этом значит выдать анекдот за измерение.
func (r Result) headline() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Сравнение: %s против %s\n", r.Before.Name, r.After.Name)

	switch {
	case r.Before.Runs == 1 && r.After.Runs == 1:
		b.WriteString("По одному прогону с каждой стороны — это анекдот, а не измерение.\n")
		b.WriteString("Три прогона в каталог с каждой стороны дадут медиану.")
	default:
		fmt.Fprintf(&b, "Медиана %d прогонов против %d.", r.Before.Runs, r.After.Runs)
	}
	return b.String()
}
