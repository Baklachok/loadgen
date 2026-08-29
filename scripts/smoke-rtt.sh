#!/usr/bin/env bash
# Прогон под настоящим RTT — на этой же машине. tc netem на loopback: та же
# очередь ядра, тот же путь пакета, только с задержкой, джиттером и потерями.
# Даёт то, чего не даёт ни голый loopback, ни внешние сайты в режиме смоука:
# Lag в open-loop под RTT, потолок closed-loop по формуле Литтла, хвост
# ретрансмитов от потерь.
#
# Требует sudo: qdisc на lo замедляет ВЕСЬ loopback машины, пока висит.
# Снимается в любом случае через trap. Не в CI.
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
BIN="$ROOT/bin/loadgen"
[ -x "$BIN" ] || make -C "$ROOT" build >/dev/null
command -v tc >/dev/null || { echo "нужен tc (iproute2)" >&2; exit 1; }
sudo -n true 2>/dev/null || { echo "нужен sudo без пароля: netem ставится на lo" >&2; exit 1; }

DELAY=${DELAY:-20ms}; JITTER=${JITTER:-5ms}; LOSS=${LOSS:-0.5%}
STUB_PID=""; TMP=$(mktemp -d)
cleanup() { sudo tc qdisc del dev lo root 2>/dev/null; [ -n "$STUB_PID" ] && kill "$STUB_PID" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT

mkdir -p "$TMP/stub"; cat > "$TMP/stub/main.go" <<'GO'
package main

import ("fmt"; "net"; "net/http")

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { panic(err) }
	fmt.Println(ln.Addr().String())
	panic(http.Serve(ln, nil))
}
GO
printf 'module stub\n\ngo 1.22\n' > "$TMP/stub/go.mod"
(cd "$TMP/stub" && go build -o stub .) || exit 1
"$TMP/stub/stub" > "$TMP/addr" & STUB_PID=$!
for _ in $(seq 100); do [ -s "$TMP/addr" ] && break; sleep 0.05; done
ADDR=$(cat "$TMP/addr")

fail=0
say() { printf '  %-40s %s\n' "$1" "$2"; }
expect() { if [ "$2" -eq 1 ] 2>/dev/null || [ "$2" = true ]; then say "$1" ok; else say "$1" "FAIL ($3)"; fail=$((fail+1)); fi; }

sudo tc qdisc add dev lo root netem delay "$DELAY" "$JITTER" loss "$LOSS" || exit 1
rtt=$(ping -c 5 -q 127.0.0.1 | awk -F/ '/rtt/{print $5}')
echo "netem: delay $DELAY ±$JITTER loss $LOSS → RTT ${rtt} ms"

# closed-loop: потолок ограничен RTT по формуле Литтла, c / RTT.
"$BIN" -z 6s -c 50 -trace -o json "http://$ADDR/" > "$TMP/closed.json" 2>/dev/null </dev/null
rps=$(jq '.rps' "$TMP/closed.json"); little=$(awk -v r="$rtt" 'BEGIN{printf "%d", 50/(r/1000)}')
expect "closed-loop RPS within 80% of c/RTT=$little" "$(awk -v a="$rps" -v b="$little" 'BEGIN{print (a > 0.8*b && a <= 1.05*b) ? 1 : 0}')" "rps=$rps"
p50=$(jq '.latency.p50_ms' "$TMP/closed.json")
expect "closed-loop p50 ≈ RTT ($rtt)" "$(awk -v p="$p50" -v r="$rtt" 'BEGIN{print (p > 0.8*r && p < 1.5*r) ? 1 : 0}')" "p50=$p50"
tail=$(jq '[.histogram[] | select(.upper_ms > 200) | .count] | add // 0' "$TMP/closed.json")
expect "retransmit tail >200ms present (loss $LOSS)" "$(awk -v t="$tail" 'BEGIN{print (t > 0) ? 1 : 0}')" "tail=$tail"
conn=$(jq '.trace.connect.p50_ms' "$TMP/closed.json")
expect "trace: TCP connect ≈ RTT" "$(awk -v c="$conn" -v r="$rtt" 'BEGIN{print (c > 0.8*r) ? 1 : 0}')" "connect=$conn"

# open-loop: расписание держится, Lag ненулевой но малый, поправка ≥ latency.
"$BIN" -z 6s -rate 300 -c 200 -o json "http://$ADDR/" > "$TMP/open.json" 2>/dev/null </dev/null
orps=$(jq '.rps' "$TMP/open.json")
expect "open-loop holds rate 300 within 5%" "$(awk -v a="$orps" 'BEGIN{print (a > 285) ? 1 : 0}')" "rps=$orps"
lag=$(jq '.max_lag_ms' "$TMP/open.json")
expect "open-loop max_lag nonzero, < RTT" "$(awk -v l="$lag" -v r="$rtt" 'BEGIN{print (l > 0 && l < r) ? 1 : 0}')" "lag=$lag"
cp99=$(jq '.corrected.p99_ms' "$TMP/open.json"); p99=$(jq '.latency.p99_ms' "$TMP/open.json")
expect "corrected p99 ≥ p99" "$(awk -v c="$cp99" -v p="$p99" 'BEGIN{print (c >= p) ? 1 : 0}')" "$cp99 < $p99"

echo "total: fail=$fail"
[ $fail -eq 0 ]
