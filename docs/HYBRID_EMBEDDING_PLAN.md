# Hybrid Embedding Backend + Cross-Machine Sync — Implementation Plan v2

**Status**: APPROVED DESIGN (2026-04-14) — supersedes v1 (see git history at `cfe05bd`)
**Owner**: Zane
**Effort**: 1.5–2 days focused work (all phases)
**Branch**: `feat/embedder-interface` (worktree at `/tmp/claudemem-refactor`)

## Why this version supersedes v1

The v1 plan (same filename, now in git history) addressed backend pluggability only. It had five gaps that came out of a second-round review:

1. **Outdated cloud ranking** — Voyage-3-large was #1; Gemini-embedding-001 now leads verified public MTEB (68.32). Voyage-3.5-lite is the new $0.02/M value pick.
2. **Silent TF-IDF fallback preserved** — incompatible with the design rule below.
3. **Cross-machine sync omitted** — notes/sessions are machine-siloed today; v1 didn't address it.
4. **Per-document embedding metadata missing** — without it, backend switch = full reindex AND mixed-backend machines can't coexist.
5. **Existing shipped work understated** — v1 described min-max hybrid fusion and backend consistency checks as "Phase C" items; both already shipped in commits `2a52f34` and `66c3fdc`. Real Phase C scope is smaller than v1 implied.

## Design Rules (non-negotiable)

1. **No silent fallback.** User explicitly picks a backend via `claudemem setup` (wizard) or `claudemem config set embedding.backend <name>` (manual). If the configured backend is unreachable, we fail loud with recovery instructions — never degrade to TF-IDF behind the user's back.
2. **Interactive recovery when stdin is a TTY.** On backend failure during a search, offer: (a) retry, (b) fall back to FTS-only for THIS query, (c) run `claudemem setup`. Non-TTY shells get (a) = error exit only.
3. **Per-doc embedding metadata.** Every vector row carries `(doc_id, backend, model, dim)`. Enables mixed-backend cross-machine sync, incremental re-embedding on backend switch, and clean search filtering.
4. **Markdown is source of truth.** Cross-machine sync ships markdown only. SQLite FTS + vector indices are rebuildable from markdown, so they're never in git.
5. **TF-IDF is a first-class choice, not a fallback.** Useful for airgapped / CI / zero-config users. Selectable in the wizard. Never auto-selected.

## Research Update — April 2026 (verified)

**Local (Ollama)**:
| Rank | Model | Dim (native → recommended) | Chinese | Notes |
|---|---|---|---|---|
| **1** | `qwen3-embedding:4b` | 2560 → **768** | ✅ CN+EN primary training | MTEB multilingual top-tier. Matryoshka from 32 up. 2.5GB model |
| 2 | `qwen3-embedding:0.6b` | 1024 → 512 | ✅ | Lighter option if 4B is slow |
| 3 | `bge-m3` | 1024 | ✅ 100+ langs | Battle-tested fallback, dense+sparse+multivec |

**Cloud**:
| Rank | Model | Price /1M | Dim (matryoshka) | Chinese | Third-party MTEB |
|---|---|---|---|---|---|
| **1 Quality** | `gemini-embedding-001` | $0.15 ($0.075 batch) | 3072 → 768/1024 | ✅ 100+ langs | **68.32 verified** |
| 2 Value | `voyage-3.5-lite` | $0.02 (200M free) | 1024 | ✅ 26 langs | Voyage self-report only |
| 3 Budget | `text-embedding-3-small` | $0.02 | 512 → 1536 | ⚠ English-heavy | 62.3 |

Dropped from rankings: nomic-embed-text (weak Chinese), voyage-3-large (superseded by 3.5-lite at same/lower cost), text-embedding-3-large (outclassed on multilingual).

For a 3K-note bilingual corpus, Gemini-001 at 768-dim matryoshka runs ~$0.50/month. Local qwen3-4b at 768-dim uses ~6MB of vectors total. Both are practical.

## Architecture

