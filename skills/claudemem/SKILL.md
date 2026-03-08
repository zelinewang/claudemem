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

```bash
# Notes (knowledge fragments — saved automatically during conversation)
claudemem note add <category> --title "..." --content "..." --tags "..." [--session-id "..."]
claudemem note search "query" [--in category] [--tag tags] [--format json]
claudemem note list [category]
claudemem note get <id>
claudemem note append <id> "additional content"
claudemem note update <id> --content "..." [--title "..."] [--tags "..."]
claudemem note delete <id>
claudemem note categories
claudemem note tags

# Sessions (detailed work reports — saved via /wrapup command)
claudemem session save --title "..." --branch "..." --project "..." --session-id "..." [--related-notes "id:title:cat,..."]
claudemem session list [--last N] [--date today] [--date-range 7d] [--branch X]
claudemem session search "query" [--branch X]
claudemem session get <id>

# Unified search (notes + sessions together, with cross-references)
claudemem search "query" [--type note|session] [--limit N] [--format json]
claudemem search "query" --compact [--format json]   # Token-efficient: IDs + titles only

# Context injection (for SessionStart hooks)
claudemem context inject [--limit N] [--project path] [--format json]

# Utilities
claudemem stats                              # Storage statistics
claudemem config set/get/list/delete <key>   # Configuration
claudemem export [output-file]               # Backup as tar.gz
claudemem import <archive-file>              # Restore from backup
claudemem verify                             # Check consistency
claudemem repair                             # Fix orphaned entries
```

Add `--format json` to any command for structured output.

## Hook Configuration

For automatic context injection at session start, add to your Claude Code settings:

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

This automatically loads recent notes and sessions into context when a new conversation begins.

## Autonomous Behavior

### 0. Session Context (At Conversation Start — Automatic via Hook)

If the SessionStart hook is configured, recent context is automatically injected.
If NOT configured, manually run at the start of significant tasks:

```bash
claudemem context inject --limit 5
```

This provides continuity: recent notes, session summaries, and stats.

### 1. Auto-Save Notes (During Conversation — Silent)

Automatically capture knowledge **without asking** during normal conversation. This is your
primary ongoing responsibility — save knowledge AS you discover it, not just at wrap-up.

After saving, show a brief indicator:
```
[noted: "TikTok Rate Limits" -> api-specs]
```

**What to auto-save** (proactive, no user prompt needed):
- API specs, endpoints, rate limits, field mappings, authentication quirks
- Technical decisions with rationale (why X over Y, what alternatives were rejected)
- Bug root causes, symptoms, diagnosis patterns, and fix approaches
- Configuration quirks, gotchas, environment-specific settings
- Architecture patterns discovered or established
- User preferences: coding style, naming conventions, workflow preferences
- Integration quirks: third-party API behaviors, undocumented features, workarounds
- Important commands, URLs, environment configs that someone would need again

**How to auto-save:**
1. Identify the knowledge fragment during your normal response
2. Choose an appropriate category (check existing: `claudemem note categories`)
3. Search to avoid duplicates: `claudemem note search "<key phrase>" --format json`
4. If related note exists: `claudemem note append <id> "new info"`
5. If new: `claudemem note add <category> --title "..." --content "..." --tags "..."`
6. Show indicator: `[noted: "<title>" -> <category>]`

**Do NOT auto-save:**
- Temporary debugging output or transient state
- Bare file paths or code without explanatory context
- General programming knowledge available in docs
- Information the user said is temporary or will change immediately

### 2. Auto-Search Before Tasks (Silent)

At the **start of any significant task**, search memory for relevant prior context:

```bash
# Quick scan first (token-efficient)
claudemem search "<relevant keywords>" --compact --format json --limit 5

# If a result looks relevant, get full content
claudemem note get <id>
```

Search when:
- Starting work on a feature, API, or system previously discussed
- Before making architectural decisions
- When the user references something that might have been captured before
- When working with a codebase or domain you've worked on before

If relevant results found, mention briefly:
```
[memory: Found "TikTok Rate Limits" — rate limit is 100/min per API key]
```

If a note has `session_id` in its metadata, you can look up the full session for more context:
```bash
claudemem session search "<session-id-prefix>" --format json
```

### 3. Session Reports (ONLY via /wrapup — Never Auto)

Session reports are **only** saved when the user explicitly requests via `/wrapup` command
or natural phrases like "wrap up" or "let's wrap up".

**NEVER auto-save sessions** — the user may want to continue the conversation.

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
