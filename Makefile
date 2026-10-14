# Makefile для sim800ctl — кросс-платформенная CLI-утилита отправки SMS.
# Сборка под Windows и Linux без CGO.

BINARY      := sim800ctl
PKG         := ./cmd/$(BINARY)
BIN_DIR     := bin
GOFLAGS     := -trimpath
LDFLAGS     := -s -w
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_TIME  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS     += -X main.version=$(VERSION) -X main.buildTime=$(BUILD_TIME)

.PHONY: all build build-linux build-windows build-macos build-all \
        run test vet fmt tidy clean dist help

all: build

# --- Локальная сборка (текущая ОС) ---
build:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(PKG)

# --- Кросс-сборка ---
build-linux:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY)-linux-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY)-linux-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY)-linux-armv7 $(PKG)

build-windows:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY)-windows-amd64.exe $(PKG)

build-macos:
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY)-darwin-amd64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY)-darwin-arm64 $(PKG)

build-all: build-linux build-windows build-macos

# --- Запуск и тесты ---
run: build
	./$(BIN_DIR)/$(BINARY) --help

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BIN_DIR)

help:
	@echo "Цели: build build-linux build-windows build-macos build-all run test vet fmt tidy clean"
