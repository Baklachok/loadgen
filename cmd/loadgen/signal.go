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

	go func() {
		<-signals
		fmt.Fprintln(stderr, "\nостановка, собираю результаты… (ещё раз Ctrl+C — выйти немедленно)")
		cancel()

		// Без этого зависший запрос делает Ctrl+C бесполезным, и пользователь
		// уходит в kill -9, теряя все собранные результаты.
		<-signals
		fmt.Fprintln(stderr, "прервано")
		exit(exitInterrupt)
	}()

	return ctx, func() {
		signal.Stop(signals)
		cancel()
	}
}
