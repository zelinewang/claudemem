# Hybrid Embedding Backend — Implementation Plan

**Status**: PLANNING (2026-04-14) — pending next-session implementation
**Owner**: Zane
**Effort**: Phase A 5 min · Phase B 30 min · Phase C 2–6 hours

## Why

Current claudemem has exactly two embedding backends:
- **Ollama** (primary, requires local daemon)
- **TF-IDF** (automatic fallback when Ollama unavailable)

This is brittle across machines. On web_dev, Ollama isn't installed → TF-IDF is used → semantic search is crippled (`claudemem search "多路复用器"` returns `[]` even though Zellij notes exist). The database was originally indexed on MacBook with `nomic-embed-text`, but the metadata was reset to `tfidf` during the 2026-04-14 reindex when the current backend couldn't match.

Goals:
1. **Make semantic search work on every machine** — not just MacBook.
2. **Support pluggable backends** — local Ollama (offline, zero-cost) AND cloud APIs (best quality) via config, no recompile.
3. **Preserve FTS** — FTS is still the right tool for exact matches (IDs, hashes, error codes). Hybrid search (FTS + vector) stays.

## Research Findings — Best Embeddings (April 2026)

### Local (recommended for web_dev + MacBook)

| Rank | Model | Why | Ollama command | Dim (native→recommended) |
|------|-------|-----|----------------|--------------------------|
| **1** | `qwen3-embedding:4b` | Bilingual Chinese+English, 32K ctx, Matryoshka truncation. Qwen3 family held MTEB Multilingual top spot 2025. | `ollama pull qwen3-embedding:4b` | 2560 → **768** |
| 2 | `bge-m3` | Battle-tested since 2024, 100+ languages, strong Chinese, dense+sparse+ColBERT in one. | `ollama pull bge-m3` | 1024 |
| 3 (current default) | `nomic-embed-text` | Safe fallback, small (~270MB), fast, English-heavy. Already claudemem default. | `ollama pull nomic-embed-text` | 768 |

**Disk cost**: qwen3:4b ≈ 2.5GB, bge-m3 ≈ 1.2GB, nomic ≈ 270MB.

### Cloud (recommended when you want better quality than local)

| Rank | Provider/Model | Cost /1M tokens | Max input | Dim | Notes |
|------|----------------|-----------------|-----------|-----|-------|
| **1** | Voyage `voyage-3-large` | $0.06–0.18 | 32K | 1024 (matryoshka from 2048) | Best overall quality, ~3% ahead of OpenAI 3-large on third-party benchmarks (Agentset). 200M free tokens. |
| Budget | OpenAI `text-embedding-3-small` | $0.02 | 8K | 512 (matryoshka from 1536) | 3× cheaper than 3-large, serviceable Chinese, widely compatible tooling. |
| Not recommended | OpenAI `text-embedding-3-large` | $0.13 | 8K | 3072 | Outclassed by voyage-3-large on quality AND cost. Include only as reference. |

### Caveats (must flag before acting)

- **MTEB v1 vs v2 scores are NOT comparable.** Don't treat aggregator leaderboards as apples-to-apples.
- **Voyage's "+9.7% vs OpenAI" is vendor marketing.** Third-party benchmarks (Agentset) show ~3% — directionally correct, magnitude inflated.
- **Qwen3 Ollama quantization quality on Chinese retrieval is unverified** — no public C-MTEB for `qwen3-embedding:4b-q4_K_M`.
- Research done via Claude subagent (web search + MTEB HF space + vendor docs). Cross-reference before spending money.

Full research trace: session `fa4d137a` (2026-04-14) + note `86629e7e`.

## Current Architecture (verified from source)

Source: `~/claudemem/pkg/vectors/` — **this is Zane's own fork** (github.com/zelinewang/claudemem).

