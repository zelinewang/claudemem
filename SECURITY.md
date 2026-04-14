# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it via [GitHub Issues](https://github.com/zelinewang/claudemem/issues) with the label "security".

## Security Design

- **Opt-in network**: Default install makes no network calls. Network activates only
  when the user explicitly selects a backend via `claudemem setup`:
  - TF-IDF (default) or offline — no network ever
  - Local Ollama — localhost only
  - Gemini / Voyage / OpenAI — external API calls (user must opt in per machine)
  No telemetry, no analytics.
- **Secrets never on disk**: API keys for cloud backends are read from environment
  variables only (`GEMINI_API_KEY`, `VOYAGE_API_KEY`, `OPENAI_API_KEY`). The
  `IsSecretKey` guard in `pkg/config/wizard.go` refuses `claudemem config set`
  attempts on secret-looking keys; `config.json` stores only the env var NAME.
- **Local-only storage**: Notes, sessions, and SQLite index stay at `~/.claudemem/`
  with `0600`/`0700` permissions. Optional cross-machine sync (`claudemem sync`)
  ships only markdown via a user-provided private git remote.
- **Input validation**: Path traversal protection, SQL parameterized queries,
  input length limits.
- **Dependency scanning**: `govulncheck` reports no code-level vulnerabilities in
  project packages. Stdlib CVEs follow the Go toolchain version in use.
- **Open source**: ~8,500 lines of Go, fully auditable.

## Supported Versions

| Version | Supported |
|---------|-----------|
| 1.0.x   | ✅        |

## Vulnerability Scanning

We run `govulncheck` before each release. Dependencies are kept up to date.
