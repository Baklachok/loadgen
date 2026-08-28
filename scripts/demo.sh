#!/usr/bin/env bash
# Пересобирает демо-гифку для README одной командой.
#
# Раньше запись была ручной: поднять сервер, включить asciinema, набрать две
# команды. Из-за этого гифка отставала от отчёта — строку «план» она пропустила
# на восемь коммитов. Здесь всё воспроизводимо, включая сервер.
set -euo pipefail

command -v asciinema >/dev/null || { echo "нужен asciinema: apt install asciinema" >&2; exit 1; }
command -v agg >/dev/null || { echo "нужен agg: https://github.com/asciinema/agg/releases" >&2; exit 1; }

ROOT=$(cd "$(dirname "$0")/.." && pwd)
TMP=$(mktemp -d)
SRV_PID=""
trap '[ -n "$SRV_PID" ] && kill "$SRV_PID" 2>/dev/null; rm -rf "$TMP"' EXIT

# Версию задаём явно: в рабочей копии git describe даёт хвост с хешем и -dirty,
# а на картинке должно быть то, что увидит человек с релизного бинарника.
make -C "$ROOT" build VERSION="${VERSION:-$(git -C "$ROOT" describe --tags --abbrev=0)}" >/dev/null

# Сервер из README: отвечает за 5 мс, но раз в две секунды замирает на 400.
# Ровно на нём видно разницу closed- и open-loop, ради которой демо и снимается.
mkdir -p "$TMP/srv"
cat > "$TMP/srv/main.go" <<'GO'
package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

func main() {
	var mu sync.RWMutex
	go func() {
		for {
			time.Sleep(2 * time.Second)
			mu.Lock()
			time.Sleep(400 * time.Millisecond)
			mu.Unlock()
		}
	}()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.RLock()
		defer mu.RUnlock()
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, "ok")
	})
	panic(http.ListenAndServe("127.0.0.1:8080", nil))
}
GO
printf 'module demosrv\n\ngo 1.22\n' > "$TMP/srv/go.mod"
(cd "$TMP/srv" && go build -o srv .)
"$TMP/srv/srv" & SRV_PID=$!

for _ in $(seq 100); do
	(exec 3<>/dev/tcp/127.0.0.1/8080) 2>/dev/null && break
	sleep 0.05
done

# Сценарий записи. Команда «набирается» посимвольно: без этого кадр
# с готовой строкой возникает мгновенно и читается как склейка.
cat > "$TMP/session.sh" <<SESSION
export PATH="$ROOT/bin:\$PATH"
type_and_run() {
	printf '\$ '
	for (( i = 0; i < \${#1}; i++ )); do printf '%s' "\${1:i:1}"; sleep 0.03; done
	printf '\n'
	eval "\$1"
	printf '\n'
}
sleep 0.5
type_and_run 'loadgen -z 3s -c 20 http://localhost:8080'
sleep 1
type_and_run 'loadgen -z 3s -rate 2500 -c 400 http://localhost:8080'
sleep 1.5
SESSION

# Ширина 100 обязательна: отчёт под неё свёрстан, на 80 бары схлопываются
# с 81 блока до 60. Высота 50 вмещает closed-loop отчёт целиком — пересматривать
# при каждом новом блоке в отчёте.
asciinema rec --overwrite --cols 100 --rows 50 \
	-c "bash $TMP/session.sh" "$TMP/demo.cast" </dev/null >/dev/null 2>&1

agg --idle-time-limit 1 "$TMP/demo.cast" "$ROOT/assets/demo.gif" >/dev/null 2>&1

echo "assets/demo.gif обновлён: $(stat -c%s "$ROOT/assets/demo.gif") байт"
