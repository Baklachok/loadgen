package main

import (
	"fmt"
	"net/http"
	"strings"
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
