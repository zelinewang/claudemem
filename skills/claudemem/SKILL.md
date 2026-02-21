---
name: claudemem
description: >
  Persistent memory that survives across conversations. Automatically remembers important context
  (API specs, decisions, quirks, preferences) and saves session summaries. Searches past knowledge
  before starting new tasks. Responds naturally to phrases like "remember this", "what do you know
  about...", "save this session", or "what did we do last time". All local, zero network.
---

# claudemem — Your Persistent Memory

Memory that carries across conversations. Automatically captures important knowledge during work
and saves structured session summaries when you're done. Searches past context before new tasks.

## Trigger Phrases

These are natural phrases that activate memory operations:

**To save knowledge:**
- **"remember this"** — store the current information for future reference
- **"save this"** or **"note this down"** — same as above
- **"keep this in mind"** — store for later retrieval

**To search memory:**
- **"what do you remember about..."** — search for relevant past context
- **"do you recall..."** or **"check your notes on..."** — search memory
- **"what do we know about..."** — search before starting work
- **"look up..."** — quick memory search

**To save a session summary:**
- **"save this session"** — generate and save a structured summary of this conversation
- **"wrap up"** or **"let's save our progress"** — same as above
- **"summarize what we did"** — save session with summary

**To recall past sessions:**
- **"what did we do last time"** — search past session summaries
- **"show me recent sessions"** — list recent work
- **"what happened with [topic]"** — search sessions by topic

## Setup

Before first use, verify the CLI is installed. If `claudemem` is not found on PATH, install it:

```bash
curl -fsSL https://raw.githubusercontent.com/zelinewang/claudemem/main/skills/claudemem/scripts/install.sh | bash
```

Or run the bundled installer:

```bash
bash "SKILL_DIR/scripts/install.sh"
```

After installation, verify with `claudemem --version`.

## Commands

```bash
# Notes (knowledge fragments)
claudemem note add <category> --title "..." --content "..." --tags "tag1,tag2"
claudemem note search "query" [--in category] [--tag tags]
claudemem note list [category]
claudemem note get <id>
claudemem note update <id> --content "..." [--title "..."] [--tags "..."]
claudemem note append <id> "additional content"
claudemem note delete <id>
claudemem note categories
claudemem note tags

# Sessions (conversation summaries)
claudemem session save --title "..." --branch "..." --project "..." --session-id "..." --content "..."
claudemem session list [--last N] [--date today] [--date-range 7d] [--branch X]
claudemem session search "query" [--branch X]
claudemem session get <id>

# Unified search (across notes AND sessions)
claudemem search "query" [--type note|session] [--limit N]

# Statistics
claudemem stats

# Configuration
claudemem config set/get/list/delete <key> [value]

# Data portability
claudemem export [output-file]              # Backup as tar.gz
claudemem import <archive-file>             # Restore from backup (auto-reindexes)

# Data integrity
claudemem verify                            # Check DB-file consistency
claudemem repair                            # Fix orphaned entries
```

Add `--format json` to any command for structured output.

## Autonomous Behavior

### When to Remember (Proactive — No User Prompt Needed)

Automatically capture information that would be lost when the conversation ends:

* Business requirements, use cases, user stories
* API specifications, field mappings, data transformations, rate limits
* System constraints, assumptions, edge cases, exclusions
* Integration-specific behavior, quirks, gotchas
* Domain terminology, aliases, abbreviations
* Technical decisions with rationale (why X over Y)
* Known issues, limitations, workarounds
* Configuration requirements, thresholds, defaults
* User preferences and project conventions
* Resolved bugs and their root causes
* Useful URLs, endpoints, environment configs

### When to Search Memory (Proactive — No User Prompt Needed)

Automatically search memory at the start of tasks that might benefit from prior context:

* Before implementing a feature in a domain previously discussed
* When the user references something from a past conversation
* When working with an API, integration, or system previously documented
* Before making architectural decisions that may have prior rationale recorded

### When to Save Sessions (Proactive or On Request)

Save a session summary when:

* A significant piece of work is completed
* The conversation is ending and meaningful work was done
* Before context would be lost between sessions
* The user says "save this session", "wrap up", or "summarize what we did"

### Workflow Rules

1. **Before saving**: search existing content first — update or append if related note exists
2. **Before working**: search for relevant context that may inform the current task
3. **Merge related information** under existing categories/titles when possible
4. **Preserve existing content** unless contradicted by new information
5. **Focus on evergreen knowledge**, not transient conversation artifacts

## Session Summary Template

When saving a session, generate content following this structure:

```markdown
## Summary
One or two paragraphs describing what was accomplished.

## Key Decisions
- Decision 1 with rationale
- Decision 2 with rationale

## What Changed
- `path/to/file.py` — Description of change

## Problems & Solutions
- **Problem**: Description of issue
  **Solution**: How it was resolved

## Questions Raised
- Open question needing future attention

## Next Steps
- [ ] First follow-up task
```

## What NOT to Capture

* Temporary debugging sessions or transient state
* File paths or code snippets without context
* General programming knowledge available in docs
* Meta-commentary about the conversation itself
* Information that changes frequently without lasting value

## Data Portability

All data stored at `~/.claudemem/` as plain Markdown files with YAML frontmatter.
SQLite FTS5 index is a rebuildable cache — only the Markdown files matter.

**Backup**: `claudemem export backup.tar.gz`
**Restore**: `claudemem import backup.tar.gz` (auto-rebuilds search index)

## Storage

`~/.claudemem/` — Plain text Markdown files organized by type (notes/ and sessions/).
FTS5 SQLite index for sub-10ms full-text search. File permissions: 0600/0700.
