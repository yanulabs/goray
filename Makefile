SHELL := /bin/sh

GO ?= go
BINARY := go-mcp-server
BIN_DIR := bin
ROOT ?= .

CONFIG_HOME := $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)
SKILL_NAME ?= go-mcp-server
SKILL_SRC ?= .opencode/skills/$(SKILL_NAME)
SKILL_TARGET ?=
SKILL_DIR ?=
SKILL_MODE ?= copy

ifndef VERSION
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
endif
LDFLAGS ?= -s -w -X main.version=$(VERSION)

CGO_ENABLED ?= 0

.DEFAULT_GOAL := help

.PHONY: all build install install-skill test test-race vet lint fmt fmt-check verify tidy db-info clean help

all: build ## Build the binary

build: ## Build $(BIN_DIR)/$(BINARY)
	@mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o "$(BIN_DIR)/$(BINARY)" "./cmd/$(BINARY)"

install: ## Install binary to GOBIN/GOPATH with version metadata
	CGO_ENABLED=$(CGO_ENABLED) $(GO) install -trimpath -ldflags '$(LDFLAGS)' "./cmd/$(BINARY)"
	@bin="$$($(GO) env GOBIN)"; \
	if [ -z "$$bin" ]; then \
		gopath="$$($(GO) env GOPATH)"; \
		bin="$${gopath%%:*}/bin"; \
	fi; \
	echo "installed $$bin/$(BINARY)"

