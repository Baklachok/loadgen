package stats

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

// netTimeout — минимальная net.Error с истёкшим сроком: настоящую такую
// ошибку в тесте без сети не получить.
type netTimeout struct{}

func (netTimeout) Error() string   { return "i/o timeout" }
func (netTimeout) Timeout() bool   { return true }
func (netTimeout) Temporary() bool { return false }

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorKind
	}{
		{"отмена по Ctrl+C", context.Canceled, ErrCanceled},
		{"дедлайн контекста", context.DeadlineExceeded, ErrTimeout},
		{"соединение отвергнуто", syscall.ECONNREFUSED, ErrRefused},
		{"соединение сброшено", syscall.ECONNRESET, ErrReset},
		{"имя не разрешилось", &net.DNSError{Err: "no such host"}, ErrDNS},
		{"сетевой таймаут", netTimeout{}, ErrTimeout},
		{"сертификат не проверен", &tls.CertificateVerificationError{}, ErrTLS},
		{"что-то ещё", errors.New("boom"), ErrOtherKind},

		// net/http заворачивает ошибки в url.Error, поэтому errors.Is/As
		// обязаны работать сквозь обёртки — иначе всё уедет в «other».
		{"обёрнутый отказ", fmt.Errorf(`Get "http://x": %w`, syscall.ECONNREFUSED), ErrRefused},
		{"дважды обёрнутый дедлайн", fmt.Errorf("a: %w", fmt.Errorf("b: %w", context.DeadlineExceeded)), ErrTimeout},
		{"обёрнутый DNS", fmt.Errorf("lookup: %w", &net.DNSError{Err: "server misbehaving"}), ErrDNS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Errorf("Classify(%v) = %q, ожидалось %q", tt.err, got, tt.want)
			}
		})
	}
}

// Порядок проверок в Classify важен: у ошибки может подойти сразу несколько
// признаков, и выиграть должен самый конкретный.
func TestClassifyPrefersSpecificCause(t *testing.T) {
	// DNSError реализует net.Error и умеет быть таймаутом. Это всё равно
	// проблема с именем, а не с сетью, и в отчёте должна значиться как dns.
	dnsTimeout := &net.DNSError{Err: "timeout", IsTimeout: true}

	if got := Classify(dnsTimeout); got != ErrDNS {
		t.Errorf("Classify(DNS-таймаут) = %q, ожидалось %q: причина в резолве", got, ErrDNS)
	}
}

// Ошибка проверки сертификата приходит завёрнутой в url.Error — так её
// отдаёт http.Client, и именно в таком виде она попадает в статистику.
func TestClassifyWrappedTLS(t *testing.T) {
	err := fmt.Errorf(`Get "https://x": %w`, &tls.CertificateVerificationError{
		Err: x509.UnknownAuthorityError{},
	})

	if got := Classify(err); got != ErrTLS {
		t.Errorf("Classify = %q, ожидалось %q", got, ErrTLS)
	}
}

// Исчерпание ресурсов клиента раньше попадало в «other» наравне с чем угодно,
// хотя требует прямо противоположных действий: чинить надо у себя.
func TestClassifyClientExhaustion(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorKind
	}{
		// Так эти ошибки приходят из net/http на самом деле
		{"кончились дескрипторы процесса",
			&net.OpError{Op: "dial", Err: os.NewSyscallError("socket", syscall.EMFILE)}, ErrFDLimit},
		{"кончились дескрипторы системы",
			&net.OpError{Op: "dial", Err: os.NewSyscallError("socket", syscall.ENFILE)}, ErrFDLimit},
		{"кончились эфемерные порты",
			&net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.EADDRNOTAVAIL)}, ErrNoPorts},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.err)
			if got != tt.want {
				t.Errorf("Classify = %q, ожидалось %q", got, tt.want)
			}
			if !got.ClientSide() {
				t.Errorf("%q не помечен как проблема клиента", got)
			}
		})
	}
}

func TestClientSideOnlyForOurLimits(t *testing.T) {
	// Отказ в соединении и обрыв — про сервис или сеть, а не про наши лимиты
	for _, kind := range []ErrorKind{ErrTimeout, ErrRefused, ErrReset, ErrDNS, ErrTLS, ErrCanceled, ErrOtherKind} {
		if kind.ClientSide() {
			t.Errorf("%q ошибочно помечен как проблема клиента", kind)
		}
	}
}
