#!/usr/bin/env bash
# Смоук: собрать бинарник и убедиться, что он работает как программа, а не
# как набор пакетов. Юнит-тесты зовут run() внутри процесса — здесь проверяются
# сборка, разбор флагов, коды выхода настоящего процесса и сериализация.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
STUB_PID=""
trap '[ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null; rm -rf "$TMP"' EXIT

fail() { echo "смоук провален: $*" >&2; exit 1; }

go build -o "$TMP/loadgen" "$ROOT/cmd/loadgen"

# Заглушка печатает свой адрес: порт не задаётся руками, поэтому смоук
# не может столкнуться с чужим слушателем и не ждёт вслепую.
mkdir -p "$TMP/stub"
cat > "$TMP/stub/main.go" <<'GO'
package main

import (
	"fmt"
	"net"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	http.HandleFunc("/fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println(ln.Addr().String())
	panic(http.Serve(ln, nil))
}
GO
printf 'module stub\n\ngo 1.22\n' > "$TMP/stub/go.mod"
(cd "$TMP/stub" && go build -o stub .)

"$TMP/stub/stub" > "$TMP/addr" &
STUB_PID=$!
for _ in $(seq 100); do [ -s "$TMP/addr" ] && break; sleep 0.05; done
ADDR=$(cat "$TMP/addr") || fail "заглушка не поднялась"
[ -n "$ADDR" ] || fail "заглушка не напечатала адрес"

# 1. Прогон целиком: флаги, запросы, JSON, код выхода.
out=$("$TMP/loadgen" -n 100 -c 10 -o json "http://$ADDR/" 2>/dev/null) \
  || fail "обычный прогон вышел с кодом $?"
echo "$out" | grep -qE '"ok": *100' \
  || fail "ожидалось 100 успешных, получено: $(echo "$out" | grep -oE '"ok": *[0-9]+')"

# 2. Контракт кодов выхода на настоящем процессе: сервис отдаёт одни 500,
#    порог нулевой — прогон обязан вернуть 3, а не 0.
code=0
"$TMP/loadgen" -n 20 -c 5 -slo-error-rate 0 "http://$ADDR/fail" >/dev/null 2>&1 || code=$?
[ "$code" -eq 3 ] || fail "нарушенный SLO дал код $code, ожидался 3"

# 3. Заметный прогон по своей машине не должен требовать подтверждения:
#    ошибиться здесь легко, а цена — сломанный смоук у каждого, кто гоняет
#    локально. stdin закрыт намеренно — как в CI.
"$TMP/loadgen" -n 20000 -c 50 "http://$ADDR/" >/dev/null 2>&1 </dev/null \
  || fail "заметный прогон по localhost потребовал подтверждения (код $?)"

echo "смоук пройден: прогон, код выхода по SLO, localhost без подтверждения"
