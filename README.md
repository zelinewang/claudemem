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

# Utilities
claudemem stats
claudemem verify
claudemem repair
claudemem config set/get/list/delete <key> [value]
claudemem export backup.tar.gz
claudemem import backup.tar.gz
```

Add `--format json` to any command for structured output.

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
- **FTS5 search** — full-text search across all notes and sessions in <10ms
- **Zero network** — everything local, no cloud, no telemetry
- **Portable** — export/import as tar.gz, move between machines
- **200+ tests** — unit, integration, E2E, and black-box feature tests

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
make test           # Quick smoke test
make e2e-test       # 10 end-to-end CLI tests
make feature-test   # 82 black-box feature tests (7 levels)
make test-all       # All tests: unit + smoke + e2e + feature

# Go unit tests directly
go test ./... -v    # 107 unit tests
```

### Test coverage

| Layer | Tests | What it covers |
|-------|-------|---------------|
| Go unit tests | 107 | Models, validation, slugify, markdown round-trip, storage, dedup, search, cross-ref, integrity |
| Smoke test | 5 | Basic note/session/search/stats |
| E2E CLI | 10 | Full CLI flag testing, JSON output, metadata |
| Feature tests | 82 | 7 levels: CRUD, search, dedup, cross-ref, edge cases, boundaries, data lifecycle |
| **Total** | **204** | |

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

**What claudemem actually does**: zero network calls, all data local, parameterized SQL queries, path traversal protection, 200+ tests passing. Full source: ~5,500 lines of Go, fully auditable.

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
