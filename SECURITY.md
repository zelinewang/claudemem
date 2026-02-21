# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it via [GitHub Issues](https://github.com/zelinewang/claudemem/issues) with the label "security".

## Security Design

- **Zero network**: The default binary makes no network calls. No telemetry, no analytics, no cloud.
- **Local-only storage**: All data stays at `~/.claudemem/` with `0600`/`0700` permissions.
- **Input validation**: Path traversal protection, SQL parameterized queries, input length limits.
- **Dependency scanning**: `govulncheck` reports 0 vulnerabilities in the current release.
- **Open source**: ~4,800 lines of Go, fully auditable.

## Supported Versions

| Version | Supported |
|---------|-----------|
| 1.0.x   | ✅        |

## Vulnerability Scanning

We run `govulncheck` before each release. Dependencies are kept up to date.
