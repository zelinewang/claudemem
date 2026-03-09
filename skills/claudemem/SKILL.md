---
name: claudemem
description: >
  Persistent memory that survives across conversations. Automatically captures important knowledge
  as notes during work, and saves detailed session reports on demand. Searches past context before
  new tasks. Notes and sessions are cross-linked for full traceability. All local, zero network.
---

# claudemem — Persistent Memory for AI Agents

Memory that carries across conversations. Two simple behaviors:
1. **Automatically** save knowledge notes as you work (silent, no user action needed)
2. **On command** (`/wrapup`) save a detailed session report with cross-linked notes

## Slash Commands

- **/wrapup** [title] — End-of-session: extract knowledge notes + save detailed session report + cross-link everything. This is the primary command.
- **/recall** [topic] — Search persistent memory for a topic, or show recent activity with cross-references.

## Natural Trigger Phrases

These natural phrases also activate memory operations:

**Save knowledge:** "remember this" / "save this" / "note this down"
**Search memory:** "what do you remember about..." / "do you recall..." / "what do we know about..."
**Wrap up:** "wrap up" / "let's wrap up" / "save everything" — triggers /wrapup
**Recall work:** "what did we do last time" / "show me recent sessions"

## Setup

Before first use, verify the CLI is installed. If `claudemem` is not found on PATH:

```bash
curl -fsSL https://raw.githubusercontent.com/zelinewang/claudemem/main/skills/claudemem/scripts/install.sh | bash
```

Verify: `claudemem --version`

## CLI Reference

All commands support `--format json` for structured output.

### Notes (knowledge fragments)
```bash
claudemem note add <category> --title "..." --content "..." --tags "..." [--session-id "..."]
claudemem note search "query" [--in category] [--tag tags] [--format json]
claudemem note list [category]
claudemem note get <id>                      # Full content by ID (supports 8-char prefix)
claudemem note append <id> "additional content"
claudemem note update <id> --content "..." [--title "..."] [--tags "..."]
claudemem note delete <id>
claudemem note categories
claudemem note tags
```

### Sessions (work reports)
```bash
claudemem session save --title "..." --branch "..." --project "..." --session-id "..." [--related-notes "id:title:cat,..."]
claudemem session list [--last N] [--date today] [--date-range 7d] [--branch X]
claudemem session search "query" [--branch X]
claudemem session get <id>
```

### Unified Search
```bash
claudemem search "query" [--type note|session] [--limit N]
claudemem search "query" --compact            # IDs + titles only (~100 tokens vs ~2000)
claudemem search "query" --category api --tag security  # Faceted filters
claudemem search "query" --after 2025-01-01 --before 2025-12-31  # Date range
claudemem search "query" --sort date          # Sort by date (default: relevance with recency boost)
```

Search modes and when each is useful:
- **Default**: Full results with previews, metadata, scores. Best when you need complete context.
- **--compact**: Only IDs, titles, types, scores. ~20x fewer tokens. Best for scanning "do I have anything on this?" before deciding to fetch full content.
- **Faceted filters** (--category, --tag, --after, --before): Narrow results when you know the domain. Combinable with --compact.
- **--sort date**: Chronological ordering. Useful for "what happened recently?" queries. Default sort uses relevance with a recency boost (entries <7 days get up to 20% score boost, decaying over 30 days).

### Context Injection
```bash
claudemem context inject [--limit N] [--project path]
```
Returns recent notes + session summaries + stats in a compact format (~1-2KB).
Designed for session-start context loading, but usable anytime to get a quick overview of stored knowledge.

### Code Intelligence
```bash
claudemem code outline <file>                # Extract symbols (functions, classes, types)
```
Extracts structural outline from source files: function signatures, class definitions, type declarations, method signatures — without full implementation bodies.
Supports Go, Python, TypeScript/JavaScript, Rust. Uses regex-based pattern matching (~80-95% accuracy depending on language; Go is highest via stdlib AST patterns).
Returns ~10-20 tokens per symbol vs ~500+ tokens for full file reads. Useful when you need to understand a file's structure before deciding which parts to read in full.

### Utilities
```bash
claudemem stats                              # Storage statistics
claudemem config set/get/list/delete <key>   # Configuration
claudemem export [output-file]               # Backup as tar.gz
claudemem import <archive-file>              # Restore from backup
claudemem verify                             # Check consistency
claudemem repair                             # Fix orphaned entries
```

## Hook Configuration (Optional)

If you want automatic context loading at conversation start, add to Claude Code settings:

```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "claudemem context inject --limit 5",
        "timeout": 10000
      }]
    }]
  }
}
```

## Agent Guidelines

claudemem provides three capabilities. How and when to use them is your judgment call based on conversation context.

