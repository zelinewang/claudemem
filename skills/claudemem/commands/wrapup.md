---
description: Complete session wrap-up — detailed work report + knowledge extraction with cross-linking
argument-hint: Optional session title
---

# Session Wrap-Up Protocol

You are wrapping up this conversation. This is your **MOST IMPORTANT** final task — produce a
thorough work report that captures everything meaningful from this session. Your future self
(or a teammate) will rely on this to understand exactly what happened, why, and what was learned.

**Superficial wrap-ups are NOT acceptable.** A good session report reads like a detailed work journal
entry — not a bullet summary.

## Phase 1: Generate Session Reference ID

Create a unique session ID for cross-referencing notes and sessions:

```bash
SESSION_REF="$(date +%Y%m%d-%H%M%S)-$(head -c 4 /dev/urandom | xxd -p)"
echo "Session ref: $SESSION_REF"
```

You will use this `$SESSION_REF` to link notes to this session (Phase 2) and tag the session itself (Phase 3).

## Phase 2: Extract & Save Knowledge Notes

Review the **ENTIRE** conversation from start to finish. Identify knowledge fragments that should
persist beyond this session — things you or a future agent would want to know.

**For each knowledge fragment:**

1. **Search first** — avoid duplicates:
   ```bash
   claudemem note search "<key phrase>" --format json
   ```
2. **If related note exists** — append new info:
   ```bash
   claudemem note append <id> "new detail learned in this session"
   ```
3. **If genuinely new** — create with session link:
   ```bash
   claudemem note add <category> --title "..." --content "..." --tags "..." --session-id "$SESSION_REF"
   ```

**Keep a running list** of every note ID you save — you need these for Phase 3.

### What to extract as notes:

- **API specs**: endpoints, rate limits, field mappings, authentication quirks
- **Technical decisions**: what was decided, why, what alternatives were rejected
- **Bug patterns**: root causes, symptoms, how to diagnose, how to fix
- **Configuration**: environment-specific settings, gotchas, defaults that surprised you
- **Architecture patterns**: patterns discovered or established during the session
- **User preferences**: coding style, naming conventions, workflow preferences
- **Integration quirks**: third-party API behaviors, undocumented features, workarounds

### What NOT to extract:

- Temporary debugging output or transient state
- Generic programming knowledge available in docs
- Information the user explicitly said was temporary
- Bare file paths or code without explanatory context

## Phase 3: Generate & Save Detailed Session Report

Now write the session report. This is a **work journal entry**, not a tweet.

### Required Sections and Quality Standards

#### `## Summary` — MINIMUM 2-3 substantial paragraphs

Must answer ALL of these:
- What was the **goal** or trigger for this session? (bug report? feature request? investigation?)
- What was **accomplished**? Be specific — not "fixed a bug" but "fixed the mention-gating bug in Vio Gateway"
- What is the **current state**? Is it deployed? Pending review? Partially complete?
- What's the **significance**? Why does this matter for the project?

#### `## What Happened` — MINIMUM 3 numbered phases

Write numbered paragraphs (NOT bullet points). Each phase covers one logical step of work:

```
1. **[Phase title]** — [What was done, specifically]. [Why it was done — the trigger or reason].
   [Specific details: file paths, line numbers, commands run, error messages seen].
   [What was discovered or decided at this point]. [What this led to next].

2. **[Phase title]** — ...
```

**Requirements per phase:**
- At least one specific file path, command, or concrete reference
- Cause-and-effect: "X happened because Y, which led us to Z"
- If a decision was made, mention what alternatives existed

#### `## Key Decisions` — Every decision with rationale

```
- **[What was decided]**: [Why this approach was chosen]. Alternatives considered: [list]. Rejected because: [reasons].
```

Skip this section only if truly no decisions were made (rare).

#### `## What Changed` — Every file modified

```
- `path/to/file.py` — [What changed and why]
```

Include ALL files touched, even config changes. Skip only if no code was written.

#### `## Problems & Solutions` — Root cause analysis

```
- **Problem**: [Description of what went wrong — symptoms AND root cause]
  **Solution**: [What was done to fix it, and why this solution works]
```

Do NOT write "Fixed X." Instead: describe the root cause, the investigation path, and why the fix addresses the root cause. Skip only if no problems were encountered.

