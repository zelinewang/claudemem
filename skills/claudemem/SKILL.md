---
name: claudemem
description: >
  Captures knowledge notes and session reports that persist across conversations.
  Provides full-text search with faceted filters, code structure analysis, and
  bidirectional cross-referencing between notes and sessions. Use when saving
  knowledge for future sessions, recalling past work, searching stored context,
  wrapping up a session, or analyzing code structure. All local, zero network.
---

# claudemem — Persistent Memory for AI Agents

Captures and retrieves knowledge across conversations. Two behaviors:
1. **Automatically** save knowledge notes during work (silent, no user action needed)
2. **On command** (`/wrapup`) save a structured session report with cross-linked notes

## Slash Commands

- **/wrapup** [title] — End-of-session: extract knowledge notes + save detailed session report + cross-link everything.
- **/recall** [topic] — Search persistent memory for a topic, or show recent activity.

**Natural triggers:** "remember this", "what do you remember about...", "wrap up", "what did we do last time"

## Setup

If `claudemem` is not on PATH:
```bash
curl -fsSL https://raw.githubusercontent.com/zelinewang/claudemem/main/skills/claudemem/scripts/install.sh | bash
```

## CLI Reference

All commands support `--format json` for structured output.

```bash
# Notes
claudemem note add <category> --title "..." --content "..." --tags "..." [--session-id "..."]
claudemem note search "query" [--in category] [--tag tags]
claudemem note list [category]
claudemem note get <id>                        # Supports 8-char prefix
claudemem note append <id> "additional content"
claudemem note update <id> --content "..." [--title "..."] [--tags "..."]
claudemem note delete <id>
claudemem note categories
claudemem note tags

# Sessions
claudemem session save --title "..." --branch "..." --project "..." --session-id "..." [--related-notes "id:title:cat,..."]
claudemem session list [--last N] [--date today] [--date-range 7d] [--branch X]
claudemem session search "query" [--branch X]
claudemem session get <id>

# Search
claudemem search "query" [--type note|session] [--limit N]
claudemem search "query" --compact              # IDs + titles only
claudemem search "query" --category X --tag Y   # Faceted filters
claudemem search "query" --after 2025-01-01     # Date range
claudemem search "query" --sort date            # Chronological (default: relevance + recency boost)
claudemem search "query" --semantic             # TF-IDF vector similarity (feature-flagged)

# Context
claudemem context inject [--limit N] [--project path]  # Recent notes + sessions overview

# Code
claudemem code outline <file>                   # Structural symbols (Go/Python/TS/Rust)

# Knowledge Graph
claudemem graph                                 # DOT format (pipe to Graphviz)
claudemem graph --format json                   # Adjacency list

# Utilities
claudemem stats [--top-accessed]                # Storage stats + access tracking
claudemem reindex [--vectors] [--all]           # Rebuild search/vector indexes
claudemem config set/get/list/delete | export | import | verify | repair
```

---

## Protocols (Fixed — Always Follow These)

These have predictable inputs and outputs. Follow them exactly every time.

### Protocol 1: Note Saving

Every time you save a note, follow this sequence:
1. Search for duplicates: `note search "<key phrase>" --format json`
2. If topic exists: `note append <id> "new info"`
3. If new: `note add <category> --title "..." --content "..." --tags "..."`
4. Check existing categories first: `note categories`
5. Show indicator: `[noted: "title" -> category]`

### Protocol 2: Session Reports (`/wrapup`)

Session reports are ONLY created via explicit `/wrapup` or "wrap up" — never automatically.
Every session report uses this exact template:

```markdown
## Summary
2-3 substantial paragraphs: goal, accomplished, current state, significance.

## What Happened
Numbered paragraphs. Each phase: what, why, file paths, cause-and-effect, decisions. Minimum 3 phases.

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

### Protocol 3: Cross-Referencing

Notes and sessions MUST be bidirectionally linked during `/wrapup`:
- Notes saved during wrapup: include `--session-id "$SESSION_REF"` to link note → session
- Session save: include `--related-notes "id:title:category,..."` to link session → notes

This enables tracing: from any note → which session produced it; from any session → what knowledge was extracted.

### Protocol 4: Retrieval Indicators

When prior knowledge is found and used, always show:
```
[memory: Found "TikTok Rate Limits" — 100/min per API key]
```

---

## Capabilities (Flexible — Agent Decides When/How)

These vary by context. Use the trade-offs below to make your own judgment call.

### Search Modes

| Approach | When useful | Token cost |
|----------|------------|------------|
| `search "X" --compact --format json` | Quick scan: "do I know anything about this?" | ~100 tokens |
| `search "X" --format json` | Need full context with previews | ~2000 tokens |
| `search "X" --category Y --tag Z` | Know the domain, want precise results | varies |
| `search "X" --semantic` | Find conceptually related content (no exact keyword match needed) | ~100-2000 |
| `note get <id>` | Need complete content of one specific note | ~500 tokens |
| `context inject` | Session start: recent knowledge overview | ~1-2KB |
| `stats --top-accessed` | See which notes are most frequently used (ROI tracking) | ~200 tokens |
| `graph --format json` | Understand note ↔ session relationship structure | ~500-2000 |

Default sort uses relevance with recency boost (entries <7 days get up to 20% score boost, decaying over 30 days). Use `--sort date` for chronological ordering.

**Semantic search** (`--semantic`): Uses TF-IDF vectors + cosine similarity combined with FTS5 via Reciprocal Rank Fusion. Finds conceptually related content even without exact keyword matches. Requires feature flag: `config set features.semantic_search true` + `reindex --vectors`.

### Code Intelligence

`code outline <file>` extracts structural symbols — function signatures, classes, types — without bodies. ~10-20 tokens per symbol vs ~500+ for full file reads.

Supports Go (~95% accuracy), Python (~90%), TypeScript/JS (~80%), Rust (~85%). Unsupported languages return empty result.

### Context Injection

`context inject` returns recent notes + sessions + stats (~1-2KB). Can be configured as a SessionStart hook for automatic loading, or run manually when starting significant work.

```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "",
      "hooks": [{ "type": "command", "command": "claudemem context inject --limit 5", "timeout": 10000 }]
    }]
  }
}
```

---

## Domain Knowledge (Reference — Informs Decisions)

### What's Worth Saving

**High-value** (things Claude won't know in a future session):
- Project-specific decisions with rationale (why X over Y, rejected alternatives)
- Bug root causes and diagnosis patterns specific to this codebase
- Configuration quirks, environment-specific gotchas, undocumented API behaviors
- User preferences discovered during work (naming conventions, workflow choices)

**Low-value** (skip — Claude already knows or doesn't need):
- Transient debugging state, temporary output
- General programming knowledge from public documentation
- Information the user explicitly says is ephemeral

### Data Model

- **Notes**: Knowledge fragments with category, tags, content, metadata. Source of truth: `~/.claudemem/notes/<category>/<title>.md`
- **Sessions**: Work reports with structured sections. Source of truth: `~/.claudemem/sessions/<title>.md`
- **Cross-refs**: Note.metadata.session_id → Session; Session.RelatedNotes[] → Notes
- **Index**: SQLite FTS5 at `~/.claudemem/.index/search.db` (regenerable cache, not source of truth)

All data is plain Markdown with YAML frontmatter — human-readable, git-friendly, portable.