```
pkg/vectors/
├── embedder.go         NEW: Embedder interface, InputType enum, common errors
├── ollama.go           REFACTORED: implement Embedder; add Name()/Dimensions()
├── gemini.go           NEW: Google Gemini Embedding API client
├── voyage.go           NEW: Voyage AI client with document/query modes
├── openai.go           NEW: OpenAI embeddings
├── tfidf.go             KEPT: now implements Embedder (TF-IDFEmbedder)
├── store.go            REFACTORED: Embedder-based, no type switches
├── registry.go         NEW: factory — name → Embedder constructor
└── health.go           NEW: invariant checks (I1–I5)

pkg/config/
├── config.go            EXTENDED: typed EmbedderConfig struct + secret-guard
└── wizard.go           NEW: interactive setup (claudemem setup command)

cmd/
├── setup.go            NEW: wraps wizard.go for CLI surface
├── health.go           NEW: claudemem health / verify --deep
└── sync.go             NEW: claudemem sync {pull, push, status, init}

pkg/sync/
├── gitsync.go          NEW: markdown-only git sync orchestration
└── reconcile.go        NEW: post-pull reconciliation (rebuild index, re-embed missing)
```

### Embedder interface

```go
// InputType tells multi-mode backends which optimization to apply.
// Ollama/TF-IDF ignore it. Voyage/Gemini use it.
type InputType string
const (
    InputTypeDocument InputType = "document"
    InputTypeQuery    InputType = "query"
)

// Embedder produces dense vector embeddings.
type Embedder interface {
    // Available checks if the backend is reachable NOW. Not cached — every call.
    Available() error             // returns nil if healthy, error with recovery hint otherwise
    Embed(text string, t InputType) ([]float32, error)
    EmbedBatch(texts []string, t InputType) ([][]float32, error)
    Name() string                 // "ollama" | "gemini" | "voyage" | "openai" | "tfidf"
    Model() string                // "qwen3-embedding:4b" | "gemini-embedding-001" | ...
    Dimensions() int              // after any matryoshka truncation
}
```

Key design choice: `Available()` returns an **error with a recovery hint**, not a bool. This enables the fail-loud rule — the error message tells the user exactly what to do (`ollama serve`, `export GEMINI_API_KEY=...`, `claudemem setup`).

### Vectors table schema (migration v22)

```sql
-- OLD: CREATE TABLE vectors (id TEXT PRIMARY KEY, vector BLOB)

-- NEW:
CREATE TABLE vectors (
    doc_id TEXT NOT NULL,
    backend TEXT NOT NULL,       -- "ollama" | "gemini" | "voyage" | "openai" | "tfidf"
    model TEXT NOT NULL,          -- "qwen3-embedding:4b" | "gemini-embedding-001" | ...
    dim INTEGER NOT NULL,
    vector BLOB NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (doc_id, backend, model)
);
CREATE INDEX idx_vectors_backend ON vectors(backend, model);
CREATE INDEX idx_vectors_doc ON vectors(doc_id);
```

Search becomes: `SELECT vector FROM vectors WHERE doc_id IN (...) AND backend = ? AND model = ?`.

