package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

type Result struct {
	Duration   time.Duration
	StatusCode int
	Err        error
	BytesRead  int64
}

func doRequest(client *http.Client, url string) Result {
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return Result{Duration: time.Since(start), Err: err}
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body) // важно: дочитать тело!
	return Result{Duration: time.Since(start), StatusCode: resp.StatusCode, BytesRead: n}
}

func main() {
	// Настройки для проверки
	const url = "http://localhost:8000"
	const n = 10                       // Количество запросов

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	var totalDuration time.Duration
	var successCount int

	fmt.Printf("Запуск %d последовательных запросов к %s...\n\n", n, url)

	for i := 1; i <= n; i++ {
		res := doRequest(client, url)

		if res.Err != nil {
			fmt.Printf("Запрос %d: Ошибка: %v\n", i, res.Err)
			continue
		}

		fmt.Printf("Запрос %d: Статус %d | Время: %v | Прочитано байт: %d\n",
			i, res.StatusCode, res.Duration, res.BytesRead)

		totalDuration += res.Duration
		successCount++
	}

	if successCount > 0 {
		averageDuration := totalDuration / time.Duration(successCount)
		fmt.Printf("\nУспешно выполнено: %d/%d\n", successCount, n)
		fmt.Printf("Среднее время запроса: %v\n", averageDuration)
	} else {
		fmt.Println("\nВсе запросы завершились ошибкой.")
	}
}
