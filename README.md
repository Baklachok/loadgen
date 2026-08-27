# loadgen

[![CI](https://github.com/Baklachok/loadgen/actions/workflows/ci.yml/badge.svg)](https://github.com/Baklachok/loadgen/actions/workflows/ci.yml)

Нагрузочный тестер HTTP на Go. В отличие от большинства самописных — не врёт про хвост латенси.

![демо](assets/demo.gif)

## Зачем ещё один

**Closed-loop** (`-c 20`) — пул воркеров, каждый шлёт следующий запрос после ответа на предыдущий: частоту диктует сервер.

**Open-loop** (`-rate 2500`) — запросы уходят по расписанию, не дожидаясь ответов, а latency считается от запланированного момента отправки.

Разница на сервере, который отвечает за 5 мс, но раз в 2 секунды замирает на 400 мс:

| | closed-loop `-c 20` | open-loop `-rate 2500 -c 400` |
|---|---|---|
| RPS | 2143 | 2484 |
| p50 | 7.21 мс | 6.45 мс |
| p90 | 10.41 мс | 49.05 мс |
| p95 | 12.06 мс | 317.02 мс |
| **p99** | **16.60 мс** | **409.31 мс** |

Сервер лежал пятую часть прогона, а closed-loop показал p99 = 16.6 мс: пока сервер стоял, пул тоже стоял и не отправлял запросы, так что в статистику попали лишь 20 штук, бывших в полёте. Это coordinated omission — им страдают `ab`, `jmeter` и обычный `wrk`.

## Установка

```bash
go install github.com/Baklachok/loadgen/cmd/loadgen@latest
```

Из исходников:

```bash
git clone https://github.com/Baklachok/loadgen && cd loadgen
go build -ldflags "-X main.version=$(git describe --tags --always --dirty)" -o bin/loadgen ./cmd/loadgen
```

## Использование

```bash
loadgen -n 1000 -c 50 http://localhost:8080             # 1000 запросов в 50 потоков
loadgen -z 30s -rate 500 -c 200 http://localhost:8080   # 30 секунд по 500 RPS
loadgen -n 1000 -o json http://localhost:8080 | jq      # для CI

loadgen -m POST -d '{"a":1}' -H 'Content-Type: application/json' http://localhost:8080/api
```

`Ctrl+C` останавливает прогон и печатает частичные результаты. Цвета гаснут сами при выводе в пайп или файл, принудительно — `NO_COLOR=1`.

| Флаг | По умолчанию | Смысл |
|---|---|---|
| `-n` | 200 | количество запросов |
| `-z` | — | длительность прогона, взаимоисключающе с `-n` |
| `-c` | 50 | конкурентность; в open-loop — потолок запросов в полёте |
| `-rate` | 0 | постоянный RPS, включает open-loop |
| `-m` | GET | HTTP-метод |
| `-d` | — | тело запроса |
| `-H` | — | заголовок `'Key: Value'`, можно несколько раз |
| `-t` | 10s | таймаут запроса |
| `-o` | text | формат вывода: `text` или `json` |
| `-insecure` | false | не проверять TLS-сертификат |
| `-disable-keepalive` | false | новое соединение на каждый запрос |
| `-http2` | false | разрешить HTTP/2 |
| `-version` | | показать версию |

### JSON

Длительности в миллисекундах. Блоки `corrected` и `max_lag_ms` появляются только в open-loop, где расписание действительно было.

```bash
$ loadgen -z 5s -rate 500 -c 200 -o json http://localhost:8080 \
    | jq -c '{rps: (.rps*10|round/10), p99: .latency.p99_ms,
              corrected: .corrected.p99_ms, lag: .max_lag_ms}'
{"rps":490.5,"p99":390.875,"corrected":392.052,"lag":19.627}
```

## Как устроен

```
cmd/loadgen/      разбор флагов, больше ничего
internal/runner/  движок: пул воркеров (closed-loop) и планировщик (open-loop)
internal/stats/   агрегация: перцентили, классификация ошибок, бакеты гистограммы
internal/report/  представление: текст с цветами и гистограммой, JSON для CI
```

## Разработка

```bash
make build    # bin/loadgen с версией из git describe
make test     # go test -race ./...
make lint     # golangci-lint: govet, staticcheck, gosec, bodyclose, gofmt
make cross    # dist/: linux, darwin, windows — amd64 и arm64
```

<!--
Перезаписать демо-гифку:

  sudo apt install asciinema
  asciinema rec demo.cast --cols 100 --rows 38

  # agg только бинарником из релизов, для других платформ:
  # https://github.com/asciinema/agg/releases
  curl -sSL -o ~/.local/bin/agg \
    https://github.com/asciinema/agg/releases/latest/download/agg-x86_64-unknown-linux-gnu
  chmod +x ~/.local/bin/agg

  agg --idle-time-limit 1 demo.cast assets/demo.gif

100x38 вмещают полный отчёт closed-loop без обрезки, бары выходят по 81 блоку вместо 60
на дефолтных 80. Два прогона по три секунды — 190 КБ; длиннее не надо.
-->
