# claudemem

Persistent memory for AI coding agents. Notes and session summaries that survive across conversations — with bidirectional cross-referencing.

## Install

```bash
npx skills add zelinewang/claudemem
```

That's it. Next time you start Claude Code (or Cursor, Gemini CLI, etc.), it just works.

### Upgrade

```bash
npx skills add zelinewang/claudemem -y -g
```

Same command as install. Overwrites with the latest version. Your saved data (`~/.claudemem/`) is never touched.

## How It Works

**claudemem remembers things for you across conversations.**

During your work, it silently saves important context — API specs, decisions, quirks, resolved bugs. When you start a new task, it searches past knowledge automatically.

You can also talk to it naturally:

| Say this | What happens |
|----------|-------------|
| "remember this" | Saves the current info as a note |
| "what do you remember about TikTok" | Searches past notes |
| "wrap up" | Saves detailed session report + extracts notes |
| "what did we do last time" | Shows recent sessions |

Or use slash commands: `/wrapup`, `/recall [topic]`

## What Gets Saved

```
~/.claudemem/
├── notes/          ← knowledge fragments (markdown)
├── sessions/       ← work reports with cross-links (markdown)
└── .index/         ← search index (auto-rebuilt)
```

Everything is plain Markdown with YAML frontmatter. Human-readable, git-friendly, portable.

## CLI Quick Reference

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

# Cross-machine sync (markdown-only via git)
claudemem sync init <remote-url>             # git init ~/.claudemem with remote
claudemem sync push                          # commit + push notes/sessions
claudemem sync pull                          # pull + rebuild FTS + embed missing vectors
claudemem sync status                        # git status + index health

# Utilities
claudemem stats
claudemem verify
claudemem repair
claudemem config set/get/list/delete <key> [value]
claudemem export backup.tar.gz
claudemem import backup.tar.gz
```

Add `--format json` to any command for structured output.

## Setup — Pick Your Search Backend

Semantic search uses an embedding model. Pick it explicitly — claudemem never
falls back silently to a worse backend behind your back.

```bash
claudemem setup
```

The wizard walks through:

| Option | Where it runs | Cost | Chinese | When to pick |
|---|---|---|---|---|
| **Local — Ollama** | Your machine | Free | qwen3 ✅ / nomic weaker | Daily use, offline, airgapped |
| **Cloud — Gemini** | Google | $0.15/M tokens (≈$0.50/mo for 3K notes) | ✅ 100+ langs | Best quality, you already have a key |
| **Cloud — Voyage** | Voyage AI | $0.02/M, 200M free tokens | ✅ | Budget pick, effectively free |
| **Cloud — OpenAI** | OpenAI | $0.02/M (3-small) | ⚠️ English-heavy | You already pay OpenAI for other things |
| **TF-IDF** | Your machine | Free | OK | No daemon, no keys, keyword-ish similarity |

API keys are always read from environment variables (`GEMINI_API_KEY`,
`VOYAGE_API_KEY`, `OPENAI_API_KEY`) — claudemem refuses to store them in
`config.json`. Only the env var **name** is recorded, so configs are safe
to commit + sync across machines.

Manual equivalent (for scripts):

```bash
claudemem config set embedding.backend gemini
claudemem config set embedding.model gemini-embedding-001
claudemem config set embedding.dimensions 768
claudemem config set embedding.api_key_env GEMINI_API_KEY
claudemem reindex --vectors
```

### When the backend is down

claudemem never degrades silently. If the configured backend is unreachable:

- **Non-interactive shells / CI**: error with recovery instructions + exit 1.
- **Interactive terminals**: prompt offering retry / FTS-only-this-query / run setup.

Use `claudemem search "..." --fts-only` to skip semantic for one query
when you know the backend is down.

## Cross-Machine Memory

Memory can follow you between machines (web_dev ↔ MacBook, etc.) via a
private git repo.

```bash
# once, per user
claudemem sync init git@github.com:YOU/claudemem-memory.git

# after work
claudemem sync push

