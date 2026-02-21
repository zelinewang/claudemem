---
name: claudemem
description: >
  Unified persistent memory for AI agents combining knowledge notes and session summaries.
  Auto-captures API specs, technical decisions, integration quirks, and domain knowledge during
  conversations. Auto-saves structured session summaries at conversation end. Auto-searches past
  context before new tasks. All data stored locally with FTS5 search. Zero network calls by default.
  Triggered manually by "braindump this" (to store), "use your brain" (to retrieve), /done (save session),
  or /recall (search sessions).
---

# claudemem — Unified Agent Memory

Local, searchable memory that persists across conversations. Combines knowledge fragment capture
(like braindump) with structured session summaries (like claude-done) in a single Go binary with
FTS5 full-text search.

Manual triggers:
- **"braindump this"** or **"save this to memory"** — store a knowledge fragment
- **"use your brain"** or **"remember anything about..."** — search memory before performing a task
- **/done** — save a structured session summary
- **/recall** or **"recall"** — search past session summaries

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
claudemem config set <key> <value>
claudemem config get <key>
claudemem config list
claudemem config delete <key>

# Data portability
claudemem export [output-file]              # Backup as tar.gz
claudemem import <archive-file>             # Restore from backup (auto-reindexes)

# Migration from other tools
claudemem migrate braindump [--source path] # Import from ~/.braindump/
claudemem migrate claude-done [--source path] # Import from ~/.claude-done/

# Data integrity
claudemem verify                            # Check DB-file consistency
claudemem repair                            # Fix orphaned entries
```

Add `--format json` to any command for structured output.

## Autonomous Behavior

### When to Store Notes (Proactive, Silent)

Capture information that would be lost when the conversation ends:

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

### When to Retrieve (Proactive, Silent)

Search memory at the start of tasks that might benefit from prior context:

* Before implementing a feature in a domain previously discussed
* When the user references something from a past conversation
* When working with an API, integration, or system previously documented
* Before making architectural decisions that may have prior rationale recorded

### When to Save Sessions (/done)

Save a session summary when:

* A significant piece of work is completed
* The conversation is ending and meaningful work was done
* Before context would be lost between sessions
* The user explicitly says /done or "save this session"

### Workflow Rules

1. **Before storing**: search existing content first — update or append if found, add new note if not
2. **Before working**: search for relevant context that may inform the current task
3. **Merge related information** under existing categories/titles when possible
4. **Preserve existing content** unless contradicted by new information
5. **Focus on evergreen knowledge**, not conversation artifacts

## Session Summary Template

When saving a session with /done, generate content following this structure:

```markdown
## Summary
One or two paragraphs describing what was accomplished.

## Key Decisions
- Decision 1 with rationale
- Decision 2 with rationale

## What Changed
- `path/to/file.py` — Description of change
- `path/to/other.ts` — Description of change

## Problems & Solutions
- **Problem**: Description of issue
  **Solution**: How it was resolved

## Questions Raised
- Open question needing future attention

## Next Steps
- [ ] First follow-up task
- [ ] Second follow-up task
```

## Categories and Structure

**Categories** represent cohesive domain areas: an integration, a system capability, a distinct module.
Choose categories intuitively — use existing ones when appropriate, create new ones when needed.

**Titles** should be searchable keywords that narrow context effectively.

**Content** should be concise, fact-dense. Use bullet points for lists.

## What NOT to Capture

* Temporary debugging sessions or transient state
* File paths or code snippets without context
* General programming knowledge available in docs
* Meta-commentary about the conversation itself
* Information that changes frequently without lasting value

## Data Portability

All data is stored at `~/.claudemem/` as plain Markdown files with YAML frontmatter.
The SQLite FTS5 index is a secondary cache that can be rebuilt from the Markdown files.

**Backup**: `tar czf claudemem-backup.tar.gz ~/.claudemem/`
**Restore**: Extract on any machine where claudemem is installed.
**Migrate**: `claudemem migrate braindump` or `claudemem migrate claude-done` to import existing data.

## Storage

`~/.claudemem/` — Plain text Markdown files organized by type. An FTS5 SQLite index powers
fast search across both notes and sessions. File permissions: 0600 (files), 0700 (dirs).