#### `## Learning Insights` — Reusable knowledge

What did you learn that could help in future sessions? Think: "If I encountered a similar situation next week, what would I want to know?"

```
- [Insight with enough context to be useful standalone]
```

#### `## Related Notes` — Cross-references to Phase 2

List every note saved or updated in Phase 2:

```
- `<note-id-prefix>` — "<Note Title>" (<category>)
```

This creates bidirectional linking: the note has `session_id` metadata pointing here, and this session lists the notes.

#### `## Next Steps` — Actionable follow-ups

```
- [ ] [Concrete next action]
```

### Save the session:

```bash
printf '%s' '<full markdown content with all sections above>' | claudemem session save \
  --title "<descriptive title summarizing the session>" \
  --branch "$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo 'unknown')" \
  --project "$(basename "$(pwd)")" \
  --session-id "$SESSION_REF" \
  --tags "<comma,separated,relevant,tags>" \
  --related-notes "<note-id-1:title1:category1>,<note-id-2:title2:category2>"
```

## Phase 4: Show Wrap-Up Report

Display what was captured:

```
===================================
  Session Wrapped Up!
===================================

  Notes:
    Saved: "<title>" -> <category> [linked]
    Updated: "<title>" (appended new info)
    Skipped: "<title>" (already stored)

  Session Report:
    "<session title>" (<branch>, <date>)
    Quality: <N> paragraphs summary, <N> phases, <N> decisions, <N> insights

  Cross-references: <N> notes <-> 1 session linked

  Memory: <N> notes, <N> sessions total
===================================
```

Run `claudemem stats` to get the totals.

## Quality Self-Check

Before saving, verify EVERY item. If any check fails, go back and fix it:

- [ ] Summary has at least 2 full paragraphs with specific details?
- [ ] "What Happened" has at least 3 numbered phases with file paths or commands?
- [ ] Every decision includes WHY and what alternatives existed?
- [ ] Problems describe ROOT CAUSE, not just symptoms?
- [ ] Insights are genuinely useful for a future session (not generic advice)?
- [ ] All notes from Phase 2 are listed in Related Notes?
- [ ] Session title is descriptive (not "Session Summary" or "Today's Work")?

## Examples

### BAD — Will be rejected by future you:

> **Summary**: Fixed a bug in the gateway. Made some changes.
>
> **What Happened**: 1. Found bug. 2. Fixed it.

This is useless. You can't tell what bug, where, why, or how.

### GOOD — Valuable work journal entry:

> **Summary**: Investigated and fixed a critical mention-gating bug in Vio Gateway
> (`project_amy/vio/gateway/index.js`) where the bot was responding to ALL messages in Lark
> group chats instead of only @mentioned messages. The root cause was that the original gateway
> implementation assumed 1:1 chat topology — group chat filtering was never implemented.
>
> The fix was committed as `4a7ea06a` and pushed to master. It uses the same Lark bot API
> (`GET /open-apis/bot/v3/info`) that Amy/Eva's proven claw-lark extension uses, with graceful
> fallback if the API is temporarily unavailable. User explicitly chose gateway-level filtering
> (over agent-only) for defense-in-depth in the multi-tenant environment.
>
> **What Happened**:
> 1. **Diagnosed the problem** — User reported Vio responding to every message in a Lark group chat
>    (link_token=6d5g2950). Read `gateway/index.js:380-420` and found `handleLarkEvent()` had no
>    mention-check. The event handler processed every `im.message.receive_v1` event regardless of
>    whether the bot was mentioned, because the original code assumed P2P chats only.
>
> 2. **Designed the fix** — Referenced Amy/Eva's implementation in
>    `shared/extensions/claw-lark/dist/src/monitor.js:153-170` which uses `getBotOpenId()` with
>    caching. Presented 3 options to user via AskUserQuestion: gateway filter (recommended),
>    agent-only, per-route configurable. User chose gateway-level for defense-in-depth.
>
> 3. **Implemented and verified** — Added `fetchBotOpenId()` with response caching, `isMentioned()`
>    check in the event handler, and graceful fallback (if API fails, allows message through rather
>    than dropping it). Tested in the reported group chat — bot now only responds to @mentions.

