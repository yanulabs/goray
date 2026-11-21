SHELL := /bin/sh

GO ?= go
BINARY := goray
BIN_DIR := bin
ROOT ?= .

CONFIG_HOME := $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)
SKILL_NAME ?= goray
SKILL_SRC ?= .opencode/skills/$(SKILL_NAME)
SKILL_TARGET ?=
SKILL_DIR ?=
SKILL_MODE ?= copy

MCP_NAME ?= $(BINARY)
MCP_SCOPE ?= global
MCP_ARGS ?= serve
OPENCODE_CONFIG ?=

ifndef VERSION
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
endif
LDFLAGS ?= -s -w -X main.version=$(VERSION)

CGO_ENABLED ?= 0

.DEFAULT_GOAL := help

.PHONY: all build install uninstall install-skill uninstall-skill install-mcp uninstall-mcp install-all uninstall-all test test-race vet lint fmt fmt-check verify tidy db-info clean help

define RESOLVE_BIN
	bin="$$($(GO) env GOBIN)"; \
	if [ -z "$$bin" ]; then \
		gopath="$$($(GO) env GOPATH)"; \
		bin="$${gopath%%:*}/bin"; \
	fi; \
	ext="$$($(GO) env GOEXE)"; \
	bin_path="$$bin/$(BINARY)$$ext"
endef

