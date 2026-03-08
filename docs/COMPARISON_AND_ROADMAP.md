# claudemem vs claude-mem: Deep Comparison & Roadmap

## Executive Summary

Two projects solving the same problem — persistent memory for AI coding assistants — with fundamentally different philosophies.

| | **claudemem** (zelinewang) | **claude-mem** (thedotmack) |
|---|---|---|
| Philosophy | **Pull-based, Unix-style CLI** | **Push-based, plugin ecosystem** |
| Language | Go (single static binary) | TypeScript (Bun + Node.js + Python) |
| Binary | 7.5 MB, CGO_ENABLED=0 | ~186 MB (node_modules + Chroma + worker) |
| Network | Zero (verified at build time) | Localhost HTTP worker on :37777 |
| Storage | Markdown files + SQLite FTS5 | SQLite + Chroma vector DB |
| Automation | Manual (skills invoke CLI) | Automatic (lifecycle hooks capture everything) |
| Maturity | v2.0.0, 41 commits, 204 tests | v10.5.2, 2284 commits, 65 test files |

---

## 1. Architectural Differences (Objective)

### 1.1 Data Capture Model

**claudemem — Pull Model:**
- User/skill explicitly invokes commands: `note add`, `session save`, `/wrapup`
- User decides WHAT to remember and WHEN
- Nothing is captured without explicit action
- Trade-off: Higher signal-to-noise ratio, but requires discipline

**claude-mem — Push Model:**
- 5 lifecycle hooks auto-capture at: SessionStart, UserPromptSubmit, PostToolUse, Stop, SessionEnd
- Every tool use generates an "observation" (XML-parsed structured data)
- Auto-summaries at session end (investigated/learned/completed/next_steps)
- Trade-off: Comprehensive capture, but higher noise and storage overhead

### 1.2 Search Architecture

**claudemem — Single-Layer FTS5:**
```
Query → SQLite FTS5 MATCH → BM25 ranking → Full results returned
```
- Speed: <10ms
- Capability: Keyword matching only
- Token cost: Returns full content immediately

**claude-mem — Hybrid 3-Layer Progressive Disclosure:**
```
Layer 1: search() → IDs + titles only (~50-100 tokens)
Layer 2: timeline() → Context around results (~200-500 tokens)
Layer 3: get_observations() → Full content on demand (~500-1K tokens)
```
Plus: Chroma vector DB for semantic/concept-level search
- Speed: <100ms (vector overhead)
- Capability: Keyword + semantic similarity
- Token cost: 8-12x savings via progressive disclosure

### 1.3 Storage Design

**claudemem — Dual-Write (Markdown + SQLite):**
- Source of truth: Markdown files with YAML frontmatter
- SQLite FTS5 index: Regenerable cache for fast search
- Human-readable, git-friendly, portable, editable with any text editor
- Backup: `export/import` as tar.gz

**claude-mem — SQLite + Vector Store:**
- Source of truth: SQLite database (`~/.claude-mem/claude-mem.db`)
- Vector store: Chroma (Python subprocess via MCP)
- Binary format, requires tooling to inspect
- Backup: Manual SQLite dump

### 1.4 Context Injection

**claudemem — Manual:**
- `/recall [topic]` to search past knowledge
- No automatic context at session start
- User must remember to invoke recall

**claude-mem — Automatic:**
- SessionStart hook calls `/api/context/inject`
- ContextBuilder selects recent observations + summaries
- Auto-injected into system context before first turn
- Token economics tracking (discovery cost vs read cost)

### 1.5 Code Intelligence

**claudemem — None:**
- No AST parsing, no code structure analysis
- File-level operations only

**claude-mem — Smart Code Tools (tree-sitter):**
- `smart_search`: Find symbols across codebase (10 languages)
- `smart_outline`: Folded file structure (signatures without bodies)
- `smart_unfold`: Expand single symbol on demand
- Progressive disclosure: 8-12x token savings vs Glob → Grep → Read

### 1.6 Integration Model