```
pkg/vectors/
├── store.go        # VectorStore — orchestrates backends, init logic
├── ollama.go       # OllamaEmbedder — concrete HTTP client to localhost:11434
├── tfidf.go        # Vectorizer — TF-IDF fallback
└── *_test.go
```

Key code observations:

- `VectorStore` struct has **concrete fields**, not interface:
  ```go
  type VectorStore struct {
      db         *sql.DB
      vectorizer *Vectorizer       // TF-IDF fallback
      ollama     *OllamaEmbedder   // Ollama primary (nil if unavailable)
      useOllama  bool
  }
  ```
- `tryInitOllama(model string)` hardcodes the single init path.
- `OllamaEmbedder.baseURL` hardcoded to `"http://localhost:11434"` (ollama.go:43).
- Default model `"nomic-embed-text"` hardcoded in `NewOllamaEmbedder` (ollama.go:40).
- Config system (`~/.claudemem/config.json`) is Cobra/Viper-style key-value — `claudemem config set/get/list/delete` works but only `features.semantic_search` is actively read today.

**Gap**: There is no `Embedder` interface. Adding cloud requires introducing one AND refactoring `VectorStore` to depend on the interface.

## Implementation Plan

### Phase A — Install Ollama on web_dev (5 minutes)

**Goal**: get semantic search working TODAY with the existing claudemem default.

Blocker encountered this session: official install script needs sudo (not available); manual tarball URL returned 404 (release layout changed to `.tar.zst`). Two options:

**Option A1 — Official installer via user sudo** (preferred if user can sudo interactively)
```bash
curl -fsSL https://ollama.com/install.sh | sh
# Prompts for sudo password; installs to /usr/local and creates systemd service.
```

**Option A2 — User-local install (no sudo)**
```bash
# Find current release asset name (format changed to tar.zst)
LATEST=$(curl -sL https://api.github.com/repos/ollama/ollama/releases/latest \
  | grep -oE '"browser_download_url":\s*"[^"]+linux-amd64\.tar\.zst"' | head -1 \
  | cut -d'"' -f4)
curl -fsSL "$LATEST" -o /tmp/ollama.tar.zst

# Need zstd to extract. Install if missing:
command -v zstd || sudo apt install -y zstd
# (or: download prebuilt zstd binary to ~/.local/bin — but apt is simpler)

# Extract to ~/.local
mkdir -p ~/.local
tar --zstd -xf /tmp/ollama.tar.zst -C ~/.local/
# Produces ~/.local/bin/ollama + ~/.local/lib/ollama/

# Start service in background
~/.local/bin/ollama serve &
# (better: create systemd --user service or fish conf.d launcher)

# Pull default model to match claudemem's hardcoded default
~/.local/bin/ollama pull nomic-embed-text
```

**Verification**:
```bash
curl -s http://localhost:11434/api/tags | jq '.models[].name'
# Should include: "nomic-embed-text:latest"
```

Then rebuild claudemem's vector index:
```bash
claudemem reindex --vectors
# Expected output: "Vector index rebuilt: 1086 documents indexed (backend: ollama)"
```

**Success criterion**:
```bash
claudemem search "多路复用器" --limit 3 --format json
# Should return Zellij-related notes via semantic match.
# Currently returns [] under tfidf.
```

### Phase B — Upgrade to qwen3-embedding:4b (optional, 30 minutes)

**Goal**: bilingual retrieval quality for Chinese/English mixed notes.

```bash
ollama pull qwen3-embedding:4b    # ~2.5GB
```

Problem: claudemem hardcodes `nomic-embed-text`. To use qwen3 without code changes, the cleanest path is to tag it as the model alias Ollama uses internally. But since claudemem pulls from the `NewOllamaEmbedder(model)` path that defaults to `"nomic-embed-text"`, the only runtime lever today is the (private) `NewVectorStoreWithModel(db, model)` entry point — not exposed via CLI.

**Three ways to force a different model**:

