# claudemem — Project Guide

Persistent memory CLI for AI coding agents. Notes + sessions with bidirectional cross-referencing.

## Tech Stack
- **Language**: Go 1.24+ / toolchain 1.25 (CGO_ENABLED=0, pure Go, single static binary)
- **CLI**: spf13/cobra
- **Storage**: Markdown files (source of truth) + SQLite FTS5 index (modernc.org/sqlite)
- **Testing**: `go test` (330+ unit, 82.1% coverage) + `e2e_test.sh` (23 E2E) + `tests/feature_test.sh` (82 black-box)

## Key Directories
- `cmd/` — Cobra CLI commands (24 files)
- `pkg/storage/` — Dual-backend storage (Markdown + SQLite FTS5)
- `pkg/models/` — Note and Session data models
- `pkg/config/` — JSON config management
- `skills/claudemem/` — Claude Code skill definition + slash commands
- `tests/` — Black-box feature test suite

## Core Constraints (Inviolable)
1. **Opt-in network** — Default install makes **zero** network calls. Network is only
   activated when the user explicitly selects a backend that needs it via
   `claudemem setup`:
   - TF-IDF (default) → no network ever
   - Local Ollama → localhost only
   - Gemini / Voyage / OpenAI → external API calls (explicit user choice)
   Secrets (API keys) are **always** env-var-only; config.json stores only the env
   var NAME. Enforced by `IsSecretKey` guard in `pkg/config/wizard.go`.
2. **CGO_ENABLED=0** — All deps must be pure Go. No C bindings.
3. **Single binary** — No runtime dependencies, no daemon processes required by
   the tool itself (user may opt into an Ollama daemon via setup).
4. **Human-readable storage** — Markdown files with YAML frontmatter, always
   inspectable. SQLite index + vectors are regenerable from markdown.
5. **Backward compatible** — Existing notes/sessions must survive upgrades.
   Schema migrations preserve data (see v21→v22 migration in pkg/vectors/store.go).

## Build & Test
```bash
make build              # Build binary
make test               # Smoke tests (5 cases)
make e2e-test           # E2E CLI tests (10 cases)
make feature-test       # Black-box feature tests (82 cases)
make test-all           # All tests: unit + smoke + E2E + feature
make install            # Install to ~/.local/bin/
```

## Data Model
- **Note**: ID, Category, Title, Content, Tags, Metadata (session_id for cross-linking)
- **Session**: ID, Title, Date, Branch, Project, SessionID + structured sections (Summary, Decisions, Changes, Problems, Insights, NextSteps, RelatedNotes, ExtraSections)
- **Cross-refs**: Note ↔ Session bidirectional via metadata.session_id and RelatedNotes

## Storage Pattern
Dual-write: every note/session writes to both filesystem (Markdown) AND SQLite index.
SQLite is a regenerable cache — Markdown files are the source of truth.