define MANAGE_SKILL
	case "$(SKILL_NAME)" in \
		""|.|..|*/*) \
			echo "error: invalid SKILL_NAME='$(SKILL_NAME)'" >&2; \
			exit 1 ;; \
	esac; \
	src_abs=""; \
	if [ "$$ACTION" = "install" ]; then \
		if [ ! -f "$(SKILL_SRC)/SKILL.md" ]; then \
			echo "error: skill not found at $(SKILL_SRC)/SKILL.md" >&2; \
			exit 1; \
		fi; \
		src_abs="$$(CDPATH= cd -- "$(SKILL_SRC)" && pwd)"; \
	elif [ -d "$(SKILL_SRC)" ]; then \
		src_abs="$$(CDPATH= cd -- "$(SKILL_SRC)" && pwd)"; \
	fi; \
	targets="opencode claude codex cursor gemini agents"; \
	expand_tilde() { \
		case "$$1" in \
			"~") printf '%s\n' "$(HOME)" ;; \
			"~/"*) printf '%s\n' "$(HOME)/$${1#~/}" ;; \
			*) printf '%s\n' "$$1" ;; \
		esac; \
	}; \
	resolve_dir() { \
		case "$$1" in \
			1|opencode) printf '%s\n' "$(CONFIG_HOME)/opencode/skills" ;; \
			2|claude)   printf '%s\n' "$(HOME)/.claude/skills" ;; \
			3|codex)    printf '%s\n' "$(HOME)/.codex/skills" ;; \
			4|cursor)   printf '%s\n' "$(HOME)/.cursor/skills" ;; \
			5|gemini)   printf '%s\n' "$(HOME)/.gemini/skills" ;; \
			6|agents)   printf '%s\n' "$(HOME)/.agents/skills" ;; \
			/*|./*|../*|~|~/*) expand_tilde "$$1" ;; \
			*) return 1 ;; \
		esac; \
	}; \
	skill_status() { \
		d="$$1/$(SKILL_NAME)"; \
		if [ -L "$$d" ]; then \
			if [ -e "$$d" ]; then printf '%s' "symlink"; else printf '%s' "broken symlink"; fi; \
		elif [ -e "$$d" ]; then \
			printf '%s' "installed"; \
		fi; \
	}; \
	join_dest() { \
		case "$$1" in \
			/) printf '/%s\n' "$(SKILL_NAME)" ;; \
			*) printf '%s/%s\n' "$$1" "$(SKILL_NAME)" ;; \
		esac; \
	}; \
	install_one() { \
		base="$$1"; \
		if [ -z "$$base" ]; then \
			echo "error: invalid skill destination" >&2; \
			exit 1; \
		fi; \
		mkdir -p "$$base"; \
		base_abs="$$(CDPATH= cd -- "$$base" && pwd)"; \
		dest="$$(join_dest "$$base_abs")"; \
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
	uninstall_one() { \
		base="$$1"; \
		if [ -z "$$base" ]; then \
			echo "error: invalid skill destination" >&2; \
			exit 1; \
		fi; \
		if [ ! -d "$$base" ]; then \
			echo "not installed: $$base/$(SKILL_NAME)"; \
			return 0; \
		fi; \
		base_abs="$$(CDPATH= cd -- "$$base" && pwd)"; \
		dest="$$(join_dest "$$base_abs")"; \
		if [ -n "$$src_abs" ] && [ "$$dest" = "$$src_abs" ]; then \
			echo "skipped $$dest (source directory)"; \
			return 0; \
		fi; \
		if [ -L "$$dest" ]; then \
			rm -f "$$dest"; \
			echo "removed skill (symlink): $$dest"; \
		elif [ -e "$$dest" ]; then \
			rm -rf "$$dest"; \
			echo "removed skill: $$dest"; \
		else \
			echo "not installed: $$dest"; \
		fi; \
	}; \
	apply_one() { \
		if [ "$$ACTION" = "install" ]; then \
			install_one "$$1"; \
		else \
			uninstall_one "$$1"; \
		fi; \
	}; \
	if [ -n "$(SKILL_DIR)" ]; then \
		apply_one "$$(expand_tilde "$(SKILL_DIR)")"; \
		exit 0; \
	fi; \
	detected=""; \
	installed=""; \
	for t in $$targets; do \
		dir="$$(resolve_dir "$$t")"; \
		if [ -d "$${dir%/skills}" ]; then \
			detected="$$detected $$t"; \
		fi; \
		if [ -n "$$(skill_status "$$dir")" ]; then \
			installed="$$installed $$t"; \
		fi; \
	done; \
	detected="$${detected# }"; \
	installed="$${installed# }"; \
	selection="$(SKILL_TARGET)"; \
	if [ -z "$$selection" ]; then \
		if [ ! -t 0 ]; then \
			if [ "$$ACTION" = "install" ]; then \
				echo "error: non-interactive shell; set SKILL_TARGET=opencode|claude|codex|cursor|gemini|agents|detected|installed|all or SKILL_DIR=/path" >&2; \
			else \
				echo "error: non-interactive shell; set SKILL_TARGET=opencode|claude|codex|cursor|gemini|agents|installed|all or SKILL_DIR=/path" >&2; \
			fi; \
			exit 1; \
		fi; \
		if [ "$$ACTION" = "install" ]; then \
			printf 'Install skill "%s" (mode: %s). Available targets:\n' "$(SKILL_NAME)" "$(SKILL_MODE)"; \
		else \
			printf 'Uninstall skill "%s". Targets:\n' "$(SKILL_NAME)"; \
		fi; \
		i=0; \
		for t in $$targets; do \
			i=$$((i + 1)); \
			dir="$$(resolve_dir "$$t")"; \
			st="$$(skill_status "$$dir")"; \
			mark=""; \
			if [ "$$ACTION" = "install" ]; then \
				if [ -d "$${dir%/skills}" ] && [ -n "$$st" ]; then \
					mark=" [detected, $$st]"; \
				elif [ -d "$${dir%/skills}" ]; then \
					mark=" [detected]"; \
				elif [ -n "$$st" ]; then \
					mark=" [$$st]"; \
				fi; \
			elif [ -n "$$st" ]; then \
				mark=" [$$st]"; \
			fi; \
			printf '  %d) %-9s %s%s\n' "$$i" "$$t" "$$dir" "$$mark"; \
		done; \
		if [ "$$ACTION" = "install" ]; then \
			default_sel="opencode"; \
			if [ -n "$$detected" ]; then default_sel="detected"; fi; \
			printf 'Select target(s) [numbers/names, comma/space-separated, "detected", "installed", "all", custom path, or q] (%s): ' "$$default_sel"; \
		else \
			default_sel="q"; \
			if [ -n "$$installed" ]; then default_sel="installed"; fi; \
			printf 'Select target(s) [numbers/names, comma/space-separated, "installed", "all", custom path, or q] (%s): ' "$$default_sel"; \
		fi; \
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
					apply_one "$$(resolve_dir "$$selection")"; \
					exit 0 ;; \
			esac ;; \
	esac; \
	expanded=""; \
	for tok in $$(printf '%s\n' "$$selection" | tr ',' ' '); do \
		case "$$tok" in \
			all) expanded="$$expanded $$targets" ;; \
			detected) \
				if [ -z "$$detected" ]; then \
					if [ "$$ACTION" = "uninstall" ]; then \
						echo "no existing tool config directories detected; nothing to uninstall"; \
						exit 0; \
					fi; \
					echo "error: no existing tool config directories detected; specify a target explicitly" >&2; \
					exit 1; \
				fi; \
				expanded="$$expanded $$detected" ;; \
			installed) \
				if [ -z "$$installed" ]; then \
					if [ "$$ACTION" = "uninstall" ]; then \
						echo "no installed \"$(SKILL_NAME)\" skills found in known targets"; \
						exit 0; \
					fi; \
					echo "error: no installed \"$(SKILL_NAME)\" skills found; specify a target explicitly" >&2; \
					exit 1; \
				fi; \
				expanded="$$expanded $$installed" ;; \
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
		apply_one "$$dir"; \
	done
endef

define MANAGE_MCP
	$(RESOLVE_BIN); \
	cfg="$(OPENCODE_CONFIG)"; \
	if [ -z "$$cfg" ]; then \
		case "$(MCP_SCOPE)" in \
			global)  cfg="$(CONFIG_HOME)/opencode/opencode.json" ;; \
			project) cfg="./opencode.json" ;; \
			*) echo "error: invalid MCP_SCOPE='$(MCP_SCOPE)' (use global or project)" >&2; exit 1 ;; \
		esac; \
	else \
		case "$$cfg" in \
			"~") cfg="$(HOME)" ;; \
			"~/"*) cfg="$(HOME)/$${cfg#~/}" ;; \
		esac; \
	fi; \
	cfg_dir="$$(dirname "$$cfg")"; \
	print_snippet() { \
		printf '{\n  "mcp": {\n    "servers": {\n      "%s": {\n        "type": "local",\n        "command": ["%s"' "$(MCP_NAME)" "$$bin_path" >&2; \
		for mcp_arg in $(MCP_ARGS); do printf ', "%s"' "$$mcp_arg" >&2; done; \
		printf ']\n      }\n    }\n  }\n}\n' >&2; \
	}; \
	if [ -z "$(OPENCODE_CONFIG)" ] && [ ! -f "$$cfg" ] && [ -f "$$cfg_dir/opencode.jsonc" ]; then \
		if [ "$$ACTION" = "install" ]; then \
			echo "error: found $$cfg_dir/opencode.jsonc; refusing to shadow it with opencode.json or strip comments." >&2; \
			echo "Add this entry manually (or pass OPENCODE_CONFIG=$$cfg to force opencode.json):" >&2; \
			print_snippet; \
		else \
			echo "error: found $$cfg_dir/opencode.jsonc; remove \"$(MCP_NAME)\" under \"mcp.servers\" manually." >&2; \
		fi; \
		exit 1; \
	fi; \
	if command -v jq >/dev/null 2>&1; then \
		tmp=""; \
		trap 'rm -f "$$tmp"' EXIT; \
		if [ "$$ACTION" = "install" ]; then \
			mkdir -p "$$cfg_dir"; \
			tmp="$$(mktemp "$$cfg_dir/.opencode.json.XXXXXX")"; \
			if [ -s "$$cfg" ]; then \
				jq --arg name "$(MCP_NAME)" --arg bin "$$bin_path" --arg args "$(MCP_ARGS)" \
					'.["$$schema"] //= "https://opencode.ai/config.json" | .mcp = (.mcp // {}) | .mcp.servers = (.mcp.servers // {}) | del(.mcp[$$name]) | .mcp.servers[$$name] = {type: "local", command: ([$$bin] + ($$args | split(" ") | map(select(length > 0))))}' \
					"$$cfg" > "$$tmp"; \
			else \
				printf '{}\n' | jq --arg name "$(MCP_NAME)" --arg bin "$$bin_path" --arg args "$(MCP_ARGS)" \
					'.["$$schema"] //= "https://opencode.ai/config.json" | .mcp = {servers: {($$name): {type: "local", command: ([$$bin] + ($$args | split(" ") | map(select(length > 0))))}}}' \
					> "$$tmp"; \
			fi; \
			mv "$$tmp" "$$cfg"; \
			tmp=""; \
			echo "configured MCP server \"$(MCP_NAME)\" -> $$bin_path $(MCP_ARGS) in $$cfg"; \
			echo "reload OpenCode to pick up the change"; \
		else \
			if [ ! -s "$$cfg" ]; then \
				echo "not configured: $(MCP_NAME) in $$cfg"; \
				exit 0; \
			fi; \
			jq empty "$$cfg" >/dev/null; \
			if ! jq -e --arg name "$(MCP_NAME)" '((.mcp.servers // {}) | has($$name)) or ((.mcp // {}) | has($$name))' "$$cfg" >/dev/null 2>&1; then \
				echo "not configured: $(MCP_NAME) in $$cfg"; \
				exit 0; \
			fi; \
			tmp="$$(mktemp "$$cfg_dir/.opencode.json.XXXXXX")"; \
			jq --arg name "$(MCP_NAME)" \
				'if (.mcp | type) == "object" then del(.mcp.servers[$$name], .mcp[$$name]) else . end | if (.mcp | type) == "object" and ((.mcp.servers // {}) | length) == 0 then del(.mcp.servers) else . end | if (.mcp | type) == "object" and (.mcp | length) == 0 then del(.mcp) else . end' \
				"$$cfg" > "$$tmp"; \
			mv "$$tmp" "$$cfg"; \
			tmp=""; \
			echo "removed MCP server \"$(MCP_NAME)\" from $$cfg"; \
			echo "reload OpenCode to pick up the change"; \
		fi; \
	elif command -v python3 >/dev/null 2>&1; then \
		printf '%s\n' \
			'import json, os, shlex, sys, tempfile' \
			'action = os.environ["MCP_ACTION"]' \
			'cfg = os.environ["MCP_CFG"]' \
			'name = os.environ["MCP_NAME"]' \
			'bin_path = os.environ["MCP_BIN"]' \
			'raw_args = os.environ.get("MCP_ARGS", "")' \
			'cfg_dir = os.path.dirname(os.path.abspath(cfg))' \
			'if os.path.exists(cfg) and os.path.getsize(cfg) > 0:' \
			'    with open(cfg, "r", encoding="utf-8") as f:' \
			'        data = json.load(f)' \
			'    if not isinstance(data, dict):' \
			'        sys.exit(f"error: root of {cfg} must be a JSON object")' \
			'else:' \
			'    if action == "uninstall":' \
			'        print(f"not configured: {name} in {cfg}")' \
			'        sys.exit(0)' \
			'    data = {}' \
			'mcp = data.get("mcp")' \
			'if mcp is None:' \
			'    mcp = {}' \
			'elif not isinstance(mcp, dict):' \
			'    sys.exit(f"error: \"mcp\" in {cfg} must be a JSON object")' \
			'servers = mcp.get("servers")' \
			'if servers is None:' \
			'    servers = {}' \
			'elif not isinstance(servers, dict):' \
			'    sys.exit(f"error: \"mcp.servers\" in {cfg} must be a JSON object")' \
			'if action == "install":' \
			'    os.makedirs(cfg_dir, exist_ok=True)' \
			'    data.setdefault("$$schema", "https://opencode.ai/config.json")' \
			'    cmd = [bin_path] + (shlex.split(raw_args) if raw_args.strip() else [])' \
			'    mcp.pop(name, None)' \
			'    servers[name] = {"type": "local", "command": cmd}' \
			'    mcp["servers"] = servers' \
			'    data["mcp"] = mcp' \
			'    msg = f"configured MCP server \"{name}\" -> {bin_path} {raw_args} in {cfg}"' \
			'else:' \
			'    if name not in servers and name not in mcp:' \
			'        print(f"not configured: {name} in {cfg}")' \
			'        sys.exit(0)' \
			'    servers.pop(name, None)' \
			'    mcp.pop(name, None)' \
			'    if servers:' \
			'        mcp["servers"] = servers' \
			'    else:' \
			'        mcp.pop("servers", None)' \
			'    if mcp:' \
			'        data["mcp"] = mcp' \
			'    else:' \
			'        data.pop("mcp", None)' \
			'    msg = f"removed MCP server \"{name}\" from {cfg}"' \
			'fd, tmp_path = tempfile.mkstemp(prefix=".opencode.json.", dir=cfg_dir)' \
			'try:' \
			'    with os.fdopen(fd, "w", encoding="utf-8") as f:' \
			'        json.dump(data, f, indent=2)' \
			'        f.write("\n")' \
			'    os.replace(tmp_path, cfg)' \
			'except Exception:' \
			'    if os.path.exists(tmp_path):' \
			'        os.unlink(tmp_path)' \
			'    raise' \
			'print(msg)' \
			'print("reload OpenCode to pick up the change")' \
		| MCP_ACTION="$$ACTION" MCP_CFG="$$cfg" MCP_NAME="$(MCP_NAME)" MCP_BIN="$$bin_path" MCP_ARGS="$(MCP_ARGS)" python3 -; \
	else \
		echo "error: jq or python3 is required to edit $$cfg" >&2; \
		if [ "$$ACTION" = "install" ]; then \
			echo "Add this manually to $$cfg:" >&2; \
			print_snippet; \
		fi; \
		exit 1; \
	fi
endef

all: build ## Build the binary

build: ## Build $(BIN_DIR)/$(BINARY)
	@mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=$(CGO_ENABLED) $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o "$(BIN_DIR)/$(BINARY)" "./cmd/$(BINARY)"

install: ## Install binary to GOBIN/GOPATH with version metadata
	CGO_ENABLED=$(CGO_ENABLED) $(GO) install -trimpath -ldflags '$(LDFLAGS)' "./cmd/$(BINARY)"
	@$(RESOLVE_BIN); echo "installed $$bin_path"

uninstall: ## Remove installed binary from GOBIN/GOPATH
	@set -eu; \
	$(RESOLVE_BIN); \
	if [ -e "$$bin_path" ] || [ -L "$$bin_path" ]; then \
		rm -f "$$bin_path"; \
		echo "removed $$bin_path"; \
	else \
		echo "not installed: $$bin_path"; \
	fi

install-skill: ## Install skill interactively or via SKILL_TARGET / SKILL_DIR
	@set -eu; ACTION=install; $(MANAGE_SKILL)

uninstall-skill: ## Uninstall skill interactively or via SKILL_TARGET / SKILL_DIR
	@set -eu; ACTION=uninstall; $(MANAGE_SKILL)

install-mcp: install ## Install binary and register MCP server in OpenCode config
	@set -eu; ACTION=install; $(MANAGE_MCP)

uninstall-mcp: ## Unregister MCP server from OpenCode config
	@set -eu; ACTION=uninstall; $(MANAGE_MCP)

install-all: install install-skill install-mcp ## Install binary, skills, and OpenCode MCP server

uninstall-all: uninstall-mcp uninstall-skill uninstall ## Remove OpenCode MCP server, skills, and binary

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
	@awk 'BEGIN {FS = ":.*## "; printf "goray (version %s)\n\nTargets:\n", "$(VERSION)"} \
		/^[a-zA-Z0-9_.-]+:.*## / {printf "  %-17s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf '\nVariables:\n'
	@printf '  ROOT=%s\n' '$(ROOT)'
	@printf '  SKILL_TARGET=%s  (opencode|claude|codex|cursor|gemini|agents|detected|installed|all or path)\n' '$(SKILL_TARGET)'
	@printf '  SKILL_DIR=%s     (explicit destination dir; bypasses prompt)\n' '$(SKILL_DIR)'
	@printf '  SKILL_MODE=%s    (copy|link)\n' '$(SKILL_MODE)'
	@printf '  MCP_NAME=%s      (MCP server key in OpenCode config)\n' '$(MCP_NAME)'
	@printf '  MCP_SCOPE=%s     (global|project)\n' '$(MCP_SCOPE)'
	@printf '  MCP_ARGS=%s      (extra CLI args passed to the MCP server)\n' '$(MCP_ARGS)'
	@printf '  OPENCODE_CONFIG=%s (explicit opencode.json path)\n' '$(OPENCODE_CONFIG)'