**claudemem — CLI Tool + Skills:**
- Standalone binary invoked via bash
- Skills define trigger phrases and command templates
- No lifecycle hooks, no background processes
- Zero runtime overhead when not in use

**claude-mem — Plugin + Worker + Hooks:**
- Claude Code plugin with hooks.json
- Persistent HTTP worker service (Bun daemon on :37777)
- MCP server for search tools
- Process management: PID files, zombie cleanup, port allocation
- 150-200 MB memory usage when active

---

## 2. Strengths Assessment

### claudemem Strengths
1. **Simplicity** — 5,500 LOC Go, auditable in a day
2. **Zero network** — Build target `verify-no-network` ensures no net/http imports
3. **Data sovereignty** — Markdown files are universal, git-trackable, human-readable
4. **Reliability** — No background processes, no port conflicts, no zombie cleanup
5. **Testing** — 204 tests (107 unit + 5 smoke + 10 E2E + 82 feature tests)
6. **Dedup intelligence** — Topic-based note merge, session-based merge with history
7. **Cross-referencing** — Bidirectional Note ↔ Session links with prefix lookup
8. **Portability** — Single binary, works on Linux/macOS/Windows, no runtime deps

### claude-mem Strengths
1. **Full automation** — Zero manual commands needed for capture
2. **Semantic search** — Concept-level discovery via vector embeddings
3. **Token efficiency** — 3-layer progressive disclosure saves 8-12x tokens
4. **Code intelligence** — AST-based symbol navigation (tree-sitter, 10 languages)
5. **Context injection** — Automatic past-context loading at session start
6. **Web viewer** — Visual dashboard at localhost:37777
7. **Mature ecosystem** — v10.5.2, 2284 commits, active community, docs site
8. **Multi-LLM support** — Pluggable agents (Claude, Gemini, OpenRouter)

---

## 3. Weaknesses Assessment

### claudemem Weaknesses
| Gap | Impact | Severity |
|-----|--------|----------|
| No semantic search | Can't find conceptually related content without exact keywords | High |
| No auto context injection | Must manually `/recall` every session | High |
| No progressive disclosure | Full content returned always, wastes tokens | Medium |
| No code intelligence | Can't navigate code structure efficiently | Medium |
| Manual-only capture | Relies on discipline/skill prompts | Medium |

### claude-mem Weaknesses
| Issue | Impact | Severity |
|-------|--------|----------|
| Heavy footprint (186 MB) | Resource-hungry, slow install | Medium |
| Complex stack (Bun + Node + Python + Chroma) | Fragile dependency chain | High |
| Opaque storage (binary SQLite) | Can't inspect/edit data directly | High |
| Worker process management | Zombie cleanup, port conflicts, platform quirks | Medium |
| AGPL license | Restrictive for commercial/integrated use | High |
| No human-readable export | Data locked in tool-specific format | Medium |

---

## 4. Roadmap: What claudemem Should Borrow

### Design Principles for Borrowing
- **Borrow the IDEA, not the implementation** — claude-mem's TypeScript/Bun approach doesn't fit Go CLI
- **Maintain core constraints**: zero network, CGO_ENABLED=0, single binary, human-readable storage
- **Tiered fallbacks**: Rich features degrade gracefully to simpler alternatives
- **Optional > mandatory**: New capabilities are opt-in, never break existing usage

---

### Phase 1: Quick Wins (v2.1.0) — ~1 week

#### 1A. Progressive Disclosure Search
**Borrow from**: claude-mem's 3-layer search workflow

Add `--compact` and `--context` flags to search:
```bash
# Layer 1: Index only (IDs + titles + scores)
claudemem search "auth" --compact
# → ~100 tokens output

# Layer 2: With preview context
claudemem search "auth" --context
# → ~500 tokens output

# Layer 3: Full content (existing behavior, unchanged)
claudemem search "auth"
# → Full results
```

**Implementation**: Modify `cmd/search.go` + `cmd/output.go`
- Add `CompactSearchResult` struct (ID, Title, Score only)
- Add `ContextSearchResult` struct (+ Preview 200 chars, Category, Created)
- ~80 lines of new code
- **Zero new dependencies**

