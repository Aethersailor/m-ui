GO ?= go
NPM ?= npm
OUTPUT_DIR ?= dist
BINARY ?= $(OUTPUT_DIR)/m-ui
VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
DIRTY ?= $(shell test -z "$$(git status --porcelain 2>/dev/null)" && echo false || echo true)
LDFLAGS := -X github.com/Aethersailor/m-ui/internal/version.version=$(VERSION) \
	-X github.com/Aethersailor/m-ui/internal/version.commit=$(COMMIT) \
	-X github.com/Aethersailor/m-ui/internal/version.date=$(BUILD_DATE) \
	-X github.com/Aethersailor/m-ui/internal/version.dirty=$(DIRTY)

.PHONY: all build web-install web-build test test-go test-web vet lint typecheck clean

all: build

build: web-build
	mkdir -p $(OUTPUT_DIR)
	$(GO) build -tags webembed -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/m-ui

web-install:
	$(NPM) --prefix web ci

web-build:
	$(NPM) --prefix web run build

test: test-go test-web

test-go:
	$(GO) test ./...

test-web:
	$(NPM) --prefix web run test

vet:
	$(GO) vet ./...

lint:
	$(NPM) --prefix web run lint

typecheck:
	$(NPM) --prefix web run typecheck

clean:
	rm -rf "$(OUTPUT_DIR)" internal/httpapi/ui/dist