**Migration strategy (preserve existing vectors, don't drop)**:

On first run with new binary, detect old-schema `vectors` table via `PRAGMA table_info(vectors)`:

```go
// Pseudocode for pkg/storage/migrations/v22.go
func MigrateV22(db *sql.DB) error {
    if !hasOldVectorsSchema(db) { return nil }   // already migrated

    // 1. Read what backend produced the existing rows
    var indexedBackend string
    db.QueryRow("SELECT value FROM vector_meta WHERE key='index_backend'").Scan(&indexedBackend)
    // e.g. "ollama:nomic-embed-text" or "tfidf"
    backend, model := parseBackendTuple(indexedBackend)
    dim := readDimensionFromFirstRow(db)          // inspect blob length / 4

    // 2. Rename old table
    db.Exec("ALTER TABLE vectors RENAME TO vectors_v21")

    // 3. Create new schema
    db.Exec(newV22Schema)

    // 4. Copy every old row with backend/model/dim stamped
    db.Exec(`INSERT INTO vectors (doc_id, backend, model, dim, vector, created_at)
             SELECT id, ?, ?, ?, vector, datetime('now') FROM vectors_v21`,
            backend, model, dim)

    // 5. Drop old table (data preserved in new)
    db.Exec("DROP TABLE vectors_v21")
    return nil
}
```

Result:
- **web_dev**: old rows were TF-IDF (degraded); migration preserves them tagged as `("tfidf", "tfidf", dim)`. User can run `claudemem setup` to switch to Gemini/Ollama then `reindex --vectors` to get quality semantic search.
- **MacBook**: old rows were real Ollama embeddings (`nomic-embed-text` 768-dim); migration preserves them as `("ollama", "nomic-embed-text", 768)`. Zero re-embed cost. User can switch backend any time; old vectors stay queryable under `backend="ollama"`.
- **Cross-machine sync**: after first sync, MacBook's Ollama vectors and web_dev's Gemini vectors coexist in the same table on both machines (each machine's queries filter by its own configured backend).

### Config schema

```
embedding.backend       = "ollama" | "gemini" | "voyage" | "openai" | "tfidf"
embedding.model         = model name (e.g., "gemini-embedding-001")
embedding.dimensions    = optional matryoshka truncation target
embedding.input_type    = "document" (default) — most users don't touch
embedding.api_key_env   = "GEMINI_API_KEY" | "VOYAGE_API_KEY" | "OPENAI_API_KEY"
embedding.endpoint      = optional URL override (for proxies)
embedding.on_failure    = "error" (default) | "fts_only" | "prompt"
```

**Secret guard**: `config set embedding.api_key ...` (without `_env` suffix) rejects with error. Secrets are ONLY env vars.

## Setup wizard UX

```
$ claudemem setup
claudemem — Memory Setup

Which embedding backend do you want to use?

  1) Local — Ollama (offline, zero cost, recommended for daily use)
  2) Cloud — Gemini (best quality, ~$0.15/M tokens, requires API key)
  3) Cloud — Voyage (best value, effectively free <200M tokens)
  4) Cloud — OpenAI (widely available, weaker Chinese)
  5) No semantic search — TF-IDF only (keyword-ish, no daemon/key needed)

> 1

[Ollama path]
  Checking http://localhost:11434 ... ✓ reachable
  Which model?
    1) qwen3-embedding:4b  (bilingual, 2.5GB, recommended)
    2) bge-m3              (100+ languages, 1.2GB)
    3) nomic-embed-text    (small, 270MB, English-heavy)
    4) Other (enter name)

  > 1
  Model not installed. Pull now? [Y/n] y
  (pulls qwen3-embedding:4b via ollama pull...)

  Matryoshka dimension (recommended 768 for <5k notes) [768]: ⏎
  Testing embedding: "hello world" ... ✓ got 768-dim vector
  Saved to ~/.claudemem/config.json

[Gemini path]
  Environment variable GEMINI_API_KEY: set ✓
  Which model?
    1) gemini-embedding-001 (recommended, 768-dim matryoshka)
  > 1
  Testing embedding ... ✓ got 768-dim vector
  Saved.

Rebuild vector index with new backend now? [Y/n] y
Indexing 1086 documents ... ✓ done in 42s (backend: gemini:gemini-embedding-001)
```

Equivalent manual path (documented in CLI help):
```
claudemem config set embedding.backend gemini
claudemem config set embedding.model gemini-embedding-001
claudemem config set embedding.dimensions 768
claudemem config set embedding.api_key_env GEMINI_API_KEY
claudemem reindex --vectors
```

## Failure-mode UX

Non-TTY (CI, scripts):
```
$ claudemem search "multiplexer"
Error: embedding backend `ollama:qwen3-embedding:4b` unreachable: connection refused
  Recovery options:
    - Start Ollama: ollama serve
    - Switch backend: claudemem setup
    - Search FTS-only this once: claudemem search "multiplexer" --fts-only
Exit 1
```

TTY (interactive):
```
$ claudemem search "multiplexer"
⚠ Embedding backend `ollama:qwen3-embedding:4b` unreachable: connection refused
What do you want to do?
  1) Retry
  2) Search FTS-only for this query
  3) Run `claudemem setup` to switch backend
  4) Exit
> _
```

Never silently returns TF-IDF results.

## Cross-machine sync

### Architecture

```
┌────────────────┐           ┌────────────────┐
│   web_dev      │           │    MacBook     │
│  ~/.claudemem  │           │  ~/.claudemem  │
├────────────────┤           ├────────────────┤
│ notes/*.md     │◄─ git ──►│ notes/*.md     │
│ sessions/*.md  │◄─ git ──►│ sessions/*.md  │
│ config.json    │ (local)   │ config.json    │  ← per-machine
│ .index/*.db    │ (local)   │ .index/*.db    │  ← per-machine, rebuilt
└────────────────┘           └────────────────┘
       ↓                            ↓
   Gemini vectors              Ollama vectors
   stored in local DB          stored in local DB
   (backend=gemini)             (backend=ollama)
```

### Repo choice

**Decision: new repo `~/claudemem-memory` (separate from claude-code-config)**, remote `git@github.com:zelinewang/claudemem-memory` (private). Rationale:
- claudemem-memory contains personal knowledge (not shareable config)
- claude-code-config is already multi-machine-aware but targets tools, not data
- Separate privacy boundary (claude-code-config might go public one day; memory never)
- Single-purpose: one repo = one concern (Doctrine 2)

### Commands

```
claudemem sync init     # first run: git init in ~/.claudemem, configure remote, gitignore .index/ and config.json
claudemem sync push     # commit notes/ sessions/ MEMORY.md changes, git push
claudemem sync pull     # git pull, then reconcile (rebuild FTS, re-embed missing for configured backend)
claudemem sync status   # git status + index health report
```

### Reconcile (`claudemem sync pull` internal):
1. `git pull --ff-only` (fast-forward only — no auto-merge for markdown)
2. `Reindex()` (rebuilds the SQLite entries + memory_fts from markdown)
3. `ReindexVectors()` (wipes + rebuilds vectors for the active `(backend, model)` only; rows from other backends preserved for cross-machine coexistence)
4. Report: "Reconciled N FTS entries + M vectors (backend: <name>)"

**Note**: The current implementation does a full reindex-for-active-backend
on each pull, not a diff-based incremental embed. For corpora up to ~5K
notes this is ~<30s with Gemini and acceptable. A future optimization
would be to diff the last-synced git SHA against HEAD and embed only
changed files; tracked in open decisions #3.

### Hooks integration

Extend `~/.claude/hooks/session-memory.sh`:
```bash
# On SessionStart: offer auto-pull if enabled
if [ -f "$HOME/.claudemem/.sync_auto_pull" ]; then
    claudemem sync pull --quiet || echo "⚠ claudemem sync pull failed (not fatal)"
fi
# Existing context inject:
claudemem context inject --limit 5
# NEW: health parity check
claudemem health --quick || echo "⚠ memory index drift detected. Run 'claudemem repair'."
```

Extend `~/.claude/hooks/session-end-sync.sh`:
```bash
# After wrapup, auto-push if enabled
if [ -f "$HOME/.claudemem/.sync_auto_push" ]; then
    claudemem sync push --quiet || true
fi
# Existing rsync backup stays as belt-and-braces.
```

## Health / parity check subsystem

`claudemem health` (cheap, <100ms):
- I1: `COUNT(entries) == COUNT(markdown files)`
- I2: `COUNT(entries) == COUNT(memory_fts)`
- I3: For each `(backend, model)` in use: `COUNT(vectors WHERE backend=? AND model=?) == COUNT(entries)`
- Exit 0 if all pass, 1 + warning message if drift.

`claudemem health --deep` (slower):
- I4: No orphan FTS/vector rows pointing to nonexistent entries
- I5: `vector_meta.vectorizer_state` matches active config
- Verify a sample of 10 random vectors by re-embedding and comparing cosine similarity > 0.99

`claudemem repair`:
- Runs health, offers fixes (prompt in TTY, auto in --yes mode)
- "Rebuild FTS from markdown? [Y/n]" → runs reindex --fts
- "Re-embed 17 missing docs with gemini:gemini-embedding-001? [Y/n]" → runs partial embed

## Implementation phases (value-ordered)

**Phase 1 — Embedder interface foundation (3h)**
- Write `pkg/vectors/embedder.go` (interface + InputType + errors)
- Refactor `ollama.go` to implement the interface (add `Name()`, fix `Model()`/`Dimensions()` signatures)
- Refactor `tfidf.go` to implement the interface
- Refactor `store.go`: `VectorStore.embedder Embedder` field replacing concrete ollama/tfidf fields; remove `useOllama bool`; remove all type switches
- Schema migration v22 (add backend/model/dim columns, composite PK)
- Unit tests: interface contract test applied to both implementations

**Phase 2 — Add Gemini provider (2h)**
- Write `pkg/vectors/gemini.go` using `google.golang.org/genai` Go client
- Wire into registry.go factory
- Unit test with `httptest` mock
- Live smoke test against real API (requires `GEMINI_API_KEY`)

**Phase 3 — Setup wizard (2h)**
- Write `pkg/config/wizard.go` (survey via prompts)
- Write `cmd/setup.go` CLI surface
- Environment detection (check Ollama reachable, check env vars)
- Verify step: test embedding + recorded dim
- Auto-trigger reindex after successful setup

**Phase 4 — Fail-loud + interactive recovery (1.5h)**
- Replace `tryInitOllama()` silent fallback with explicit config read + fail-loud
- Write interactive recovery prompt in `cmd/search.go` (TTY-detected)
- `--fts-only` flag on search command
- Unit tests for non-TTY error path

**Phase 5 — Health/repair subsystem (2h)**
- Write `pkg/vectors/health.go` (I1–I5 invariants)
- Write `cmd/health.go` and `cmd/repair.go`
- Extend `session-memory.sh` hook with `--quick` call
- Unit tests for drift detection

**Phase 6 — Cross-machine sync (4h)**
- Write `pkg/sync/gitsync.go` (delegates to git CLI via `os/exec`)
- Write `pkg/sync/reconcile.go` (post-pull re-embed logic)
- Write `cmd/sync.go` (init/push/pull/status subcommands)
- `.gitignore` generation (excludes `.index/`, `config.json`, `.sync_auto_*`)
- Test: two temp dirs, push from one, pull from other, verify equivalence
- Extend `session-memory.sh` and `session-end-sync.sh` with optional auto-sync hooks

**Phase 7 — Voyage + OpenAI providers (2h, parallelizable)**
- Add `voyage.go` and `openai.go` implementing Embedder
- Tests with httptest mocks
- Lower priority than Gemini since Gemini is the new cloud default

**Phase 8 — Docs + PR (1h)**
- Update README.md with Setup section
- Update SKILL.md in `skills/claudemem/` directories
- Update CLAUDE.md if claudemem commands changed
- Open PR with summary + migration note

**Total: ~17.5h = 2 focused days**

## Acceptance criteria

After each phase, these must pass:

| Gate | Test |
|---|---|
| Interface contract | Both Ollama + TF-IDF pass same unit test suite (Embed returns correct-dim vector, Available returns error with recovery hint, Name/Model/Dimensions consistent) |
| Gemini smoke | `GEMINI_API_KEY=... claudemem setup` → picks Gemini → reindex succeeds → `claudemem search "多路复用器"` returns Zellij notes |
| Fail-loud | Kill Ollama daemon, run `claudemem search "x"` → exits 1 with recovery message, does NOT return TF-IDF results |
| Per-doc metadata | Index 5 docs with Ollama, switch to Gemini, reindex — vectors table has 10 rows (5 ollama + 5 gemini); search under each backend returns correct results independently |
| Health drift | Delete a markdown file manually → `claudemem health` reports drift; `claudemem repair` offers to remove orphan entry |
| Sync round-trip | Worktree A: add note, push; Worktree B: pull → markdown present, FTS contains it, vector created under B's backend |
| Hooks | SessionStart hook runs health --quick in <200ms and doesn't block shell |

## Open decisions (small, can resolve as we go)

1. **Auto-pull on SessionStart by default?** Proposal: opt-in via `touch ~/.claudemem/.sync_auto_pull`. Reason: unexpected network calls on session start are surprising.
2. **Markdown conflict strategy on pull?** Proposal: append-with-separator merge (mirrors existing note-dedup logic), not git merge. Reason: markdown notes don't have meaningful diffs — keep both.
3. **Batch size for initial reindex?** Gemini allows 250/batch, Voyage 128, Ollama 50. Proposal: let each embedder expose `MaxBatchSize()` and let RebuildIndex respect it.
4. **Should `claudemem health --quick` run on every search?** Proposal: no — only at SessionStart (once per session). Cache a freshness token.
5. **sqlite-vec library?** For <10K docs, brute-force is fine (<50ms). Defer to future — not in this refactor.

## Risks & mitigations

- **Gemini API changes** (preview → GA) — wrap in versioned client; pin API version in config.
- **Git conflicts on markdown** — mitigated by append-with-separator merge policy + timestamp metadata.
- **Accidentally commit secrets** — `config set api_key` rejects (secret guard); `.gitignore` excludes config.json; pre-commit hook in claudemem-memory repo greps for common key prefixes.
- **Backend switch orphans old vectors** — NOT dropped automatically; `claudemem vacuum` cleans unused (backend, model) rows explicitly.
- **Cross-machine clock drift affects timestamps in markdown merges** — use session_id (UUID) as primary dedup key, not timestamp.

## References

- Current architecture source: `pkg/vectors/store.go` (413 LOC, inspected 2026-04-14)
- Previous plan: git `cfe05bd` (superseded by this doc)
- Research: MTEB leaderboard, Qwen3 HF, Gemini Embedding API docs, Voyage docs, BentoML 2026 guide, Premai RAG ranking
- Hook integration map: `~/.claude/hooks/session-memory.sh`, `~/.claude/hooks/session-end-sync.sh`, `~/.claude/commands/wrapup.md`
- Cross-machine host repo: `~/claude-code-config/` (3-tier sync system, separate from this plan)
