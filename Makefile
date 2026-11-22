SHELL := /bin/sh

GO ?= go
BINARY := goray
BIN_DIR := bin
ROOT ?= .
ROOT_DIR := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
MCP_HELPER ?= $(ROOT_DIR)/scripts/mcp-config.py

CONFIG_HOME := $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)
SKILL_NAME ?= goray
SKILL_SRC ?= .opencode/skills/$(SKILL_NAME)
SKILL_TARGET ?=
SKILL_DIR ?=
SKILL_MODE ?= copy

MCP_NAME ?= $(BINARY)
MCP_SCOPE ?= global
MCP_ARGS ?= serve
MCP_TARGET ?=
MCP_FORMAT ?=
MCP_CONFIG ?=
MCP_CWD ?=
MCP_BIN ?=
OPENCODE_CONFIG ?=
export MCP_ARGS
export MCP_CWD

ifndef VERSION
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
endif
LDFLAGS ?= -s -w -X main.version=$(VERSION)

CGO_ENABLED ?= 0

.DEFAULT_GOAL := help

.PHONY: all build install uninstall install-skill uninstall-skill install-mcp uninstall-mcp install-all uninstall-all test test-race vet lint fmt fmt-check verify tidy db-info clean help web-install web-dev web-typecheck web-build

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
			"~/"*) printf '%s\n' "$(HOME)/$${1#??}" ;; \
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
	if [ -n "$(MCP_BIN)" ]; then \
		mcp_bin="$(MCP_BIN)"; \
		case "$$mcp_bin" in \
			"~") bin_path="$(HOME)" ;; \
			"~/"*) bin_path="$(HOME)/$${mcp_bin#??}" ;; \
			*) bin_path="$$mcp_bin" ;; \
		esac; \
	fi; \
	bin_dir="$$(dirname "$$bin_path")"; \
	if [ ! -d "$$bin_dir" ]; then \
		echo "error: binary directory not found: $$bin_dir" >&2; \
		exit 1; \
	fi; \
	bin_path="$$(CDPATH= cd -- "$$bin_dir" && pwd)/$$(basename "$$bin_path")"; \
	if [ ! -x "$$bin_path" ]; then \
		echo "error: binary not found or not executable: $$bin_path (run make install)" >&2; \
		exit 1; \
	fi; \
	if ! command -v python3 >/dev/null 2>&1; then \
		echo "error: python3 is required by make install-mcp and make uninstall-mcp" >&2; \
		exit 1; \
	fi; \
	if [ ! -f "$(MCP_HELPER)" ]; then \
		echo "error: MCP config helper not found: $(MCP_HELPER)" >&2; \
		exit 1; \
	fi; \
	name="$(MCP_NAME)"; \
	case "$$name" in \
		""|*[!A-Za-z0-9_-]*) \
			echo "error: invalid MCP_NAME='$$name' (use letters, digits, _ or -)" >&2; \
			exit 1 ;; \
	esac; \
	case "$(MCP_SCOPE)" in \
		global|project) ;; \
		*) echo "error: invalid MCP_SCOPE='$(MCP_SCOPE)' (use global or project)" >&2; exit 1 ;; \
	esac; \
	targets="opencode claude codex cursor gemini antigravity agents"; \
	expand_home() { \
		case "$$1" in \
			"~") printf '%s\n' "$(HOME)" ;; \
			"~/"*) printf '%s\n' "$(HOME)/$${1#??}" ;; \
			*) printf '%s\n' "$$1" ;; \
		esac; \
	}; \
	claude_dir() { \
		if [ -n "$${CLAUDE_CONFIG_DIR:-}" ]; then \
			printf '%s\n' "$$CLAUDE_CONFIG_DIR"; \
		else \
			printf '%s\n' "$(HOME)/.claude"; \
		fi; \
	}; \
	claude_state() { \
		if [ -n "$${CLAUDE_CONFIG_DIR:-}" ]; then \
			printf '%s\n' "$$CLAUDE_CONFIG_DIR/.claude.json"; \
		else \
			printf '%s\n' "$(HOME)/.claude.json"; \
		fi; \
	}; \
	codex_home() { \
		if [ -n "$${CODEX_HOME:-}" ]; then \
			printf '%s\n' "$$CODEX_HOME"; \
		else \
			printf '%s\n' "$(HOME)/.codex"; \
		fi; \
	}; \
	resolve_cfg() { \
		case "$$1" in \
			opencode) \
				if [ -n "$(OPENCODE_CONFIG)" ]; then \
					expand_home "$(OPENCODE_CONFIG)"; \
				elif [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./opencode.json"; \
				else \
					printf '%s\n' "$(CONFIG_HOME)/opencode/opencode.json"; \
				fi ;; \
			claude) \
				if [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./.mcp.json"; \
				else \
					printf '%s\n' "$$(claude_state)"; \
				fi ;; \
			codex) \
				if [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./.codex/config.toml"; \
				else \
					printf '%s\n' "$$(codex_home)/config.toml"; \
				fi ;; \
			cursor) \
				if [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./.cursor/mcp.json"; \
				else \
					printf '%s\n' "$(HOME)/.cursor/mcp.json"; \
				fi ;; \
			gemini) \
				if [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./.gemini/settings.json"; \
				else \
					printf '%s\n' "$(HOME)/.gemini/settings.json"; \
				fi ;; \
			antigravity) \
				if [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./.agents/mcp_config.json"; \
				else \
					printf '%s\n' "$(HOME)/.gemini/config/mcp_config.json"; \
				fi ;; \
			agents) \
				if [ "$(MCP_SCOPE)" = "project" ]; then \
					printf '%s\n' "./.agents/mcp.json"; \
				else \
					printf '%s\n' "$(HOME)/.agents/mcp.json"; \
				fi ;; \
			*) return 1 ;; \
		esac; \
	}; \
	display_cfg() { \
		case "$$1" in \
			"$(HOME)/"*) printf '~/%s\n' "$${1#$(HOME)/}" ;; \
			*) printf '%s\n' "$$1" ;; \
		esac; \
	}; \
	infer_format() { \
		case "$$1" in \
			*opencode.json|*opencode.jsonc) printf '%s\n' opencode ;; \
			*config.toml) printf '%s\n' codex ;; \
			*mcp_config.json) printf '%s\n' antigravity ;; \
			*.mcp.json|*claude.json) printf '%s\n' claude ;; \
			*/.cursor/*) printf '%s\n' cursor ;; \
			*/.gemini/*) printf '%s\n' gemini ;; \
			*/.agents/*) printf '%s\n' agents ;; \
			*) printf '%s\n' generic ;; \
		esac; \
	}; \
	detected_target() { \
		case "$$1" in \
			opencode) [ -d "$(CONFIG_HOME)/opencode" ] || [ -f "./opencode.json" ] || [ -f "./opencode.jsonc" ] || [ -d "./.opencode" ] || command -v opencode >/dev/null 2>&1 ;; \
			claude) [ -d "$$(claude_dir)" ] || [ -f "$$(claude_state)" ] || [ -f "./.mcp.json" ] || [ -d "./.claude" ] || command -v claude >/dev/null 2>&1 ;; \
			codex) [ -d "$$(codex_home)" ] || [ -d "./.codex" ] || command -v codex >/dev/null 2>&1 ;; \
			cursor) [ -d "$(HOME)/.cursor" ] || [ -d "./.cursor" ] || [ -d "/Applications/Cursor.app" ] ;; \
			gemini) [ -d "$(HOME)/.gemini" ] || [ -d "./.gemini" ] || command -v gemini >/dev/null 2>&1 ;; \
			antigravity) [ -d "$(HOME)/.gemini/antigravity" ] || [ -d "$(HOME)/.gemini/antigravity-cli" ] || [ -f "$(HOME)/.gemini/config/mcp_config.json" ] || [ -f "./.agents/mcp_config.json" ] || [ -d "/Applications/Antigravity.app" ] || command -v antigravity >/dev/null 2>&1 ;; \
			agents) [ -d "$(HOME)/.agents" ] || [ -d "./.agents" ] ;; \
			*) return 1 ;; \
		esac; \
	}; \
	config_status() { \
		python3 "$(MCP_HELPER)" status --format "$$1" --config "$$2" --name "$$name" >/dev/null 2>&1; \
	}; \
	apply_one() { \
		cfg="$$1"; fmt="$$2"; \
		case "$$cfg" in \
			*.jsonc) \
				echo "error: $$cfg is JSONC; make install-mcp edits JSON only" >&2; \
				echo "use the client's own MCP command or convert the file to JSON" >&2; \
				return 1 ;; \
		esac; \
		if [ "$$ACTION" = "install" ]; then \
			if [ "$$fmt" = "opencode" ] && [ ! -f "$$cfg" ]; then \
				case "$$cfg" in \
					*/opencode.json|opencode.json) jsonc="$${cfg%.json}.jsonc" ;; \
					*) jsonc="" ;; \
				esac; \
				if [ -n "$$jsonc" ] && [ -f "$$jsonc" ]; then \
					echo "error: found $$jsonc; refusing to create $$cfg beside it" >&2; \
					echo "add the server manually, or pass MCP_CONFIG=<opencode.json>" >&2; \
					return 1; \
				fi; \
			fi; \
			python3 "$(MCP_HELPER)" install --format "$$fmt" --config "$$cfg" --name "$$name" --command "$$bin_path" || return 1; \
			display_args="$${MCP_ARGS}"; \
			if [ "$$fmt" = "antigravity" ] && [ "$$display_args" = "serve" ]; then \
				display_args='serve $${workspaceFolder}'; \
			fi; \
			if [ -n "$${MCP_CWD}" ]; then \
				display_args="$$display_args (cwd $$MCP_CWD)"; \
			fi; \
			echo "configured MCP server \"$$name\" -> $$bin_path $$display_args in $$cfg"; \
		else \
			if config_status "$$fmt" "$$cfg"; then \
				python3 "$(MCP_HELPER)" uninstall --format "$$fmt" --config "$$cfg" --name "$$name" || return 1; \
				echo "removed MCP server \"$$name\" from $$cfg"; \
			else \
				echo "not configured: $$name in $$cfg"; \
				return 1; \
			fi; \
		fi; \
	}; \
	apply_custom() { \
		cfg="$$(expand_home "$$1")"; \
		fmt="$(MCP_FORMAT)"; \
		if [ -z "$$fmt" ]; then fmt="$$(infer_format "$$cfg")"; fi; \
		case "$$fmt" in \
			opencode|claude|codex|cursor|gemini|antigravity|agents|generic) ;; \
			*) echo "error: invalid MCP_FORMAT='$$fmt' (use opencode, claude, codex, cursor, gemini, antigravity, agents, or generic)" >&2; exit 1 ;; \
		esac; \
		apply_one "$$cfg" "$$fmt"; \
	}; \
	target_name() { \
		case "$$1" in \
			1|opencode) printf '%s\n' opencode ;; \
			2|claude) printf '%s\n' claude ;; \
			3|codex) printf '%s\n' codex ;; \
			4|cursor) printf '%s\n' cursor ;; \
			5|gemini) printf '%s\n' gemini ;; \
			6|antigravity) printf '%s\n' antigravity ;; \
			7|agents) printf '%s\n' agents ;; \
			*) return 1 ;; \
		esac; \
	}; \
	if [ -n "$(MCP_CONFIG)" ]; then \
		cfg="$$(expand_home "$(MCP_CONFIG)")"; \
		fmt="$(MCP_FORMAT)"; \
		if [ -z "$$fmt" ]; then \
			case "$(MCP_TARGET)" in \
				opencode|claude|codex|cursor|gemini|antigravity|agents|generic) fmt="$(MCP_TARGET)" ;; \
			esac; \
		fi; \
		if [ -z "$$fmt" ]; then fmt="$$(infer_format "$$cfg")"; fi; \
		case "$$fmt" in \
			opencode|claude|codex|cursor|gemini|antigravity|agents|generic) ;; \
			*) echo "error: invalid MCP_FORMAT='$$fmt' (use opencode, claude, codex, cursor, gemini, antigravity, agents, or generic)" >&2; exit 1 ;; \
		esac; \
		if ! apply_one "$$cfg" "$$fmt"; then \
			[ "$$ACTION" = "uninstall" ] || exit 1; \
		fi; \
		exit 0; \
	fi; \
	if [ -n "$(OPENCODE_CONFIG)" ] && { [ -z "$(MCP_TARGET)" ] || [ "$(MCP_TARGET)" = "opencode" ]; }; then \
		if ! apply_one "$$(expand_home "$(OPENCODE_CONFIG)")" opencode; then \
			[ "$$ACTION" = "uninstall" ] || exit 1; \
		fi; \
		exit 0; \
	fi; \
	detected=""; \
	installed=""; \
	for t in $$targets; do \
		if detected_target "$$t"; then detected="$$detected $$t"; fi; \
		cfg="$$(resolve_cfg "$$t")"; \
		if config_status "$$t" "$$cfg"; then installed="$$installed $$t"; fi; \
	done; \
	detected="$${detected# }"; \
	installed="$${installed# }"; \
	selection="$(MCP_TARGET)"; \
	if [ -z "$$selection" ]; then \
		if [ ! -t 0 ]; then \
			if [ "$$ACTION" = "install" ]; then \
				echo "non-interactive shell; defaulting to MCP_TARGET=opencode" >&2; \
				selection="opencode"; \
			elif [ -n "$$installed" ]; then \
				selection="installed"; \
			else \
				echo "no installed \"$$name\" MCP server found in known targets"; \
				exit 0; \
			fi; \
		else \
			if [ "$$ACTION" = "install" ]; then \
				printf 'Install MCP server "%s" (scope: %s). Targets:\n' "$$name" "$(MCP_SCOPE)"; \
			else \
				printf 'Uninstall MCP server "%s" (scope: %s). Targets:\n' "$$name" "$(MCP_SCOPE)"; \
			fi; \
			i=0; \
			for t in $$targets; do \
				i=$$((i + 1)); \
				cfg="$$(resolve_cfg "$$t")"; \
				det=""; \
				inst=""; \
				if detected_target "$$t"; then det=1; fi; \
				if config_status "$$t" "$$cfg"; then inst=1; fi; \
				mark=""; \
				if [ -n "$$det" ] && [ -n "$$inst" ]; then \
					mark=" [detected, installed]"; \
				elif [ -n "$$det" ]; then \
					mark=" [detected]"; \
				elif [ -n "$$inst" ]; then \
					mark=" [installed]"; \
				fi; \
				printf '  %d) %-12s %s%s\n' "$$i" "$$t" "$$(display_cfg "$$cfg")" "$$mark"; \
			done; \
			if [ "$$ACTION" = "install" ]; then \
				default_sel="opencode"; \
				if [ -n "$$detected" ]; then default_sel="detected"; fi; \
				printf 'Select target(s) [numbers/names, comma/space-separated, "detected", "installed", "all", custom path, or q] (%s): ' "$$default_sel"; \
			else \
				default_sel="installed"; \
				if [ -z "$$installed" ]; then default_sel="q"; fi; \
				printf 'Select target(s) [numbers/names, comma/space-separated, "installed", "all", custom path, or q] (%s): ' "$$default_sel"; \
			fi; \
			if ! IFS= read -r selection; then \
				echo "aborted." >&2; \
				exit 1; \
			fi; \
			if [ -z "$$selection" ]; then selection="$$default_sel"; fi; \
		fi; \
	fi; \
	case "$$selection" in q|Q|quit) echo "aborted."; exit 0 ;; esac; \
	case "$$selection" in \
		/*|./*|../*|~|~/*) \
			case "$$selection" in \
				*,*) ;; \
				*) \
					if ! apply_custom "$$selection"; then \
						[ "$$ACTION" = "uninstall" ] || exit 1; \
					fi; \
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
						echo "no installed \"$$name\" MCP servers found"; \
						exit 0; \
					fi; \
					echo "error: no installed \"$$name\" MCP servers found; specify a target explicitly" >&2; \
					exit 1; \
				fi; \
				expanded="$$expanded $$installed" ;; \
			*) expanded="$$expanded $$tok" ;; \
		esac; \
	done; \
	selected=""; \
	for tok in $$expanded; do \
		if ! t="$$(target_name "$$tok")"; then \
			echo "error: unknown target '$$tok'" >&2; \
			exit 1; \
		fi; \
		selected="$$selected $$t"; \
	done; \
	seen=""; \
	count=0; \
	failed=0; \
	for t in $$selected; do \
		cfg="$$(resolve_cfg "$$t")"; \
		case "$$seen" in *"|$$cfg|"*) continue ;; esac; \
		seen="$$seen|$$cfg|"; \
		if apply_one "$$cfg" "$$t"; then \
			count=$$((count + 1)); \
		else \
			failed=$$((failed + 1)); \
		fi; \
	done; \
	if [ "$$failed" -gt 0 ] && [ "$$ACTION" = "install" ]; then \
		exit 1; \
	fi; \
	if [ "$$count" -gt 0 ]; then \
		echo "restart the client(s) to pick up the change"; \
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

install-mcp: install ## Install binary and register MCP server in selected clients
	@set -eu; ACTION=install; $(MANAGE_MCP)

uninstall-mcp: ## Unregister MCP server from selected clients
	@set -eu; ACTION=uninstall; $(MANAGE_MCP)

install-all: install install-skill install-mcp ## Install binary, skills, and MCP servers

uninstall-all: uninstall-mcp uninstall-skill uninstall ## Remove MCP servers, skills, and binary

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

web-install: ## Install web dependencies from the committed lockfile
	cd web && npm ci

web-dev: ## Run the Vite dev server (proxies /api to a running goray serve)
	cd web && npm run dev

web-typecheck: ## Typecheck the report frontend
	cd web && npm run typecheck

web-build: ## Typecheck and build the single-file report into internal/render/dist
	cd web && npm run typecheck && npm run build

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
	@printf '  MCP_NAME=%s      (MCP server key)\n' '$(MCP_NAME)'
	@printf '  MCP_SCOPE=%s     (global|project)\n' '$(MCP_SCOPE)'
	@printf '  MCP_TARGET=%s    (opencode|claude|codex|cursor|gemini|antigravity|agents|detected|installed|all or path)\n' '$(MCP_TARGET)'
	@printf '  MCP_CONFIG=%s    (explicit config file; bypasses the prompt)\n' '$(MCP_CONFIG)'
	@printf '  MCP_FORMAT=%s    (auto|opencode|claude|codex|cursor|gemini|antigravity|agents|generic)\n' '$(MCP_FORMAT)'
	@printf '  MCP_CWD=%s       (Antigravity cwd override; fallback for $${workspaceFolder})\n' '$(MCP_CWD)'
	@printf '  MCP_ARGS=%s      (arguments passed to the MCP server)\n' '$(MCP_ARGS)'
	@printf '  MCP_BIN=%s       (binary path override; default: GOBIN/goray)\n' '$(MCP_BIN)'
	@printf '  OPENCODE_CONFIG=%s (legacy OpenCode config path override)\n' '$(OPENCODE_CONFIG)'