#### 1B. Auto Context Injection via SessionStart Hook
**Borrow from**: claude-mem's SessionStart hook + context builder

New command + hook configuration:
```bash
# New CLI command
claudemem context inject [--project X] [--limit 5] [--compact]

# Returns: Recent notes + last session summary, formatted for Claude context
```

Hook configuration in skill's `.claude/settings.json`:
```json
{
  "hooks": {
    "SessionStart": [{
      "matcher": "",
      "hooks": [{
        "type": "command",
        "command": "claudemem context inject --compact --limit 5"
      }]
    }]
  }
}
```

**Implementation**: New `cmd/context_inject.go` (~100 lines)
- Query last N notes + last 2-3 sessions
- Format as concise markdown (<2KB)
- **Zero new dependencies**

#### 1C. Compact Output Format
**Borrow from**: claude-mem's token-conscious output design

Add to `cmd/output.go`:
- `--format compact` returns minimal JSON (ID + title + score)
- `--format context` returns medium JSON (+ preview + metadata)
- Existing `--format json` unchanged (full content)

**Implementation**: ~30 lines in `cmd/output.go`

---

### Phase 2: Search Enhancement (v2.2.0) — ~2 weeks

#### 2A. BM25 Keyword Ranking Layer
**Borrow from**: claude-mem's hybrid search ranking concept

Current FTS5 uses built-in BM25, but we can enhance with:
- Recency boost (newer results ranked higher)
- Category/tag weighting
- Frequency-based term importance

**Implementation**: Add scoring function in `pkg/storage/filestore_search.go`
- Custom `ScoreFunc` that combines FTS5 rank + recency + tag overlap
- ~150 lines
- **Dependency**: None (pure Go math)

#### 2B. Faceted Search
**Borrow from**: claude-mem's filter-rich search API

```bash
claudemem search "auth" --category api --tag security --after 2025-01-01 --sort date
```

**Implementation**: Add `SearchOpts` struct, extend SQL query builder
- ~200 lines in `pkg/storage/filestore_search.go`
- ~50 lines in `cmd/search.go` for new flags
- **Zero new dependencies**

#### 2C. Optional Semantic Search via Local Ollama
**Borrow from**: claude-mem's Chroma vector search concept (not implementation)

Strategy: If user has Ollama running locally, use it for embeddings. Otherwise, graceful fallback to FTS5.

```bash
# Enable (opt-in)
claudemem config set features.semantic_search true

# Claudemem checks localhost:11434 for Ollama
# If available: compute embeddings, store in SQLite vectors table
# If unavailable: fall back to FTS5 (zero degradation)
```

**Implementation**:
- New `pkg/storage/vectors.go` — SQLite table for embeddings
- New `pkg/embeddings/ollama.go` — Optional Ollama client (~200 lines)
- Feature flag gated: `config.GetBool("features.semantic_search")`
- **Dependency**: `github.com/ollama/ollama/api` (pure Go, optional)
- **Network**: Localhost only, opt-in, graceful fallback

---

### Phase 3: Code Intelligence (v2.3.0) — ~2-3 weeks

#### 3A. Regex-Based Smart Outline (MVP)
**Borrow from**: claude-mem's `smart_outline` concept (not tree-sitter implementation)

```bash
claudemem code outline <file>
# Returns: Function/class/method signatures without bodies

claudemem code search <symbol> [--path .]
# Returns: Files containing matching symbols
```

**Implementation**: Regex patterns for common languages
- Go: `go/ast` (stdlib, perfect accuracy)
- Python: `^def |^class |^async def ` patterns (~90% accuracy)
- TypeScript/JavaScript: `^function |^class |^export ` patterns (~80%)
- Rust: `^fn |^struct |^impl |^trait ` patterns (~85%)
- ~300 lines in `pkg/code/outline.go`
- **Zero new dependencies** (Go stdlib `regexp` + `go/ast`)

