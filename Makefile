SHELL := /bin/sh

GO ?= go
BINARY := go-mcp-server
BIN_DIR := bin
ROOT ?= .
SKILL_DIR ?= $(HOME)/.config/opencode/skills
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS ?= -s -w -X main.version=$(VERSION)

CGO_ENABLED ?= 0
export CGO_ENABLED

.DEFAULT_GOAL := help

.PHONY: all build install install-skill test test-race vet lint fmt fmt-check verify tidy db-info clean help

all: build

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

install:
	$(GO) install -trimpath -ldflags '$(LDFLAGS)' ./cmd/$(BINARY)
	@bin="$$($(GO) env GOBIN)"; [ -n "$$bin" ] || bin="$$($(GO) env GOPATH)/bin"; echo "installed $$bin/$(BINARY)"

install-skill:
	@mkdir -p "$(SKILL_DIR)/go-mcp-server"
	@cp .opencode/skills/go-mcp-server/SKILL.md "$(SKILL_DIR)/go-mcp-server/SKILL.md"
	@echo "installed skill to $(SKILL_DIR)/go-mcp-server/SKILL.md"

test:
	$(GO) test ./...

test-race:
	$(GO) test -count=1 -race ./...

vet:
	$(GO) vet ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -w .

fmt-check:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt required for:"; echo "$$out"; exit 1; fi

verify: fmt-check vet test-race lint

tidy:
	$(GO) mod tidy

db-info: build
	$(BIN_DIR)/$(BINARY) -db-info "$(ROOT)"

clean:
	rm -rf $(BIN_DIR)

help:
	@echo "go-mcp-server targets:"
	@echo "  build      build $(BIN_DIR)/$(BINARY) (version $(VERSION))"
	@echo "  install    install to GOBIN with version metadata"
	@echo "  install-skill  install the OpenCode skill globally"
	@echo "  test       run tests"
	@echo "  test-race  run tests with the race detector"
	@echo "  vet        run go vet"
	@echo "  lint       run golangci-lint"
	@echo "  fmt        format Go sources"
	@echo "  fmt-check  fail if Go sources are not formatted"
	@echo "  verify     fmt-check + vet + test-race + lint"
	@echo "  tidy       run go mod tidy"
	@echo "  db-info    print populated cache statistics (ROOT=$(ROOT))"
	@echo "  clean      remove build output"