### Capability 1: Knowledge Capture

Save knowledge fragments as notes during conversation — proactively, without asking the user.

**High-value knowledge** (tends to be useful across sessions):
- API specs, endpoints, rate limits, field mappings, authentication quirks
- Technical decisions with rationale (why X over Y, what alternatives were rejected)
- Bug root causes, symptoms, diagnosis patterns, and proven fix approaches
- Configuration quirks, gotchas, environment-specific settings
- Architecture patterns discovered or established in this codebase
- User preferences: coding style, naming conventions, workflow preferences
- Integration quirks: third-party API behaviors, undocumented features, workarounds
- Important commands, URLs, environment configs that someone would need again

**Low-value / skip** (creates noise, not useful across sessions):
- Temporary debugging output or transient state
- Bare file paths or code snippets without explanatory context
- General programming knowledge available in documentation
- Information the user explicitly says is temporary or will change soon
- Meta-commentary about the conversation itself

**Quality practices:**
- Search for duplicates before saving (`note search "<key phrase>"`). Append to existing notes (`note append <id>`) when the topic already exists — this accumulates knowledge rather than scattering it.
- Choose categories that match existing ones (`note categories`) for consistency.
- After saving, show a brief indicator so the user knows: `[noted: "title" -> category]`

Session reports (`session save`) are only created when the user explicitly requests via `/wrapup` or phrases like "wrap up" — never automatically.

### Capability 2: Knowledge Retrieval

Search stored knowledge when prior context would improve your response. The search tools offer different trade-offs:

| Approach | When useful | Token cost |
|----------|------------|------------|
| `search "X" --compact --format json` | Quick scan: "do I know anything about this?" | ~100 tokens |
| `search "X" --format json` | Need full context including previews | ~2000 tokens |
| `search "X" --category Y --tag Z` | Know the domain, want precise results | varies |
| `note get <id>` | Need complete content of a specific note | ~500 tokens |
| `context inject` | Session start: load recent knowledge overview | ~1-2KB |

When prior knowledge is found and used, a brief indicator helps the user know: `[memory: Found "TikTok Rate Limits" — 100/min per API key]`

If a note has `session_id` in metadata, `session get <session_id>` provides the full conversation context that produced it. This enables tracing from a knowledge fragment back to the session where it was discovered.

### Capability 3: Code Intelligence

`code outline <file>` extracts structural symbols from source files — function signatures, classes, types — without full bodies. Useful when you need to understand a file's API surface before deciding which parts to read in full. Costs ~10-20 tokens per symbol vs ~500+ for full file reads.

Currently supports Go, Python, TypeScript/JavaScript, Rust. For unsupported languages, falls back to empty result.

## Cross-Referencing System

Notes and sessions are bidirectionally linked:

- **Note -> Session**: When saving a note during `/wrapup`, include `--session-id "$SESSION_REF"`.
  This stores the session reference in the note's metadata. When viewing a note with `note get`,
  the `metadata.session_id` field shows which session it came from.

- **Session -> Notes**: When saving a session, include `--related-notes "id:title:category,..."`.
  This creates a `## Related Notes` section in the session. When viewing a session, you can see
  all knowledge extracted from it.

This enables:
- From any note: trace back to the session that produced it (context/rationale)
- From any session: see all knowledge extracted (what was learned)
- In search results: follow cross-references to get the full picture

## Session Report Template

When `/wrapup` is invoked, the session report must include these sections:

```markdown
## Summary
2-3 substantial paragraphs: goal, what was accomplished, current state, significance.

## What Happened
Numbered paragraphs (not bullets). Each phase: what was done, why, specific file paths/commands,
cause-and-effect, decisions made. Minimum 3 phases.

## Key Decisions
- **Decision**: Rationale. Alternatives considered and why rejected.

## What Changed
- `path/to/file` — What changed and why

## Problems & Solutions
- **Problem**: Root cause (not just symptoms)
  **Solution**: Fix and why it works

## Learning Insights
- Reusable knowledge for future sessions

## Related Notes
- `note-id` — "Title" (category)

## Next Steps
- [ ] Concrete actionable follow-up
```

## What NOT to Capture

- Temporary debugging sessions or transient state
- File paths or code snippets without explanatory context
- General programming knowledge available in docs
- Meta-commentary about the conversation itself
- Information that changes frequently without lasting value

## Data & Storage

All data at `~/.claudemem/` as plain Markdown files with YAML frontmatter.
SQLite FTS5 index provides sub-10ms full-text search (rebuildable cache).

**Backup**: `claudemem export backup.tar.gz`
**Restore**: `claudemem import backup.tar.gz` (auto-rebuilds index)