#### 3B. Optional Universal Ctags Integration
**Borrow from**: claude-mem's multi-language AST approach

If user has `universal-ctags` installed, use it for accurate parsing of 40+ languages.

```bash
# Auto-detect at startup
which universal-ctags  # If found, use it; otherwise, regex fallback
```

**Implementation**: ~100 lines wrapper
- **Dependency**: External tool (optional), `go-ctags` wrapper (pure Go)

---

### Phase 4: Advanced Features (v3.0.0) — Future

#### 4A. In-Process Vector Search (chromem-go)
**What**: Pure Go vector database for semantic search without external services
- Library: `github.com/philippgille/chromem-go` (pure Go, CGO_ENABLED=0 compatible)
- Binary impact: +3 MB
- No network, no external process

#### 4B. Token Economics Tracking
**Borrow from**: claude-mem's discovery-tokens vs read-tokens model
- Track: How many tokens were spent creating a note vs reading it
- Show ROI: "This note saved 12,000 tokens across 5 sessions"

#### 4C. Knowledge Graph Visualization
- `claudemem graph --format dot` → Graphviz visualization of Note ↔ Session links
- `claudemem graph --format json` → Adjacency list for programmatic use

#### 4D. Auto-Capture Hook (PostToolUse)
**Borrow from**: claude-mem's automatic observation capture
- Hook into Claude Code's PostToolUse lifecycle
- Auto-extract key decisions/discoveries from tool outputs
- Store as notes with auto-generated titles and tags
- Feature-flagged, opt-in

---

## 5. What NOT to Borrow

| claude-mem Feature | Why Skip |
|---|---|
| **Persistent worker process** | Violates simplicity. CLI-per-invocation is more reliable. |
| **Chroma vector DB** | Requires Python runtime. Use chromem-go or Ollama instead. |
| **tree-sitter native bindings** | Requires CGO. Use regex + optional ctags instead. |
| **Web viewer UI** | Scope creep. Markdown files ARE the viewer. |
| **Multi-LLM agent system** | Out of scope. claudemem is a storage tool, not an agent framework. |
| **34 language modes** | Over-engineering. One good English mode is sufficient. |
| **AGPL licensing model** | Restrictive. Keep MIT/Apache. |
| **MCP server integration** | Adds complexity. CLI invocation via skills is simpler and sufficient. |

---

## 6. Implementation Priority Matrix

| Feature | Impact | Effort | Dependencies | Phase |
|---------|--------|--------|--------------|-------|
| Progressive disclosure (`--compact`) | High | Low | None | 1 |
| SessionStart context injection | High | Low | None | 1 |
| Faceted search | Medium | Low | None | 2 |
| BM25 ranking enhancement | Medium | Low | None | 2 |
| Regex-based code outline | Medium | Medium | None | 3 |
| Optional Ollama embeddings | High | Medium | Ollama (opt-in) | 2 |
| chromem-go vector search | High | Medium | chromem-go | 4 |
| Universal ctags integration | Low | Low | ctags (opt-in) | 3 |
| Token economics tracking | Low | Medium | None | 4 |
| Knowledge graph export | Low | Low | None | 4 |
| Auto-capture hook | Medium | High | Claude Code hooks | 4 |

---

## 7. Dependency Impact Summary

### Current (v2.0.0): 7 direct deps, 7.5 MB binary

### After Phase 1-3: Same deps, same binary size
- All Phase 1-3 features use **zero new dependencies**
- Pure Go stdlib (`regexp`, `go/ast`, `net/http` for optional Ollama)
- Binary stays ~7.5 MB

### After Phase 4: +1-2 optional deps, ~10-11 MB binary
- `chromem-go` (+3 MB, optional, feature-flagged)
- `go-ctags` (+50 KB, optional, wraps external tool)

### Core Constraints Preserved
- Zero network by default (Ollama/chromem are opt-in)
- CGO_ENABLED=0 (all pure Go)
- Single static binary
- Human-readable Markdown storage unchanged
- Backward compatible (existing data works without changes)
