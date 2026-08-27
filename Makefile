BINARY  := loadgen
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# gofmt берём из GOROOT: в PATH может оказаться сборка из snap,
# которая молча ничего не находит даже на кривых файлах
GOFMT := $(shell go env GOROOT)/bin/gofmt

# Тот же набор, что в .goreleaser.yml — иначе локальная проверка и релиз разойдутся
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

os   = $(word 1,$(subst /, ,$1))
arch = $(word 2,$(subst /, ,$1))
ext  = $(if $(findstring windows,$1),.exe)

.PHONY: build test lint fmt cross clean $(PLATFORMS)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/loadgen

test:
	go test -race ./...

lint:
	@command -v golangci-lint >/dev/null || { \
		echo "golangci-lint не найден. Поставить:"; \
		echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
		exit 1; }
	golangci-lint run

fmt:
	$(GOFMT) -w ./cmd ./internal

# Пять бинарников из одной команды. -s -w срезают отладочную информацию:
# для релизных сборок это примерно четверть размера.
cross: $(PLATFORMS)

$(PLATFORMS):
	CGO_ENABLED=0 GOOS=$(call os,$@) GOARCH=$(call arch,$@) \
	go build -ldflags "-s -w $(LDFLAGS)" \
		-o dist/$(BINARY)-$(call os,$@)-$(call arch,$@)$(call ext,$@) ./cmd/loadgen

clean:
	rm -rf bin dist