1. **Cheap hack**: `ollama cp nomic-embed-text nomic-embed-text-backup && ollama cp qwen3-embedding:4b nomic-embed-text` — aliases qwen3 under nomic's name. Works but confusing.
2. **Surgical patch**: change the hardcoded string in `pkg/vectors/ollama.go:40`, rebuild, replace binary. Fast, reversible via git.
3. **Wait for Phase C** (config-driven model selection). Recommended.

### Phase C — Pluggable Embedding Backend (2–6 hours, the real work)

**Scope**: introduce `Embedder` interface + 2 new backends (OpenAI, Voyage) + config-driven dispatch + backend consistency checks that include backend name + model.

#### C.1 — Define the interface (30 min)

New file: `pkg/vectors/embedder.go`
```go
package vectors

// Embedder produces dense vector embeddings from text.
// Implementations: OllamaEmbedder (local), OpenAIEmbedder, VoyageEmbedder.
type Embedder interface {
    // Available reports whether the backend is reachable and the model is loaded.
    Available() bool
    // Embed returns the embedding for a single input.
    Embed(text string) ([]float32, error)
    // EmbedBatch returns embeddings for multiple inputs in one round-trip.
    EmbedBatch(texts []string) ([][]float32, error)
    // Model returns the model identifier (e.g. "nomic-embed-text", "voyage-3-large").
    Model() string
    // Dimensions returns the embedding vector length (after any truncation).
    Dimensions() int
    // Name returns the backend name (e.g. "ollama", "openai", "voyage") for
    // consistency checks and logging.
    Name() string
}
```

Refactor `VectorStore`:
```go
type VectorStore struct {
    db         *sql.DB
    vectorizer *Vectorizer   // kept as ultimate fallback
    embedder   Embedder      // replaces *OllamaEmbedder; nil means tfidf-only
}
```

Rename `tryInitOllama` → `initEmbedderFromConfig(cfg EmbedderConfig)` that dispatches on `cfg.Backend`.

#### C.2 — Config schema (30 min)

Add keys readable via `claudemem config get/set`:
```
embedding.backend         = ollama | openai | voyage | tfidf
embedding.model           = nomic-embed-text | qwen3-embedding:4b | voyage-3-large | text-embedding-3-small
embedding.dimensions      = 768    (optional — matryoshka truncation)
embedding.api_key_env     = VOYAGE_API_KEY | OPENAI_API_KEY  (for cloud backends)
embedding.endpoint        = http://localhost:11434 | https://api.voyageai.com/v1/embeddings  (override URL)
embedding.timeout_seconds = 30
```

Config load order:
1. `~/.claudemem/config.json` (explicit user choice)
2. Env var `CLAUDEMEM_EMBEDDING_BACKEND` (runtime override)
3. Auto-detect: if ollama reachable → ollama, else if `VOYAGE_API_KEY` set → voyage, else tfidf.

#### C.3 — OpenAIEmbedder (45 min)

New file: `pkg/vectors/openai.go`
```go
// POST https://api.openai.com/v1/embeddings
// Body: {"model": "text-embedding-3-small", "input": ["text1", "text2"], "dimensions": 512}
// Header: Authorization: Bearer $OPENAI_API_KEY
```
- Matryoshka: pass `dimensions` field to truncate server-side (cheaper transfer).
- Batch limit: 2048 inputs per request, 8191 tokens per input.
- Error handling: 429 (rate limit) → exponential backoff 3×; 401 → fail loud.

#### C.4 — VoyageEmbedder (45 min)

New file: `pkg/vectors/voyage.go`
```go
// POST https://api.voyageai.com/v1/embeddings
// Body: {"model": "voyage-3-large", "input": ["text1"], "input_type": "document", "output_dimension": 1024}
// Header: Authorization: Bearer $VOYAGE_API_KEY
```
- `input_type` = `"document"` for notes, `"query"` for searches. Separate code paths.
- Max 128 inputs per batch, 32K tokens per input.
- Response shape similar to OpenAI.

#### C.5 — Backend consistency checks (30 min)

