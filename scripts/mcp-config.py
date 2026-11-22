#!/usr/bin/env python3
"""Edit MCP client configuration files for `make install-mcp`.

Supported formats:
  opencode  OpenCode JSON (mcp.servers, with legacy mcp.<name> cleanup)
  claude    Claude Code JSON (mcpServers, stdio entry)
  cursor    Cursor JSON (mcpServers)
  gemini    Gemini CLI JSON (mcpServers)
  antigravity Antigravity App/IDE JSON (mcpServers)
  agents    Generic Agent Plugins JSON (mcpServers, stdio entry)
  generic   Any JSON client using mcpServers
  codex     Codex TOML (mcp_servers."<name>")

The script is install tooling only; it is not part of the goray binary.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import shlex
import stat
import sys
import tempfile
from pathlib import Path

FORMATS = (
    "opencode",
    "claude",
    "cursor",
    "gemini",
    "antigravity",
    "agents",
    "generic",
    "codex",
)
JSON_FORMATS = tuple(f for f in FORMATS if f != "codex")

TOML_HEADER = re.compile(r"^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(?:#.*)?$")


class ConfigError(Exception):
    """A configuration file could not be read or would be corrupted."""


def expand_path(raw: str) -> Path:
    return Path(os.path.expanduser(raw))


def writable_path(path: Path) -> Path:
    """Follow symlinks so atomic replacement does not break dotfile managers."""
    if path.is_symlink():
        try:
            return path.resolve(strict=True)
        except OSError as exc:
            raise ConfigError(f"{path}: broken symlink: {exc}") from exc
    return path


def read_text(path: Path) -> str | None:
    try:
        return path.read_text(encoding="utf-8-sig")
    except FileNotFoundError:
        return None
    except OSError as exc:
        raise ConfigError(f"{path}: {exc}") from exc


def write_text(path: Path, text: str, mode: int | None) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)
        if mode is not None:
            os.chmod(tmp_name, stat.S_IMODE(mode))
        os.replace(tmp_name, path)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except OSError:
            pass
        raise


def load_json(path: Path) -> tuple[dict, int | None]:
    resolved = writable_path(path)
    text = read_text(resolved)
    if text is None or not text.strip():
        return {}, None
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        raise ConfigError(f"{resolved}: invalid JSON: {exc}") from exc
    if not isinstance(data, dict):
        raise ConfigError(f"{resolved}: root must be a JSON object")
    try:
        mode = resolved.stat().st_mode
    except OSError:
        mode = None
    return data, mode


def save_json(path: Path, data: dict, mode: int | None) -> None:
    resolved = writable_path(path)
    text = json.dumps(data, indent=2, ensure_ascii=False) + "\n"
    write_text(resolved, text, mode)


def json_entry(fmt: str, command: str, args: list[str], cwd: str = "") -> dict:
    if fmt == "opencode":
        return {"type": "local", "command": [command, *args]}
    if fmt in ("claude", "agents"):
        return {"type": "stdio", "command": command, "args": args}
    if fmt == "antigravity":
        # Antigravity spawns stdio servers with cwd "/" and does not send MCP
        # roots. Its supported workaround is an explicit workspace argument.
        if args == ["serve"]:
            args = ["serve", "${workspaceFolder}"]
        entry = {"command": command, "args": args, "disabled": False}
        if cwd:
            entry["cwd"] = cwd
        return entry
    return {"command": command, "args": args}


def object_child(parent: dict, key: str) -> dict:
    value = parent.get(key)
    if value is None:
        value = {}
    if not isinstance(value, dict):
        raise ConfigError(f'"{key}" must be a JSON object')
    parent[key] = value
    return value


def json_install(
    data: dict, fmt: str, name: str, command: str, args: list[str], cwd: str = ""
) -> None:
    if fmt == "opencode":
        mcp = object_child(data, "mcp")
        servers = object_child(mcp, "servers")
        # Remove the pre-V2 layout so the same server is not configured twice.
        mcp.pop(name, None)
        servers[name] = json_entry(fmt, command, args)
        if "$schema" not in data:
            ordered = {"$schema": "https://opencode.ai/config.json", **data}
            data.clear()
            data.update(ordered)
        return
    servers = object_child(data, "mcpServers")
    servers[name] = json_entry(fmt, command, args, cwd)


def json_uninstall(data: dict, fmt: str, name: str) -> bool:
    removed = False
    if fmt == "opencode":
        mcp = data.get("mcp")
        if isinstance(mcp, dict):
            servers = mcp.get("servers")
            if isinstance(servers, dict) and name in servers:
                del servers[name]
                removed = True
                if not servers:
                    mcp.pop("servers", None)
            if name in mcp:
                del mcp[name]
                removed = True
            if not mcp:
                data.pop("mcp", None)
        return removed

    servers = data.get("mcpServers")
    if isinstance(servers, dict) and name in servers:
        del servers[name]
        removed = True
        if not servers:
            data.pop("mcpServers", None)
    return removed


def json_status(data: dict, fmt: str, name: str) -> bool:
    if fmt == "opencode":
        mcp = data.get("mcp")
        if not isinstance(mcp, dict):
            return False
        servers = mcp.get("servers")
        return (isinstance(servers, dict) and name in servers) or name in mcp
    servers = data.get("mcpServers")
    return isinstance(servers, dict) and name in servers


def split_toml_key(raw: str) -> list[str]:
    """Split a dotted TOML key, honoring single/double quotes."""
    parts: list[str] = []
    buf: list[str] = []
    quote: str | None = None
    escaped = False
    for char in raw.strip():
        if quote is not None:
            if escaped:
                buf.append(char)
                escaped = False
            elif char == "\\" and quote == '"':
                escaped = True
            elif char == quote:
                quote = None
            else:
                buf.append(char)
        elif char in ("'", '"'):
            quote = char
        elif char == ".":
            parts.append("".join(buf).strip())
            buf = []
        elif char.isspace():
            continue
        else:
            buf.append(char)
    parts.append("".join(buf).strip())
    return parts


def toml_header_path(line: str) -> list[str] | None:
    stripped = line.lstrip()
    # An array value can look like a table header to a line scanner.
    if stripped.startswith('["') or stripped.startswith("['"):
        return None
    match = TOML_HEADER.match(line)
    if not match:
        return None
    return split_toml_key(match.group(1))


def codex_match(header: list[str], name: str) -> bool:
    return len(header) >= 2 and header[0] == "mcp_servers" and header[1] == name


def codex_blocks(lines: list[str], name: str) -> list[tuple[int, int]]:
    """Return [start, end) line ranges for the server table and its children."""
    ranges: list[tuple[int, int]] = []
    index = 0
    while index < len(lines):
        header = toml_header_path(lines[index])
        if header is None or not codex_match(header, name):
            index += 1
            continue
        end = index + 1
        while end < len(lines) and toml_header_path(lines[end]) is None:
            end += 1
        ranges.append((index, end))
        index = end
    return ranges


def codex_remove(text: str, name: str) -> str:
    lines = text.splitlines()
    ranges = codex_blocks(lines, name)
    for start, end in reversed(ranges):
        del lines[start:end]
    return "\n".join(lines)


def codex_status(path: Path, name: str) -> bool:
    text = read_text(path)
    if text is None:
        return False
    try:
        import tomllib  # type: ignore[import-not-found]
    except ImportError:
        tomllib = None
    if tomllib is not None:
        try:
            data = tomllib.loads(text)
        except tomllib.TOMLDecodeError as exc:
            raise ConfigError(f"{path}: invalid TOML: {exc}") from exc
        servers = data.get("mcp_servers")
        return isinstance(servers, dict) and name in servers
    return bool(codex_blocks(text.splitlines(), name))


def toml_string(value: str) -> str:
    out = ['"']
    for char in value:
        code = ord(char)
        if char == "\\":
            out.append("\\\\")
        elif char == '"':
            out.append('\\"')
        elif char == "\b":
            out.append("\\b")
        elif char == "\t":
            out.append("\\t")
        elif char == "\n":
            out.append("\\n")
        elif char == "\f":
            out.append("\\f")
        elif char == "\r":
            out.append("\\r")
        elif code < 0x20:
            out.append(f"\\u{code:04x}")
        else:
            out.append(char)
    out.append('"')
    return "".join(out)


def codex_render(name: str, command: str, args: list[str]) -> str:
    lines = [
        f"[mcp_servers.{toml_string(name)}]",
        f"command = {toml_string(command)}",
    ]
    if args:
        rendered = ", ".join(toml_string(arg) for arg in args)
        lines.append(f"args = [{rendered}]")
    return "\n".join(lines)


def codex_parse(text: str, path: Path) -> dict | None:
    try:
        import tomllib  # type: ignore[import-not-found]
    except ImportError:
        return None
    try:
        return tomllib.loads(text)
    except tomllib.TOMLDecodeError as exc:
        raise ConfigError(f"{path}: generated invalid TOML: {exc}") from exc


def codex_validate(text: str, path: Path, name: str) -> None:
    data = codex_parse(text, path)
    if data is None:
        return
    servers = data.get("mcp_servers")
    if not isinstance(servers, dict) or name not in servers:
        raise ConfigError(f"{path}: generated TOML is missing mcp_servers.{name}")


def codex_has_dotted_key(text: str, name: str) -> bool:
    current: list[str] | None = None
    key = re.escape(name)
    pattern = re.compile(rf"^\s*(?:\"{key}\"|'{key}'|{key})\s*=")
    for line in text.splitlines():
        header = toml_header_path(line)
        if header is not None:
            current = header
            continue
        if current == ["mcp_servers"] and pattern.match(line):
            return True
    return False


def codex_install(path: Path, name: str, command: str, args: list[str]) -> None:
    resolved = writable_path(path)
    text = read_text(resolved) or ""
    for line in text.splitlines():
        if re.match(r"^\s*mcp_servers\s*=", line):
            raise ConfigError(
                f"{resolved}: mcp_servers is defined as an inline table; "
                "edit it manually"
            )
    if codex_has_dotted_key(text, name):
        raise ConfigError(
            f"{resolved}: mcp_servers.{name} is defined as a dotted key; "
            "edit it manually"
        )
    text = codex_remove(text, name).rstrip()
    block = codex_render(name, command, args)
    updated = f"{text}\n\n{block}\n" if text else f"{block}\n"
    codex_validate(updated, resolved, name)
    mode = resolved.stat().st_mode if resolved.exists() else None
    write_text(resolved, updated, mode)


def codex_uninstall(path: Path, name: str) -> bool:
    resolved = writable_path(path)
    text = read_text(resolved)
    if text is None:
        return False
    if not codex_status(resolved, name):
        return False
    updated = codex_remove(text, name).rstrip()
    updated = f"{updated}\n" if updated else ""
    if updated:
        codex_parse(updated, resolved)
    mode = resolved.stat().st_mode
    write_text(resolved, updated, mode)
    return True


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("status", "install", "uninstall"))
    parser.add_argument("--format", required=True, choices=FORMATS)
    parser.add_argument("--config", required=True)
    parser.add_argument("--name", required=True)
    parser.add_argument("--command", default="")
    parser.add_argument("--args", default=None)
    parser.add_argument("--cwd", default=None)
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    path = expand_path(args.config)
    name = args.name
    raw_args = args.args if args.args is not None else os.environ.get("MCP_ARGS", "")
    cwd = args.cwd if args.cwd is not None else os.environ.get("MCP_CWD", "")
    if not name or not re.fullmatch(r"[A-Za-z0-9_-]+", name):
        print(
            f"error: invalid MCP server name {name!r} "
            "(use letters, digits, _ or -)",
            file=sys.stderr,
        )
        return 2

    try:
        if args.format == "codex":
            if args.action == "status":
                return 0 if codex_status(path, name) else 1
            if args.action == "install":
                if not args.command:
                    raise ConfigError("--command is required for install")
                codex_install(path, name, args.command, shlex.split(raw_args))
                return 0
            removed = codex_uninstall(path, name)
            return 0 if removed else 1

        data, mode = load_json(path)
        if args.action == "status":
            return 0 if json_status(data, args.format, name) else 1
        if args.action == "install":
            if not args.command:
                raise ConfigError("--command is required for install")
            json_install(
                data, args.format, name, args.command, shlex.split(raw_args), cwd
            )
            save_json(path, data, mode)
            return 0
        removed = json_uninstall(data, args.format, name)
        if removed:
            save_json(path, data, mode)
        return 0 if removed else 1
    except (ConfigError, OSError, ValueError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