install-skill: ## Install skill interactively or via SKILL_TARGET / SKILL_DIR
	@set -eu; \
	if [ ! -f "$(SKILL_SRC)/SKILL.md" ]; then \
		echo "error: skill not found at $(SKILL_SRC)/SKILL.md" >&2; \
		exit 1; \
	fi; \
	src_abs="$$(CDPATH= cd -- "$(SKILL_SRC)" && pwd)"; \
	targets="opencode claude codex cursor gemini agents"; \
	resolve_dir() { \
		case "$$1" in \
			1|opencode) printf '%s\n' "$(CONFIG_HOME)/opencode/skills" ;; \
			2|claude)   printf '%s\n' "$(HOME)/.claude/skills" ;; \
			3|codex)    printf '%s\n' "$(HOME)/.codex/skills" ;; \
			4|cursor)   printf '%s\n' "$(HOME)/.cursor/skills" ;; \
			5|gemini)   printf '%s\n' "$(HOME)/.gemini/skills" ;; \
			6|agents)   printf '%s\n' "$(HOME)/.agents/skills" ;; \
			/*|./*|../*|~|~/*) \
				p="$$1"; \
				case "$$p" in \
					"~") p="$(HOME)" ;; \
					"~/"*) p="$(HOME)/$${p#~/}" ;; \
				esac; \
				printf '%s\n' "$$p" ;; \
			*) return 1 ;; \
		esac; \
	}; \
	install_one() { \
		base="$$1"; \
		if [ -z "$(SKILL_NAME)" ] || [ -z "$$base" ]; then \
			echo "error: invalid skill destination" >&2; \
			exit 1; \
		fi; \
		mkdir -p "$$base"; \
		base_abs="$$(CDPATH= cd -- "$$base" && pwd)"; \
		dest="$$base_abs/$(SKILL_NAME)"; \
		if [ "$$dest" = "$$src_abs" ]; then \
			echo "skipped $$dest (source directory)"; \
			return 0; \
		fi; \
		rm -rf "$$dest"; \
		case "$(SKILL_MODE)" in \
			copy) \
				cp -R "$$src_abs" "$$dest"; \
				echo "installed skill (copy): $$dest" ;; \
			link) \
				ln -s "$$src_abs" "$$dest"; \
				echo "installed skill (symlink): $$dest -> $$src_abs" ;; \
			*) \
				echo "error: invalid SKILL_MODE='$(SKILL_MODE)' (use copy or link)" >&2; \
				exit 1 ;; \
		esac; \
	}; \
	if [ -n "$(SKILL_DIR)" ]; then \
		dir="$(SKILL_DIR)"; \
		case "$$dir" in \
			"~") dir="$(HOME)" ;; \
			"~/"*) dir="$(HOME)/$${dir#~/}" ;; \
		esac; \
		install_one "$$dir"; \
		exit 0; \
	fi; \
	detected=""; \
	for t in $$targets; do \
		dir="$$(resolve_dir "$$t")"; \
		if [ -d "$${dir%/skills}" ]; then \
			detected="$$detected $$t"; \
		fi; \
	done; \
	detected="$${detected# }"; \
	selection="$(SKILL_TARGET)"; \
	if [ -z "$$selection" ]; then \
		if [ ! -t 0 ]; then \
			echo "error: non-interactive shell; set SKILL_TARGET=opencode|claude|codex|cursor|gemini|agents|detected|all or SKILL_DIR=/path" >&2; \
			exit 1; \
		fi; \
		printf 'Install skill "%s" (mode: %s). Available targets:\n' "$(SKILL_NAME)" "$(SKILL_MODE)"; \
		i=0; \
		for t in $$targets; do \
			i=$$((i + 1)); \
			dir="$$(resolve_dir "$$t")"; \
			mark=""; \
			if [ -d "$${dir%/skills}" ]; then mark=" [detected]"; fi; \
			printf '  %d) %-9s %s%s\n' "$$i" "$$t" "$$dir" "$$mark"; \
		done; \
		default_sel="opencode"; \
		if [ -n "$$detected" ]; then default_sel="detected"; fi; \
		printf 'Select target(s) [numbers/names, comma/space-separated, "detected", "all", custom path, or q] (%s): ' "$$default_sel"; \
		if ! IFS= read -r selection; then \
			echo "aborted." >&2; \
			exit 1; \
		fi; \
		if [ -z "$$selection" ]; then selection="$$default_sel"; fi; \
		case "$$selection" in q|Q|quit) echo "aborted."; exit 0 ;; esac; \
	fi; \
	case "$$selection" in \
		/*|./*|../*|~|~/*) \
			case "$$selection" in \
				*,*) ;; \
				*) \
					dir="$$(resolve_dir "$$selection")"; \
					install_one "$$dir"; \
					exit 0 ;; \
			esac ;; \
	esac; \
	expanded=""; \
	for tok in $$(printf '%s\n' "$$selection" | tr ',' ' '); do \
		case "$$tok" in \
			all) expanded="$$expanded $$targets" ;; \
			detected) \
				if [ -z "$$detected" ]; then \
					echo "error: no existing tool config directories detected; specify a target explicitly" >&2; \
					exit 1; \
				fi; \
				expanded="$$expanded $$detected" ;; \
			*) expanded="$$expanded $$tok" ;; \
		esac; \
	done; \
	seen=""; \
	for tok in $$expanded; do \
		if ! dir="$$(resolve_dir "$$tok")"; then \
			echo "error: unknown target '$$tok'" >&2; \
			exit 1; \
		fi; \
		case "$$seen" in \
			*"|$$dir|"*) continue ;; \
		esac; \
		seen="$$seen|$$dir|"; \
		install_one "$$dir"; \
	done

test: ## Run unit tests
	$(GO) test ./...

test-race: ## Run tests with race detector (forces CGO_ENABLED=1)
	CGO_ENABLED=1 $(GO) test -count=1 -race ./...

vet: ## Run go vet
	$(GO) vet ./...

lint: ## Run golangci-lint
	@command -v golangci-lint >/dev/null 2>&1 || { echo "error: golangci-lint not found in PATH" >&2; exit 1; }
	golangci-lint run ./...

fmt: ## Format Go sources
	gofmt -w .

fmt-check: ## Fail if Go sources are not formatted
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt required for:"; \
		echo "$$out"; \
		exit 1; \
	fi

verify: fmt-check vet lint test-race ## Run fmt-check, vet, lint, and test-race

tidy: ## Run go mod tidy
	$(GO) mod tidy

db-info: build ## Print populated cache statistics (ROOT=$(ROOT))
	"$(BIN_DIR)/$(BINARY)" -db-info "$(ROOT)"

clean: ## Remove build output
	rm -rf "$(BIN_DIR)"

help: ## Show available targets and variables
	@awk 'BEGIN {FS = ":.*## "; printf "go-mcp-server (version %s)\n\nTargets:\n", "$(VERSION)"} \
		/^[a-zA-Z0-9_.-]+:.*## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf '\nVariables:\n'
	@printf '  ROOT=%s\n' '$(ROOT)'
	@printf '  SKILL_TARGET=%s  (opencode|claude|codex|cursor|gemini|agents|detected|all or path)\n' '$(SKILL_TARGET)'
	@printf '  SKILL_DIR=%s     (explicit destination dir; bypasses prompt)\n' '$(SKILL_DIR)'
	@printf '  SKILL_MODE=%s    (copy|link)\n' '$(SKILL_MODE)'