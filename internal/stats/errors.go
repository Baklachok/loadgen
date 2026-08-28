// Классификация ошибок запроса: превращает разнородные ошибки Go
// в короткий набор причин, понятный в отчёте.
package stats

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"syscall"
)

type ErrorKind string

const (
	ErrTimeout   ErrorKind = "timeout"
	ErrRefused   ErrorKind = "connection refused"
	ErrReset     ErrorKind = "connection reset"
	ErrDNS       ErrorKind = "dns"
	ErrTLS       ErrorKind = "tls"
	ErrCanceled  ErrorKind = "canceled"
	ErrOtherKind ErrorKind = "other"

	// Тело оборвалось, не дойдя до обещанной длины. Отдельно от «other»,
	// потому что причина конкретная и действие по ней другое: ответ был,
	// смотреть надо на код и на то, почему сервер рвёт соединения.
	ErrTruncated ErrorKind = "truncated body"

	// Ошибки, означающие, что кончились ресурсы у нас, а не у сервиса.
	// До этой правки они попадали в «other» наравне с чем угодно, хотя
	// требуют прямо противоположных действий.
	ErrFDLimit ErrorKind = "too many open files"
	ErrNoPorts ErrorKind = "no ephemeral ports"
)

// ClientSide — причина в самом генераторе или его ОС. Такие ошибки означают,
// что прогон измерил наш потолок, а не поведение сервиса, и остальные цифры
// описывают не то, ради чего затевался тест.
func (k ErrorKind) ClientSide() bool {
	return k == ErrFDLimit || k == ErrNoPorts
}

func Classify(err error) ErrorKind {
	switch {
	case errors.Is(err, context.Canceled):
		return ErrCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return ErrTimeout
	case errors.Is(err, syscall.ECONNREFUSED):
		return ErrRefused
	case errors.Is(err, syscall.ECONNRESET):
		return ErrReset

	// Только ErrUnexpectedEOF: голый io.EOF транспорт отдаёт, когда сервер
	// закрыл соединение, не ответив вовсе, — это «без ответа», а не обрыв тела.
	case errors.Is(err, io.ErrUnexpectedEOF):
		return ErrTruncated

	// EMFILE — предел процесса, ENFILE — предел системы; для отчёта это
	// одно и то же: дескрипторы кончились.
	case errors.Is(err, syscall.EMFILE), errors.Is(err, syscall.ENFILE):
		return ErrFDLimit
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return ErrNoPorts
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return ErrDNS
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ErrTimeout
	}

	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return ErrTLS
	}

	return ErrOtherKind
}
