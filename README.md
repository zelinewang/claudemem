# claudemem

Persistent memory for AI coding agents — notes and session reports that survive across conversations, searchable by FTS5 keyword **and** semantic vectors, in a single zero-network Go binary.

[![CI](https://img.shields.io/github/actions/workflow/status/zelinewang/claudemem/ci.yml?label=CI)](https://github.com/zelinewang/claudemem/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/zelinewang/claudemem)](https://github.com/zelinewang/claudemem/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/zelinewang/claudemem)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Your agent forgets everything between sessions. claudemem gives it a durable,
inspectable memory: knowledge notes and work-session reports, stored as plain
Markdown, indexed for fast search, and cross-linked so any fact traces back to
the session that produced it.

```console
$ claudemem note add architecture --title "FTS5 sanitization" \
    --content "Hyphens and quotes in queries are escaped before the MATCH clause." \
    --tags "sqlite,search"
✓ Added note to architecture: "FTS5 sanitization" (id: fee13be2)

$ claudemem session save --title "Ship v3 release pipeline" --branch feat/goreleaser \
    --project claudemem --session-id s1 --summary "Wired goreleaser + CI; cut v3.0.12."
✓ Saved session: "Ship v3 release pipeline" (2026-07-12, feat/goreleaser) [id: 2ec8c555]

$ claudemem search "FTS5"
Found 1 results for "FTS5":
1. 📝 [note] FTS5 sanitization
   category: architecture | tags: sqlite, search
   Hyphens and quotes in queries are escaped before the MATCH clause.

$ claudemem stats
ClaudeMem Statistics
====================
Notes:    1
Sessions: 1
Storage:  100.6 KB
```

## Highlights

- **Markdown is the source of truth** — every note and session is a human-readable `.md` file with YAML frontmatter. The SQLite index is a regenerable cache, not the record.
- **Hybrid search** — FTS5 keyword search fused with semantic vectors, score-weighted for the keyword-heavy queries agents actually make.
- **Zero network by default** — no telemetry, no daemon. Cloud embedding backends are an explicit per-machine opt-in; API keys come from env vars only, never written to disk.
- **Bidirectional cross-referencing** — notes link to the session that produced them; sessions list their related notes. Nothing is a dead end.

## Install

```bash
npx skills add zelinewang/claudemem --skill claudemem --global
```

This installs only the `claudemem` skill at user level. Next time you start
Claude Code (or Cursor, Gemini CLI, etc.), it just works. Your saved data lives
in `~/.claudemem/` and is never touched by upgrades. To upgrade later, run
`npx skills update claudemem --global`.

Prefer to build it yourself? See [Build from source](#build-from-source).

## Talk to It Naturally

During your work claudemem silently saves important context — API specs,
decisions, quirks, resolved bugs. When you start a new task it searches past
knowledge automatically. You can also drive it in plain language:

| Say this | What happens |
|----------|-------------|
| "remember this" | Saves the current info as a note |
| "what do you remember about TikTok" | Searches past notes |
| "wrap up" | Saves a detailed session report + extracts notes |
| "what did we do last time" | Shows recent sessions |

Or use slash commands directly: `/wrapup`, `/recall [topic]`.

## What Gets Saved

```
~/.claudemem/
├── notes/          ← knowledge fragments (markdown)
├── sessions/       ← work reports with cross-links (markdown)
└── .index/         ← search index (auto-rebuilt)
```

Everything is plain Markdown with YAML frontmatter — human-readable,
git-friendly, portable.

## Architecture

Every write goes to **both** the Markdown files and the SQLite index. The
Markdown is authoritative; the index (FTS5 rows + embedding vectors) is a cache
that can be dropped and rebuilt from the Markdown at any time.

```
        claudemem note add / session save
                       │
            ┌──────────┴───────────┐
            ▼                      ▼
   ~/.claudemem/*.md         SQLite  .index/
   Markdown + YAML           FTS5 rows + vectors
   = source of truth         = regenerable cache
            │                      │
            └──────────┬───────────┘
                       ▼
      search  =  FTS5 keyword  ⊕  semantic vectors
                (keyword-weighted score fusion)
```

Notes and sessions look similar but dedupe on opposite keys, because they mean
different things:

| | Notes | Sessions |
|---|---|---|
| **Purpose** | Knowledge fragments | Work reports |
| **Dedup key** | Same title + category | Same `session_id` |
| **Merge behavior** | Append content | Append all sections |
| **Cross-link** | `metadata.session_id` | `## Related Notes` |

Knowledge accumulates by topic (same note grows), while distinct conversations
stay separate (each session is its own record). Schema migrations preserve
existing data across upgrades.

## CLI Reference

```bash
# Notes (knowledge fragments)
claudemem note add <category> --title "..." --content "..." [--tags "..."] [--session-id "..."]
claudemem note search "query" [--in category] [--tag tags]
claudemem note list [category]
claudemem note get <id-or-prefix>
claudemem note append <id> "additional content"
claudemem note update <id> --title "..." --content "..." --tags "..."
claudemem note delete <id>
claudemem note categories
claudemem note tags

# Sessions (work reports)
claudemem session save --title "..." --branch "..." --project "..." --session-id "..." \
  [--related-notes "id:title:category,..."] [--content "..."]
claudemem session list [--last N] [--branch X] [--date-range 7d]
claudemem session search "query" [--branch X]
claudemem session get <id-or-prefix>

# Search everything
claudemem search "query" [--type note|session] [--limit N]

# Embedding backend (pick one; no silent fallback)
claudemem setup                              # interactive wizard: Local / Gemini / Voyage / OpenAI / TF-IDF
claudemem health                             # I1-I3 parity check (markdown ↔ FTS ↔ vectors, <100ms)
claudemem health --deep                      # also I4/I5 (orphans, config match)
claudemem repair                             # fix drift detected by health (interactive)
claudemem repair --prune-stale               # also delete vectors left by previously-used backends

# Cross-machine sync (markdown-only via git)
claudemem sync init <remote-url>             # git init ~/.claudemem with remote
claudemem sync push                          # commit + push notes/sessions
claudemem sync pull                          # pull + rebuild FTS + embed missing vectors
claudemem sync status                        # git status + index health

# Utilities
claudemem stats
claudemem verify
claudemem config set/get/list/delete <key> [value]
claudemem export backup.tar.gz
claudemem import backup.tar.gz
```

Add `--format json` to any command for structured output.

## Search Backends

Semantic search needs an embedding model. You pick it explicitly — claudemem
never falls back to a worse backend behind your back.

```bash
claudemem setup
```

| Option | Where it runs | Cost | Multilingual | When to pick |
|---|---|---|---|---|
| **Local — Ollama** | Your machine | Free | qwen3 strong / nomic weaker | Daily use, offline, airgapped |
| **Cloud — Gemini** | Google | ~$0.15/M tokens | 100+ langs | Best quality, you already have a key |
| **Cloud — Voyage** | Voyage AI | ~$0.02/M, 200M free | Yes | Budget pick, effectively free |
| **Cloud — OpenAI** | OpenAI | ~$0.02/M (3-small) | English-heavy | You already pay OpenAI |
| **TF-IDF** | Your machine | Free | OK | No daemon, no keys, keyword-ish similarity |

API keys are read from environment variables (`GEMINI_API_KEY`,
`VOYAGE_API_KEY`, `OPENAI_API_KEY`) — claudemem refuses to store them in
`config.json`. Only the env var **name** is recorded, so configs are safe to
commit and sync across machines.

**When the backend is down**, claudemem never degrades silently:

- Non-interactive shells / CI → error with recovery instructions + exit 1.
- Interactive terminals → prompt offering retry / FTS-only-this-query / run setup.

Use `claudemem search "..." --fts-only` to skip semantic for one query.

## Cross-Machine Memory

Memory can follow you between machines via a private git repo. Only Markdown
travels over the wire — the SQLite index and config stay per-machine, so a
cloud-Gemini laptop and a local-Ollama workstation share one corpus with zero
backend coupling.

```bash
# once, per user (HTTPS works with gh auth / keychain)
claudemem sync init https://github.com/YOU/claudemem-memory.git

# after work
claudemem sync push

# on another machine, first time
claudemem sync init https://github.com/YOU/claudemem-memory.git
claudemem sync pull
```

See [docs/HOOK_INTEGRATION.md](docs/HOOK_INTEGRATION.md) for auto-pull on
SessionStart and auto-push on SessionEnd via Claude Code hooks.

## Quality Signals

Three test layers, all green. Numbers below are reproducible from a clean
checkout:

| Layer | Command | Result |
|-------|---------|--------|
| Go unit + coverage | `go test ./... -cover` | all 7 packages pass · `pkg/models` 94.7% · `pkg/storage` 81.4% · `pkg/hooks` 79.2% |
| E2E CLI | `make e2e-test` | 23 passed, 0 failed |
| Black-box feature | `make feature-test` | 82 passed across 7 levels |
| Everything | `make test-all` | unit + smoke + E2E + feature |

The full matrix (`make test-all`) runs in GitHub Actions for pull requests that
change Go source, modules, the Makefile, CI workflow, or test harnesses (see
`.github/workflows/ci.yml`). There are 364 Go test functions across `cmd/` and
`pkg/`; all suites use temp directories, so there are zero local-environment
dependencies.

## Security

skills.sh shows "High Risk" / "Critical Risk" badges — this is normal for **any
skill that runs CLI commands**. What is actually happening:

| Scanner | Flag | Why | Real risk |
|---------|------|-----|-----------|
| Generic | High | Skill uses Bash to run `claudemem` | Every useful CLI skill needs this |
| Socket | 1 alert | `install.sh` fetches a binary via curl | Standard Go distribution |
| Snyk | Critical | `modernc.org/sqlite` (C-to-Go transpile) carries upstream CVEs | Industry-standard pure-Go SQLite |

What claudemem actually does: zero network by default (TF-IDF or offline
Ollama); cloud embedding backends are opt-in per machine via `claudemem setup`,
with API keys from env vars only. Parameterized SQL, FTS5 query sanitization,
path-traversal protection, `0600`/`0700` storage permissions. About 13,000 lines
of Go source (plus ~9,000 lines of tests), fully auditable. Running
`govulncheck ./...` before a release is currently a recommended manual check,
not an automated release gate. See [SECURITY.md](SECURITY.md).

## Build from Source

```bash
git clone https://github.com/zelinewang/claudemem.git
cd claudemem
make build          # single static binary (CGO_ENABLED=0, pure Go)
make install        # install to ~/.local/bin/
./claudemem --version
```

Requires Go 1.25+. The build is a single pure-Go binary with no runtime
dependencies; goreleaser (`.goreleaser.yml`) cross-compiles linux/darwin/windows
on amd64/arm64 for releases.

## Status

Stable and in daily use; latest release
[v3.0.12](https://github.com/zelinewang/claudemem/releases). The storage format
is backward-compatible across upgrades — schema migrations preserve existing
notes and sessions. Issues and PRs welcome.

## License

[MIT](LICENSE) © Zane Wang

## References

- [braindump](https://github.com/MohGanji/braindump) — Go-based persistent notes for AI agents
- [claude-done](https://github.com/Genuifx/claude-done) — session-summary saving for Claude Code
