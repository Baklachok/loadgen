package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func newTransport(cfg Config) *http.Transport {
	return &http.Transport{
		MaxIdleConns:        cfg.Concurrency,
		MaxIdleConnsPerHost: cfg.Concurrency,
		MaxConnsPerHost:     cfg.Concurrency,
		IdleConnTimeout:     90 * time.Second,

		DisableKeepAlives:  cfg.DisableKeepAlive,
		DisableCompression: true,

		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: cfg.Timeout,

		ForceAttemptHTTP2: cfg.HTTP2,
		//nolint:gosec // осознанный флаг --insecure
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure},
	}
}

func newClient(cfg Config, tr *http.Transport) *http.Client {
	return &http.Client{
		Timeout:   cfg.Timeout,
		Transport: tr,
		// Редиректы не проходим: мерить надо ответ сервера, а не то,
		// куда он перенаправил.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// requestFactory собирает запрос один раз и клонирует его на каждую отправку.
// Разбор URL и канонизация заголовков — работа постоянная для всего прогона,
// и ей нечего делать внутри замера: мы меряем сервер, а не свой парсер.
type requestFactory struct {
	tmpl *http.Request
}

func newRequestFactory(cfg Config) (*requestFactory, error) {
	var body io.Reader
	if len(cfg.Body) > 0 {
		body = bytes.NewReader(cfg.Body)
	}

	// Заодно проверяет метод и URL — один раз на прогон, а не на каждый запрос.
	req, err := http.NewRequest(cfg.Method, cfg.URL, body)
	if err != nil {
		return nil, err
	}

	for key, values := range cfg.Headers {
		if len(values) == 0 {
			continue
		}
		// Host — особый: net/http пишет его из req.Host и полностью игнорирует
		// одноимённый ключ в карте заголовков. Без этой ветки -H 'Host: ...'
		// молча уходит в никуда, и бенчмарк уезжает не на тот вирт-хост.
		if strings.EqualFold(key, "Host") {
			req.Host = values[0]
			continue
		}
		for _, v := range values {
			req.Header.Add(key, v)
		}
	}

	return &requestFactory{tmpl: req}, nil
}

// request возвращает копию шаблона с новым телом: один Reader дважды не прочитать.
func (f *requestFactory) request(ctx context.Context) (*http.Request, error) {
	req := f.tmpl.Clone(ctx)
	if f.tmpl.GetBody == nil {
		return req, nil
	}

	body, err := f.tmpl.GetBody()
	if err != nil {
		return nil, err
	}
	req.Body = body
	return req, nil
}
