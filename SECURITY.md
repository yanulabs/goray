# Security Policy

## Supported versions

Security fixes are provided for the latest released minor line.

| Version | Supported |
| --- | --- |
| 0.1.x | Yes |
| Older | No |

## Reporting a vulnerability

Do not open a public issue, discussion, or pull request for a security vulnerability.

Report it privately through GitHub Security Advisories:

<https://github.com/yanulabs/goray/security/advisories/new>

Include:

- The affected version (`goray version`).
- Operating system and architecture.
- A minimal reproduction or proof of concept.
- Impact and any known mitigations.
- Whether the issue affects the CLI, MCP server, report output, summary cache, or installer.

You will receive an acknowledgment and a remediation plan through the advisory. Please allow time for a fix before public disclosure.

## Scope

In scope:

- Arbitrary code execution or command injection through CLI flags, MCP tool arguments, package paths, or installer inputs.
- Path traversal or unintended filesystem writes outside documented cache/report/config locations.
- SQL injection or cache corruption triggered by untrusted input.
- MCP protocol parsing, authorization, or denial-of-service issues.
- Supply-chain or release artifact integrity issues.

Out of scope:

- Vulnerabilities in Go, `golang.org/x/tools`, `modernc.org/sqlite`, or client applications that are already fixed upstream.
- Results produced by analyzing intentionally malicious Go source; index generation is not a sandbox.
- Publicly documented behavior such as opening a browser during `goray export --open`.
- Missing hardening that requires an attacker to already control the local machine or repository.
