package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type headerFlag struct {
	h http.Header
}

func (f *headerFlag) String() string {
	if f.h == nil {
		return ""
	}
	parts := make([]string, 0, len(f.h))
	for k, vs := range f.h {
		for _, v := range vs {
			parts = append(parts, k+": "+v)
		}
	}
	return strings.Join(parts, ", ")
}

func (f *headerFlag) Set(value string) error {
	k, v, ok := strings.Cut(value, ":")
	if !ok {
		return fmt.Errorf("заголовок должен быть в формате 'Key: Value', получено %q", value)
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)
	if k == "" {
		return fmt.Errorf("пустое имя заголовка в %q", value)
	}
	if f.h == nil {
		f.h = make(http.Header)
	}
	f.h.Add(k, v)
	return nil
}

// warmupFlag принимает либо длительность («5s»), либо число запросов («100»).
// Формы различимы однозначно: time.ParseDuration требует единицы измерения,
// поэтому «100» ей не подходит, а «5s» не подходит strconv.Atoi.
type warmupFlag struct {
	requests int
	duration time.Duration
}

func (f *warmupFlag) String() string {
	switch {
	case f.duration > 0:
		return f.duration.String()
	case f.requests > 0:
		return strconv.Itoa(f.requests)
	}
	return ""
}

func (f *warmupFlag) Set(value string) error {
	// Повторный флаг перетирает предыдущий целиком, как это делают обычные
	// флаги: иначе «-warmup 5s -warmup 100» выставит обе формы разом.
	if d, err := time.ParseDuration(value); err == nil {
		f.duration, f.requests = d, 0
		return nil
	}
	if n, err := strconv.Atoi(value); err == nil {
		f.requests, f.duration = n, 0
		return nil
	}
	return fmt.Errorf("прогрев %q: ожидается длительность (5s) или число запросов (100)", value)
}
