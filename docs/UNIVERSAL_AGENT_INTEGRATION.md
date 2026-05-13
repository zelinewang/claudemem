# Universal Agent Integration

claudemem must stay useful outside any single coding agent. Claude Code and
Codex are the first-class targets today, but the integration boundary should
work for any agent that can run a CLI command with a JSON payload.

## Positioning

claudemem is not an auto-loaded personality file and it is not a log sink. It is
a durable, searchable, syncable knowledge base for coding agents.

The universal design has three layers:

1. **Core CLI and store**: notes, sessions, search, vectors, sync, repair,
   health, and future candidate queues. This layer must not know about one
   agent's hook schema.
2. **Normalized event protocol**: a small JSON contract that represents agent
   lifecycle events such as session start, user prompt, tool use, stop, and
   session end.
3. **Agent adapters**: thin scripts or config snippets that translate native
   hook payloads from Claude Code, Codex, or another agent into the normalized
   event protocol.

This keeps claudemem portable while still giving each agent a native-feeling
integration.

## First-Class Agents

| Agent | Current role | Integration priority |
| --- | --- | --- |
| Claude Code | Mature hook surface and slash-command workflow | P0 |
| Codex | Current working environment and config-driven memory doctrine | P0 |
| Other coding agents | Future adapters if they can invoke a CLI with JSON | P2 |

P0 means every new hook/capture/wrapup capability must be reviewed for both
Claude Code and Codex before it is considered complete.

## Invariants

- **Agent-neutral core**: no core storage or classification code should depend
  on Claude Code or Codex-specific field names.
- **Adapters are thin**: adapter scripts may rename fields, set agent metadata,
  and normalize timestamps, but should not implement memory policy.
- **Hooks are control-plane only**: hooks may inject context, report health,
  sync, create capture candidates, or remind the agent to wrap up. Hooks should
  not dump transcripts or save every tool output.
- **Candidate-first capture**: automatic capture must follow
  `detect -> classify -> redact -> dedupe/merge -> save/skip`.
- **Session reports remain model-active**: high-quality sessions need `/wrapup`
  or an equivalent model-active workflow. Shell-only session-end hooks may only
  remind or create a low-fidelity candidate, not default saved sessions.
- **Nonblocking by default**: startup and shutdown hooks must degrade silently or
  print one-line status. They must not block agent work when claudemem is
  unavailable.
- **Zero-cost default**: local FTS and TF-IDF-compatible behavior must work
  without paid APIs. Cloud embeddings remain opt-in.

## Normalized Event Shape

Future hook support should target a stable event command:

```bash
claudemem hook event --dry-run --payload -
```

The payload should be agent-neutral:

```json
{
  "schema_version": "claudemem.hook_event.v1",
  "agent": {
    "name": "codex",
    "version": "unknown",
    "surface": "desktop"
  },
  "event": {
    "type": "post_tool_use",
    "native_type": "PostToolUse",
    "timestamp": "2026-05-13T22:00:00Z"
  },
  "session": {
    "id": "optional-agent-session-id",
    "cwd": "/path/to/workspace",
    "project": "optional-project-name",
    "branch": "optional-git-branch"
  },
  "tool": {
    "name": "Bash",
    "input_summary": "optional redacted summary",
    "output_summary": "optional redacted summary",
    "exit_code": 0
  },
  "text": {
    "user_prompt": "",
    "assistant_summary": ""
  },
  "limits": {
    "max_payload_bytes": 32768,
    "allow_network": false
  }
}
```

Native adapters can pass through an additional `raw` object for debugging, but
the core classifier should make decisions from normalized fields.

## Expected Output

Dry-run mode should return a decision without mutating memory:

```json
{
  "action": "candidate",
  "should_save": false,
  "signal": "low",
  "reason": "transient tool output",
  "proposed_note": null
}
```

For high-signal events:

```json
{
  "action": "candidate",
  "should_save": true,
  "signal": "high",
  "reason": "verified release fact",
  "proposed_note": {
    "category": "project",
    "title": "claudemem v3.0.11 release",
    "actionable_summary": "Future agents should treat v3.0.11 as the first release with hook-safe health and free PR CI checks.",
    "tags": ["claudemem", "release", "ci"]
  }
}
```

Saving should be a second step:

```bash
claudemem candidates accept <id>
```

or, for explicitly allowed classes, an opt-in auto-save mode:

```bash
claudemem hook event --autosave=explicit-user-memory,verified-release --payload -
```

## Adapter Responsibilities

### Claude Code Adapter

Claude Code can provide lifecycle hooks such as `SessionStart`,
`UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, and `SessionEnd`.

Recommended mapping:

| Native event | claudemem behavior |
| --- | --- |
| `SessionStart` | context inject, health traffic light, optional sync pull |
| `UserPromptSubmit` | candidate if the user explicitly asks to remember something |
| `PostToolUse` | candidate only for high-signal, bounded outputs |
| `Stop` | remind if wrapup appears missing; do not save a session |
| `SessionEnd` | optional sync push; no default transcript/session dump |

### Codex Adapter

Codex should receive the same memory semantics as Claude Code, but through its
own config and hook files.

Recommended mapping:

| Native event | claudemem behavior |
| --- | --- |
| `SessionStart` | context inject, health traffic light, optional sync pull |
| `PreToolUse` | no memory writes; safety checks only |
| `PostToolUse` | candidate only, dry-run by default |
| `Stop` | wrapup reminder or missing-report warning |

Codex instructions must also state the agent behavior directly in `AGENTS.md`:
search at task start, save useful discoveries silently, and wrap up before
ending substantial sessions. Hook support is a safety net, not a replacement
for model-active judgment.

## What To Borrow From Other Memory Systems

Borrow:

- lifecycle trigger points
- automatic health/context/sync checks
- compact context injection
- progressive disclosure
- candidate capture and review queues

Do not borrow:

- saving every tool output
- transcript dumps
- opaque storage
- required background workers
- agent-specific storage paths in the core design

## Implementation Roadmap

1. Add `claudemem hook event --dry-run --payload -`.
2. Add a deterministic classifier that produces save/skip decisions without
   network calls.
3. Add `candidates list/get/accept/reject/stats`.
4. Add Claude Code and Codex adapter snippets that call the same hook command.
5. Add actionable summaries to new notes and sessions.
6. Only after reviewing candidate quality, enable narrow opt-in autosave for
   explicit user memory requests and verified release/merge/deploy facts.

The first successful version is not the one that saves the most. It is the one
that makes high-quality memory capture easier without polluting the long-term
store.
