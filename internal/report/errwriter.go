package report

import "io"

// errWriter — декоратор над io.Writer, запоминающий первую ошибку записи
// и глухой ко всему после неё.
//
// Текстовый отчёт делает три десятка Fprintf. Проверять каждый — утопить
// рендеринг в обработке ошибок; не проверять ни одного — тихо расходиться
// с JSON, который об ошибках сообщает. Декоратор снимает выбор: вызывающие
// пишут как писали, ошибка спрашивается один раз в конце.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	if e.err != nil {
		return 0, e.err
	}

	n, err := e.w.Write(p)
	e.err = err
	return n, err
}
