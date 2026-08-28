// Остановка по сигналу.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

// interruptible возвращает контекст, отменяемый по Ctrl+C.
//
// Свой канал вместо signal.NotifyContext: его stop() отменяет контекст, и
// наблюдатель просыпался при обычном завершении — сообщение об остановке
// печаталось на каждом успешном прогоне.
// exit передаётся параметром, а не берётся как os.Exit: второй сигнал
// обязан обрывать процесс, и проверить это можно только подменив выход.
func interruptible(stderr io.Writer, exit func(int)) (context.Context, func()) {
	// Буфер на два: пока печатаем и отменяем, второй Ctrl+C не должен потеряться.
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())

	// done будит наблюдателя на обычном выходе. Без него горутина остаётся
	// висеть на <-signals навсегда: signal.Stop отписывает канал, но никого
	// не будит. В бою это безобидно — процесс тут же завершается, — а в тестах
	// каждый прогон оставлял припаркованную горутину, и goleak на ней и упёрся.
	done := make(chan struct{})

	go func() {
		select {
		case <-signals:
		case <-done:
			return
		}
		fmt.Fprintln(stderr, "\nостановка, собираю результаты… (ещё раз Ctrl+C — выйти немедленно)")
		cancel()

		// Без этого зависший запрос делает Ctrl+C бесполезным, и пользователь
		// уходит в kill -9, теряя все собранные результаты.
		select {
		case <-signals:
		case <-done:
			return
		}
		fmt.Fprintln(stderr, "прервано")
		exit(exitInterrupt)
	}()

	// Вызывается ровно один раз, через defer в run. Повторный вызов дал бы
	// панику на втором close — sync.Once ради несуществующего сценария не берём.
	return ctx, func() {
		signal.Stop(signals)
		close(done)
		cancel()
	}
}
