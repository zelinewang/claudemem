# Claude Code Hook Integration

claudemem ships with suggested additions to Claude Code's session hooks
so memory health is checked at session start and memory sync happens at
session end. These are suggestions — apply them to your own hook files
manually.

For the user of this fork, the hooks live in:
- `~/claude-code-config/global/hooks/session-memory.sh` (synced)
- `~/claude-code-config/global/hooks/session-end-sync.sh` (synced)

After editing, run `/sync push` to propagate to other machines.

## session-memory.sh (SessionStart)

Append after the existing `claudemem context inject` call:

```bash
# --- claudemem health parity check (P5) ---
# Runs in <100ms on a healthy system. Prints a one-line warning if drift
# is detected; does NOT block shell startup.
if command -v claudemem >/dev/null 2>&1; then
  claudemem health --quick 2>&1 | head -4
fi

# --- claudemem auto-pull (P6, opt-in) ---
# Create ~/.claudemem/.sync_auto_pull to enable cross-machine memory sync
# at SessionStart. Off by default to avoid surprising network calls.
if [ -f "$HOME/.claudemem/.sync_auto_pull" ] && command -v claudemem >/dev/null 2>&1; then
  claudemem sync pull --quiet 2>/dev/null || true
fi
```

## session-end-sync.sh (SessionEnd)

Append AFTER the existing rsync backup:

```bash
# --- claudemem auto-push (P6, opt-in) ---
# Create ~/.claudemem/.sync_auto_push to enable cross-machine memory sync
# at SessionEnd. Pushes committed notes/sessions to the claudemem-memory
# remote (configured via `claudemem sync init`).
if [ -f "$HOME/.claudemem/.sync_auto_push" ] && command -v claudemem >/dev/null 2>&1; then
  claudemem sync push --quiet 2>/dev/null || true
fi
```

## Enabling auto-sync on a machine

```bash
# Set up the remote first (HTTPS recommended — no SSH key needed)
claudemem sync init https://github.com/YOU/claudemem-memory.git

# Enable auto-pull / auto-push
touch ~/.claudemem/.sync_auto_pull
touch ~/.claudemem/.sync_auto_push

# Verify
ls -la ~/.claudemem/.sync_auto_*
```

The two flag files live OUTSIDE config.json so they can be toggled
per-machine without affecting the shared config. A CI server can run
with auto-pull on + auto-push off, for example.
