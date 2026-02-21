# claudemem

Persistent memory for AI coding agents. Notes and session summaries that survive across conversations.

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
| "wrap up" | Saves everything from this session |
| "what did we do last time" | Shows recent sessions |

Or use slash commands: `/wrap-up`, `/save-session`, `/recall [topic]`

## What Gets Saved

```
~/.claudemem/
├── notes/          ← knowledge fragments (markdown)
├── sessions/       ← conversation summaries (markdown)
└── .index/         ← search index (auto-rebuilt)
```

Everything is plain Markdown. Human-readable, git-friendly, portable.

## CLI Quick Reference

```bash
# Notes
claudemem note add <category> --title "..." --content "..."
claudemem note search "query"
claudemem note list

# Sessions
claudemem session save --title "..." --branch "..." --project "." --session-id "..." --content "..."
claudemem session list
claudemem session search "query"

# Search everything
claudemem search "query"

# Backup & restore
claudemem export backup.tar.gz
claudemem import backup.tar.gz

# Stats
claudemem stats
```

## Recommended: Auto Wrap-Up

Want every session to be saved automatically? Add this to your `~/.claude/CLAUDE.md`:

```markdown
### Session Memory — Auto Wrap-Up
- Before ending any conversation, automatically execute `/wrap-up` to save knowledge and session summary.
- Do not ask permission — just do it as the final action.
```

This makes Claude auto-save before every session ends. Don't want it? Just remove those lines.

## Key Features

- **Auto-dedup** — same topic mentioned twice? Content merges, never duplicates
- **FTS5 search** — full-text search across all notes and sessions in <10ms
- **Zero network** — everything local, no cloud, no telemetry
- **Portable** — export/import as tar.gz, move between machines
- **29 commands** — notes, sessions, search, stats, config, export, import, verify, repair

## Security

skills.sh shows "High Risk" / "Critical Risk" badges — this is normal for **any skill that runs CLI commands**. Here's what's actually going on:

| Scanner | Flag | Why | Real risk |
|---------|------|-----|-----------|
| Gen | High | Skill uses Bash to run `claudemem` | All useful skills need this |
| Socket | 1 alert | `install.sh` downloads binary via curl | Standard Go distribution |
| Snyk | Critical | `modernc.org/sqlite` (C-to-Go transpile) has CVEs | Industry-standard SQLite lib |

**What claudemem actually does**: zero network calls, all data local, parameterized SQL queries, path traversal protection, 400+ tests passing. Full source: ~4,800 lines of Go, fully auditable. This is a scanner limitation, not a real vulnerability.

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
