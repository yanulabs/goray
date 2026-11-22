# Contributing to goray

Thanks for helping improve goray. This project is a deterministic Go code-intelligence engine, MCP server, and report generator; changes should preserve reproducible analysis and avoid runtime LLM dependencies.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

Requirements:

- Go 1.26 or newer.
- `golangci-lint` v2 for `make lint` / `make verify`.
- Node.js 20+ and npm only for frontend work under `web/`.
- Python 3.10+ only for `make install-mcp` / `make uninstall-mcp`.

Clone and build:

```sh
git clone https://github.com/yanulabs/goray.git
cd goray
make build
./bin/goray version
```

Install dependencies and run the checks:

```sh
go mod download
make verify
```

`make verify` runs formatting checks, `go vet`, the full race-enabled test suite, and `golangci-lint`. Run it before opening a pull request.

## Repository layout

| Path | Purpose |
| --- | --- |
| `cmd/goray` | CLI entry point and command dispatch |
| `internal/index` | `go/packages` / `go/types` indexing and semantic queries |
| `internal/tools` | MCP tool schemas and handlers |
| `internal/cache` | SQLite working and summary caches |
| `internal/render` | Markdown, JSON, DOT, Mermaid, and HTML rendering |
| `internal/report` | Offline report view model |
| `internal/watch` | Incremental rebuilds |
| `web` | React report source; build output is committed under `internal/render/dist` |
| `scripts` | Installer tooling only; not part of the binary |

## Coding rules

- Preserve deterministic behavior. Do not add runtime network calls or LLM calls to the indexer, CLI, report, or MCP tools.
- Prefer `go/types` and `go/packages` over text matching for semantic questions.
- Keep MCP tool behavior backwards compatible. Add parameters as optional aliases before renaming existing contracts.
- Add focused table-driven tests for parsing, resolution, filters, pagination, and error shapes.
- Keep JSON field names stable; downstream agents depend on them.
- Update `.opencode/skills/goray/SKILL.md` when a tool schema or critical parameter changes. `TestSkillDocCoversAllRegisteredTools` enforces coverage.
- Do not commit generated databases, caches, `bin/`, `dist/`, or `web/node_modules/`.

## Commit messages

Use conventional prefixes where possible:

```text
feat(tools): add compact api surface pagination
fix(index): resolve repo-relative method shorthands
docs: document MCP client installation
test(cache): cover parent summary metadata
ci: validate goreleaser config
```

Keep commits focused. Explain why in the body when the behavior is subtle.

## Pull requests

1. Open an issue first for large features or breaking changes.
2. Add tests and documentation in the same pull request.
3. Ensure `make verify` passes.
4. Fill out the pull request template and link related issues.
5. Do not include secrets, private source code, or personal data in fixtures or logs.

## Release process

Releases use Semantic Versioning and are published from annotated tags.

1. Update `CHANGELOG.md`.
2. Run `make verify`.
3. Validate the release configuration with `make release-check`.
4. Commit the release preparation.
5. Create and push an annotated tag:

   ```sh
   git tag -a v0.1.0 -m "goray v0.1.0"
   git push origin main
   git push origin v0.1.0
   ```

Pushing a `v*` tag runs `.github/workflows/release.yml`, which verifies the tree and publishes GoReleaser archives and `checksums.txt` for Linux, macOS, and Windows on amd64/arm64.

## Reporting security issues

Do not open a public issue for a vulnerability. Follow [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under the [MIT License](LICENSE).
