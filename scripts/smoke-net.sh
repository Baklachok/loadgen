#!/usr/bin/env bash
# Смоук через настоящую сеть: DNS, TLS, HTTP/2 и классификация ошибок — то,
# чего на loopback не бывает физически. Все прочие проверки проекта гоняются
# по 127.0.0.1, и до этого скрипта половина отчёта была доказана только тем,
# что печатает нули.
#
# Цели — публичные сервисы, созданные для проверки HTTP-клиентов. Режим —
# единичные запросы, не нагрузка: -n 3 -c 1 меньше, чем открытие их главной
# страницы в браузере. Наш же README про ответственное использование
# относится и к нам.
#
# Не в CI: полагаться на чужой аптайм в обязательной проверке нельзя.
# Недоступная цель — SKIP, не FAIL; провал по существу — FAIL.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin/loadgen"
[ -x "$BIN" ] || make -C "$ROOT" build >/dev/null

# Сеть медленная и чужая: таймаут с запасом, один срыв из трёх не провал.
ARGS=(-n 3 -c 1 -t 15s)
pass=0; fail=0; skip=0

reachable() { timeout 10 curl -sk -o /dev/null "$1" 2>/dev/null; }

# check печатает имя, гоняет цель и ищет шаблон в отчёте. Вывод сначала
# собирается в переменную, а не идёт в grep -q через пайп: grep -q закрывает
# пайп на первом совпадении, loadgen ловит SIGPIPE, и if видит ненулевой
# код timeout, а не совпадение — так первая версия плавала на проверке DNS.
# Имена — ASCII: printf %-Ns считает байты, и кириллица рвёт столбец.
check() {  # $1=имя  $2=url  $3=grep-шаблон  $4=reach|noreach  $5…=доп. флаги
  local name=$1 url=$2 want=$3 mode=$4; shift 4
  printf '  %-30s' "$name"
  if [ "$mode" = reach ] && ! reachable "$url"; then echo "SKIP (unreachable)"; skip=$((skip+1)); return; fi
  local out; out=$(timeout 60 "$BIN" "${ARGS[@]}" "$@" "$url" 2>&1 </dev/null)
  if grep -qE "$want" <<<"$out"; then echo "ok"; pass=$((pass+1))
  else echo "FAIL: no /$want/"; echo "$out" | sed 's/^/      /' | head -30; fail=$((fail+1)); fi
}

echo "network smoke:"
# 1. Настоящие DNS и TLS в трейсе — ненулевые, по одному замеру на соединение.
check "trace: DNS and TLS nonzero"  https://example.com/               'TLS handshake\s+[1-9]'   reach -trace
# 2. -http2 договаривается о h2 с сервером, который его умеет.
check "-http2 -> HTTP/2.0"          https://example.com/               'протокол\s+HTTP/2.0'    reach -http2
# 3. Самоподписанный сертификат без -insecure — ошибка tls, не other.
check "bad TLS -> error tls"        https://self-signed.badssl.com/    '^\s+tls\s+[1-9]'       reach
# 4. С -insecure тот же хост отвечает 200.
check "-insecure accepts bad TLS"   https://self-signed.badssl.com/    '^\s+200\s+[1-9]'       reach -insecure
# 5. Код ответа через сеть доезжает в разбивку.
check "503 over the network"        https://httpbin.org/status/503     '^\s+503\s+3'           reach
# 6. Несуществующее имя — настоящий DNSError -> dns; доступность не проверяем.
check "no such host -> dns"         https://nonexistent-loadgen-probe.invalid/ '^\s+dns\s+3'   noreach
# 7. Редирект не проходится: 302 остаётся 302.
check "redirect not followed"       https://http2.golang.org/          '^\s+302\s+[1-9]'       reach -http2

echo "total: ok=$pass fail=$fail skip=$skip"
[ $fail -eq 0 ]