# on another machine, first time
git clone git@github.com:YOU/claudemem-memory.git ~/.claudemem
claudemem sync pull
```

Only markdown travels over the wire — SQLite index and config stay
per-machine. Each machine embeds under ITS configured backend, so
a cloud-Gemini laptop and a local-Ollama workstation can share the
same corpus with zero backend coupling.

See [docs/HOOK_INTEGRATION.md](docs/HOOK_INTEGRATION.md) for Claude Code
hook integration (auto-pull on SessionStart, auto-push on SessionEnd).

## Recommended: Auto Wrap-Up

Want every session saved automatically? Add this to your `~/.claude/CLAUDE.md`:

```markdown
### Session Memory — Auto Wrap-Up
- Before ending any conversation, automatically execute `/wrapup` to save knowledge and session summary.
- Do not ask permission — just do it as the final action.
```

## Key Features

- **Cross-referencing** — notes link to sessions, sessions link to notes. Trace any knowledge back to its source
- **Custom sections preserved** — architecture diagrams, performance tables, file lists — nothing silently dropped
- **Smart dedup** — notes merge by topic; sessions stay separate by conversation (session_id-based)
- **FTS5 search** — full-text search across all notes and sessions in <10ms, with automatic query sanitization (hyphens, quotes, special chars handled safely)
- **Hybrid search** — FTS5 keyword search + semantic vector search (Gemini / Voyage / OpenAI / Ollama / TF-IDF). Score fusion tuned for keyword-heavy memory queries
- **Opt-in network** — default is zero-network (TF-IDF or offline). Cloud embedding backends are explicit per-machine choices via `claudemem setup`; API keys come from env vars only, never stored in config
- **Portable** — export/import as tar.gz, move between machines
- **440+ tests** — 331 unit (82% coverage), 23 E2E, 82 black-box feature tests across 7 levels

## For Developers

### Build from source

```bash
git clone https://github.com/zelinewang/claudemem.git
cd claudemem
make build          # Build binary
make install        # Install to ~/.local/bin/
```

### Run tests

```bash
make test           # Quick smoke test (5 operations)
make e2e-test       # 23 end-to-end CLI tests
make feature-test   # 82 black-box feature tests (7 levels)
make test-all       # All tests: unit + smoke + e2e + feature

# Go unit tests directly
go test ./... -v    # 331 unit tests, 82% coverage
```

### Test coverage

| Layer | Tests | What it covers |
|-------|-------|---------------|
| Go unit tests | 331 | Models, validation, markdown, storage, dedup, search, cross-ref, integrity, faceted search, timestamp parsing, FTS5 sanitization, session merge, vector store, migration |
| Smoke test | 5 | Basic note/session/search/stats |
| E2E CLI | 23 | Full CLI flag testing, JSON output, metadata, cross-referencing, capture, graph |
| Feature tests | 82 | 7 levels: CRUD, search, dedup, cross-ref, edge cases, boundaries, data lifecycle |
| **Total** | **441** | **82.2% statement coverage** on core storage package |

All tests use temp directories — zero local environment dependencies, fully replicatable.

## Architecture

Notes and sessions have fundamentally different dedup strategies:

| | Notes | Sessions |
|---|---|---|
| **Purpose** | Knowledge fragments | Work reports |
| **Dedup key** | Same title + category | Same session_id |
| **Merge behavior** | Append content | Append all sections |
| **Cross-link** | `metadata.session_id` | `## Related Notes` |

This ensures knowledge accumulates naturally while different conversations stay separate.

## Security

skills.sh shows "High Risk" / "Critical Risk" badges — this is normal for **any skill that runs CLI commands**. Here's what's actually going on:

| Scanner | Flag | Why | Real risk |
|---------|------|-----|-----------|
| Gen | High | Skill uses Bash to run `claudemem` | All useful skills need this |
| Socket | 1 alert | `install.sh` downloads binary via curl | Standard Go distribution |
| Snyk | Critical | `modernc.org/sqlite` (C-to-Go transpile) has CVEs | Industry-standard SQLite lib |

**What claudemem actually does**: zero network by default (TF-IDF or offline Ollama); cloud embedding backends are opt-in per-machine via `claudemem setup` with API keys from env vars only. Parameterized SQL queries, FTS5 query sanitization, path traversal protection, 441 tests passing (82% coverage). Full source: ~11,400 lines of Go, fully auditable.

## Tell a Friend

> Install persistent memory for Claude Code in 10 seconds:
> ```
> npx skills add zelinewang/claudemem
> ```
> Now say "remember this" or "wrap up" — it just works.

## License

MIT

## References

- [braindump](https://github.com/MohGanji/braindump) — Go-based persistent notes for AI agents
- [claude-done](https://github.com/Genuifx/claude-done) — Session summary saving for Claude Code