Currently `checkBackendConsistency` compares `"ollama"` vs `"tfidf"` only. Extend to `backend:model` tuple (`"ollama:nomic-embed-text"` vs `"voyage:voyage-3-large"`) so switching model — not just backend — triggers a reindex warning.

Store in `vector_meta` table (already exists):
```
key = "vectorizer_state"  → JSON {"backend": "voyage", "model": "voyage-3-large", "dimensions": 1024}
```

#### C.6 — Tests + README (30 min)

- Unit: mock HTTP server for OpenAI/Voyage, assert request shape + response parsing.
- E2E: add `e2e_test.sh` case that sets `OPENAI_API_KEY` from env (if set) and does a round-trip. Skip if env var missing — don't fail CI.
- README: new `## Embedding Backends` section with config examples per backend.

#### C.7 — Migration path for existing users (15 min)

Existing users with ollama-indexed DBs on MacBook: Phase C release should NOT break them. Strategy:
- If no `embedding.backend` key in config → fall back to current auto-detect behavior.
- If `vector_meta.vectorizer_state` was written by old format (just `"ollama"`) → treat as `"ollama:nomic-embed-text"`.
- Log a one-line deprecation warning, don't force reindex.

### Verification criteria (all three phases)

After each phase, these must pass:

| Test | Expected |
|------|----------|
| `claudemem stats` | No warning about backend mismatch |
| `claudemem search "zellij" --limit 3` | Returns zellij notes (FTS still works) |
| `claudemem search "多路复用器" --limit 3` | Returns zellij notes via semantic match (proves vector works for cross-language) |
| `claudemem search "config drift"` | Returns notes about config, not just literal-word matches (proves semantic) |
| `claudemem reindex --vectors` output | `backend: <name>` matches config |

## Open Decisions (user input needed before Phase C)

1. **Local vs Cloud default** — should claudemem default to Ollama if available, even if `VOYAGE_API_KEY` is set? Proposal: yes, prefer local (zero cost, offline). Cloud is opt-in via explicit `embedding.backend = voyage`.
2. **Upstream this?** — Phase C is a meaningful feature. Worth a PR to upstream claudemem? Or keep in personal fork? Proposal: ship in fork first, polish via real use, then upstream if stable.
3. **Chinese-heavy content** — real usage split between Chinese and English notes? If 80%+ Chinese, qwen3/voyage multilingual are must-haves. If 80%+ English, nomic/OpenAI are fine.
4. **API key storage** — env var (`VOYAGE_API_KEY`) vs config file. Proposal: env var only. Config file should NEVER store secrets (git safety).
5. **Priority**: Phase A blocking (do first), Phase B optional, Phase C is the real investment. Confirm before allocating 2-6 hours.

## Risks & Mitigations

- **Ollama service crashes mid-session** → claudemem should detect and fall back to tfidf gracefully without losing user queries. Current `Available()` check only runs on init — needs per-call retry.
- **API key leak** → always read from env, never from config file. Add `config set embedding.api_key ...` → reject with error.
- **Dimension mismatch on backend switch** → old vectors (768 dim from nomic) incompatible with new (1024 dim from voyage). `checkBackendConsistency` must BLOCK queries until reindex completes, not just warn. Currently warns only.
- **Vendor lockin** → if Voyage raises prices, need fast migration path to OpenAI. The interface abstraction handles this.

## References

- Session 2026-04-14 (claudemem `fa4d137a`) — research + architecture investigation trace
- Note `92d9f5ab` — Zellij 0.43 layout gotchas (peripheral)
- Note `86629e7e` — 3-axis decision framework (not directly used here but shows methodology)
- Source files investigated: `~/claudemem/pkg/vectors/store.go`, `~/claudemem/pkg/vectors/ollama.go`
- External: https://huggingface.co/spaces/mteb/leaderboard, https://blog.voyageai.com, https://platform.openai.com/docs/guides/embeddings
