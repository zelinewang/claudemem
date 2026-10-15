# claudemem

Persistent memory for AI coding agents — notes and session reports that survive across conversations, searchable by FTS5 keyword **and** semantic vectors, in a single zero-network Go binary.

[![CI](https://img.shields.io/github/actions/workflow/status/zelinewang/claudemem/ci.yml?label=CI)](https://github.com/zelinewang/claudemem/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/zelinewang/claudemem)](https://github.com/zelinewang/claudemem/releases)
[![Go](https://img.shields.io/github/go-mod/go-version/zelinewang/claudemem)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Your agent forgets everything between sessions. claudemem gives it a durable,
inspectable memory: knowledge notes and work-session reports, stored as plain
Markdown, indexed for fast search, and cross-linked so any fact traces back to
the session that produced it.

```console
$ claudemem note add architecture --title "FTS5 sanitization" \
    --content "Hyphens and quotes in queries are escaped before the MATCH clause." \
    --tags "sqlite,search"
✓ Added note to architecture: "FTS5 sanitization" (id: fee13be2)

$ claudemem session save --title "Ship v3 release pipeline" --branch feat/goreleaser \
    --project claudemem --session-id s1 --summary "Wired goreleaser + CI; cut v3.0.12."
✓ Saved session: "Ship v3 release pipeline" (2026-07-12, feat/goreleaser) [id: 2ec8c555]

$ claudemem search "FTS5"
Found 1 results for "FTS5":
1. 📝 [note] FTS5 sanitization
   category: architecture | tags: sqlite, search
   Hyphens and quotes in queries are escaped before the MATCH clause.

$ claudemem stats
ClaudeMem Statistics
====================
Notes:    1
Sessions: 1
Storage:  100.6 KB
```

## Highlights

- **Markdown is the source of truth** — every note and session is a human-readable `.md` file with YAML frontmatter. The SQLite index is a regenerable cache, not the record.
- **Hybrid search** — FTS5 keyword search fused with semantic vectors, score-weighted for the keyword-heavy queries agents actually make.
- **Zero network by default** — no telemetry, no daemon. Cloud embedding backends are an explicit per-machine opt-in; API keys come from env vars only, never written to disk.
- **Bidirectional cross-referencing** — notes link to the session that produced them; sessions list their related notes. Nothing is a dead end.

## Install

```bash
npx skills add zelinewang/claudemem --skill claudemem --global
```

This installs only the `claudemem` skill at user level. Next time you start
Claude Code (or Cursor, Gemini CLI, etc.), it just works. Your saved data lives
in `~/.claudemem/` and is never touched by upgrades. To upgrade later, run
`npx skills update claudemem --global`.

Prefer to build it yourself? See [Build from source](#build-from-source).

## Talk to It Naturally

During your work claudemem silently saves important context — API specs,
decisions, quirks, resolved bugs. When you start a new task it searches past
knowledge automatically. You can also drive it in plain language:

| Say this | What happens |
|----------|-------------|
| "remember this" | Saves the current info as a note |
| "what do you remember about TikTok" | Searches past notes |
| "wrap up" | Saves a detailed session report + extracts notes |
| "what did we do last time" | Shows recent sessions |

Or use slash commands directly: `/wrapup`, `/recall [topic]`.

## What Gets Saved

```
~/.claudemem/
├── notes/          ← knowledge fragments (markdown)
├── sessions/       ← work reports with cross-links (markdown)
└── .index/         ← search index (auto-rebuilt)
```

Everything is plain Markdown with YAML frontmatter — human-readable,
git-friendly, portable.

## Architecture

Every write goes to **both** the Markdown files and the SQLite index. The
Markdown is authoritative; the index (FTS5 rows + embedding vectors) is a cache
that can be dropped and rebuilt from the Markdown at any time.

```
        claudemem note add / session save
                       │
            ┌──────────┴───────────┐
            ▼                      ▼
   ~/.claudemem/*.md         SQLite  .index/
   Markdown + YAML           FTS5 rows + vectors
   = source of truth         = regenerable cache
            │                      │
            └──────────┬───────────┘
                       ▼
      search  =  FTS5 keyword  ⊕  semantic vectors
                (keyword-weighted score fusion)
```

Notes and sessions look similar but dedupe on opposite keys, because they mean
different things:

| | Notes | Sessions |
|---|---|---|
| **Purpose** | Knowledge fragments | Work reports |
| **Dedup key** | Same title + category | Same `session_id` |
| **Merge behavior** | Append content | Append all sections |
| **Cross-link** | `metadata.session_id` | `## Related Notes` |

Knowledge accumulates by topic (same note grows), while distinct conversations
stay separate (each session is its own record). Schema migrations preserve
existing data across upgrades.

## CLI Reference

```bash
# Notes (knowledge fragments)
claudemem note add <category> --title "..." --content "..." [--tags "..."] [--session-id "..."]
claudemem note search "query" [--in category] [--tag tags]
claudemem note list [category]
claudemem note get <id-or-prefix> [--tag t1,t2]
claudemem note append <id> "additional content"
claudemem note update <id> --title "..." --content "..." --tags "..."   # --tags replaces all tags
claudemem note update <id> --add-tag t1,t2 --remove-tag t3              # keeps the other tags
claudemem note delete <id>
claudemem note categories
claudemem note tags

# Sessions (work reports)
claudemem session save --title "..." --branch "..." --project "..." --session-id "..." \
  [--tags "..."] [--related-notes "id:title:category,..."] [--content "..."]
claudemem session list [--last N] [--branch X] [--date-range 7d] [--tag t1,t2]
claudemem session search "query" [--branch X] [--tag t1,t2]
claudemem session get <id-or-prefix> [--tag t1,t2]
claudemem session update <id> --add-tag t1,t2 --remove-tag t3

# Search everything
claudemem search "query" [--type note|session] [--tag t1,t2] [--limit N]

# Tags
claudemem context inject --tag project-x    # only entries carrying every listed tag
claudemem stats --tag project-x             # the statistics of the tagged entries
claudemem tag apply --file tags.jsonl [--dry-run]   # bulk add/remove, see below

# Several stores
claudemem config set stores.read "~/team-memory"   # reads also return the entries of ~/team-memory
claudemem search "query" --read-stores ""          # this store alone, for one call
claudemem move --to ~/team-memory --file ids.txt [--dry-run]   # move entries there, keeping their ids

# Embedding backend (pick one; no silent fallback)
claudemem setup                              # interactive wizard: Local / Gemini / Voyage / OpenAI / TF-IDF
claudemem health                             # I1-I3 parity check (markdown ↔ FTS ↔ vectors, <100ms)
claudemem health --deep                      # also I4/I5 (orphans, config match)
claudemem repair                             # fix drift detected by health (interactive)
claudemem repair --prune-stale               # also delete vectors left by previously-used backends

# Cross-machine sync (markdown-only via git)
claudemem sync init <remote-url>             # git init ~/.claudemem with remote
claudemem sync push                          # commit + push notes/sessions
claudemem sync pull                          # pull + rebuild FTS + embed missing vectors
claudemem sync status                        # git status + index health

# Utilities
claudemem stats
claudemem verify
claudemem config set/get/list/delete <key> [value]
claudemem export backup.tar.gz
claudemem import backup.tar.gz
```

Add `--format json` to any command for structured output.

### Tags

A tag is one word: a new tag with a space or comma is refused on every write (an older entry
keeps such a tag and stays editable). A `--tag t1,t2` filter keeps the entries that carry every
listed tag, matched as whole tags, ignoring the case of ASCII letters (`--tag team` does not match
`team-old`); `--tag` can be repeated, all the tags are required, and a `--tag` that names no tag
is an error. Tags filter what a command shows; they do not hide entries: on `get`, an entry that
exists without the tags is reported as such, and `stats --tag` is the usual dashboard over the
tagged entries (with `--top-accessed` too), without the storage size, which belongs to the whole
store. Search checks each tagged hit against its file; `stats --tag` and the `context inject`
header come from the index, so run `claudemem reindex` after editing files by hand.

`--add-tag` / `--remove-tag` and `tag apply` change tags only: the tags list in the file's
frontmatter and the entry's index rows (the body, the `updated` time and the vectors stay as
they are; a hand-written frontmatter keeps its values but may be re-spaced). `tag apply` reads
one JSON object per line:

```json
{"id": "<full id>", "kind": "note", "add": ["team"], "remove": ["personal"]}
```

The whole file is validated before anything changes; each entry is then applied atomically
under the store lock, unknown ids are listed (exit 1), and a second run of the same file
changes nothing. An entry whose file already has the requested tags but whose index rows do
not is reported as a stale index row and its rows are rebuilt from the file.

### Several stores

A store can read other stores, for example a private one that also reads a team's: list them,
comma-separated, in its `stores.read` setting (absolute paths or `~/`). Its read commands then
return the entries of all of them, and when more than one store is read each entry names its
store: `search`, `note search|list|get|categories|tags`, `session list|get|search`,
`context inject` and `stats`. Ranked lists are merged by rank (each store ranks against its own
index, so scores from two stores are not compared), date-sorted lists by date, and counts are
added up. Writes go to `--store` alone; a write command given an id that sits only in a store this
one reads says so and names the `--store` to use.

A store read is opened read-only: reading it writes nothing there, not even its access log, and
its own settings are not used. `search` finds its entries by keyword and, when `--store` searches
with an embedding backend other than TF-IDF, also by that store's vectors of the same backend,
model and dimensions, matched against the query as `--store` embedded it (once for all the
stores). A store read never chooses where a query or a key is sent.

`--read-stores` replaces the setting for one call, and `--read-stores ""` reads `--store` alone.
Only the setting of `--store` counts (the stores it names are not followed further), a store
listed twice or reached by a symlink is read once, and with a single store the output is what it
always was. A listed path that is not absolute, does not exist, is not a claudemem store (it has
no `.index/search.db`), or sits inside `--store` or holds it, and a store that cannot be read, are
skipped with a warning and left as they are; a `stores.read` setting that cannot be read is a
warning too, and `--store` is read alone. In `--read-stores`, typed for one call, a path that is
not absolute or overlaps `--store` is an error. Ids are uuids, so an
entry lives in one store; if a copy sits in two, `get` shows the one met first (`--store`, then
the listed order) and warns. The other commands (`graph`, `health`, `sync`, `export`,
`reindex`, ...) work on `--store` alone.

`claudemem move --to <store> --file <ids>` moves the listed notes and sessions from `--store` to
another existing store, keeping each entry's id and its file (under the same name, or the next free
`-N` name if another file holds it there). The ids file has one full id per line; blank lines and
anything after a `#` are skipped, and the whole file is checked before anything moves. `--dry-run`
opens both stores read-only and changes nothing in either.

The target's index rows are built from the file, as `reindex` builds them. No vectors go along, and
the target's own vectors for a moved entry are removed: a store's vectors can be older than its file
(a `reindex`, a `sync pull` or a tag edit rebuilds the search row and keeps them), and stale ones
would let a search find words the file no longer has. The report counts every entry that went into
the target, and on a rerun every listed entry an earlier run put there that still has no vectors,
and prints the command that embeds them there, `claudemem --store <target> reindex --vectors
--missing`. The access log is not carried over: it records what a person read in this store. The
entry's access rows here are removed with it, so its read counts start over in the target (a read of
it here while it moves can still log a row after it left; `stats --top-accessed` then lists that row
as deleted).

Each entry moves on its own under both stores' write locks: its file goes into the target first,
synced and never over another file (a copy already there, from an earlier run or a sync, is synced
before this store's copy goes), then its index rows (committed with a full sync), and only then does
the entry leave this store. An editor or an add-only sync takes no claudemem lock, so before a file
of this store goes, the move sets it aside under a hidden name (`.<name>.moving`) and compares it
with what went into the target: a file saved meanwhile goes back under its name, the target keeps
the version the move read, and the entry is left to clean up. A different copy of a moved file that
arrives in either store during the run (a sync pull in a long move) is found by a second walk at the
end, and its entry is left to clean up.

Every other writer of a store's files or index takes the same lock, so none runs in the middle of an
entry: note and session writes, `reindex`, `repair`, `import`, `session repair --apply` and the git
steps of `sync` that change files. A vector backfill or rebuild embeds without the lock and writes
under it, leaving out the entries that left the store meanwhile and keeping the vectors another
writer added meanwhile.

A run that stops half way leaves every entry whole in one store or both, and running the same file
again finishes it, also from a file left in the target without rows. The target keeps a journal of
the files a move is putting into it (`.index/move-pending`): the next move into that store removes
what a stopped run wrote there and never committed, its temp file and the entry's file while the
target's index does not list it there, as long as they still hold what the move wrote and the store
the entry came from (named in the journal) still holds a file of it; a rerun is then not refused
over its own leftover, and a note that turns out to belong elsewhere leaves no copy in the target.
While that store no longer holds the entry, what the stopped run left in the target may be its only
copy: it stays, with a warning on each run, until the target is reindexed or the file removed by
hand. A set-aside file that a stopped run left is found and handled by the next run.

Two different files under one id (anywhere in the target), a file reached through a symlink or
another alias between the two stores (a bind mount: one file in one directory, reached by two
paths), a stale row in the target, stores that overlap (also through a bind mount) and an index
written by a newer claudemem are refused; a hard link in another directory is allowed, since
removing one name leaves the other.

Notes move before sessions, and a session moves only when every note it lists under its Related
Notes sections is in the target or moves in the same run, since a session file names its related
notes by title. A Related Notes section is one under any heading with that title, at any level, in
any case and with or without a colon, and every id in backticks on a line there counts; a line
without a note id, a first ref in backticks that is no note id, an id shorter than 8 characters or
one that matches several notes keeps the session where it is. A note counts as in a store when its
index lists it or one of its note files holds it, and a note this run refused stays here with its
sessions; a note that neither store holds is only a warning, since the link is dead in both.

Missing and refused entries, and entries left to clean up (in the target, with a different copy of
the file, or rows the move could not remove, still in this store), make the exit code 1. With an
add-only sync between machines, a moved file can come back from another machine's copy: move there
too, or run the move again after the next pull, which removes a copy that came back when it is the
same file.

Before a migration, run `claudemem --store <store> reindex` and then `claudemem --store <store>
health` on the store the entries leave, so that its index matches its files: the move goes by the
index for what a store holds, and refuses an entry, or keeps a session, where the two disagree.

## Search Backends

Semantic search needs an embedding model. You pick it explicitly — claudemem
never falls back to a worse backend behind your back.

```bash
claudemem setup
```

| Option | Where it runs | Cost | Multilingual | When to pick |
|---|---|---|---|---|
| **Local — Ollama** | Your machine | Free | qwen3 strong / nomic weaker | Daily use, offline, airgapped |
| **Cloud — Gemini** | Google | ~$0.15/M tokens | 100+ langs | Best quality, you already have a key |
| **Cloud — Voyage** | Voyage AI | ~$0.02/M, 200M free | Yes | Budget pick, effectively free |
| **Cloud — OpenAI** | OpenAI | ~$0.02/M (3-small) | English-heavy | You already pay OpenAI |
| **TF-IDF** | Your machine | Free | OK | No daemon, no keys, keyword-ish similarity |

API keys are read from environment variables (`GEMINI_API_KEY`,
`VOYAGE_API_KEY`, `OPENAI_API_KEY`) — claudemem refuses to store them in
`config.json`. Only the env var **name** is recorded, so configs are safe to
commit and sync across machines.

**When the backend is down**, claudemem never degrades silently:

- Non-interactive shells / CI → error with recovery instructions + exit 1.
- Interactive terminals → prompt offering retry / FTS-only-this-query / run setup.

Use `claudemem search "..." --fts-only` to skip semantic for one query.

## Cross-Machine Memory

Memory can follow you between machines via a private git repo. Only Markdown
travels over the wire — the SQLite index and config stay per-machine, so a
cloud-Gemini laptop and a local-Ollama workstation share one corpus with zero
backend coupling.

```bash
# once, per user (HTTPS works with gh auth / keychain)
claudemem sync init https://github.com/YOU/claudemem-memory.git

# after work
claudemem sync push

# on another machine, first time
claudemem sync init https://github.com/YOU/claudemem-memory.git
claudemem sync pull
```

See [docs/HOOK_INTEGRATION.md](docs/HOOK_INTEGRATION.md) for auto-pull on
SessionStart and auto-push on SessionEnd via Claude Code hooks.

## Quality Signals

Three test layers, all green. Numbers below are reproducible from a clean
checkout:

| Layer | Command | Result |
|-------|---------|--------|
| Go unit + coverage | `go test ./... -cover` | all 7 packages pass · `pkg/models` 94.7% · `pkg/storage` 81.4% · `pkg/hooks` 79.2% |
| E2E CLI | `make e2e-test` | 23 passed, 0 failed |
| Black-box feature | `make feature-test` | 82 passed across 7 levels |
| Everything | `make test-all` | unit + smoke + E2E + feature |

The full matrix (`make test-all`) runs in GitHub Actions for pull requests that
change Go source, modules, the Makefile, CI workflow, or test harnesses (see
`.github/workflows/ci.yml`). There are 364 Go test functions across `cmd/` and
`pkg/`; all suites use temp directories, so there are zero local-environment
dependencies.

## Security

skills.sh shows "High Risk" / "Critical Risk" badges — this is normal for **any
skill that runs CLI commands**. What is actually happening:

| Scanner | Flag | Why | Real risk |
|---------|------|-----|-----------|
| Generic | High | Skill uses Bash to run `claudemem` | Every useful CLI skill needs this |
| Socket | 1 alert | `install.sh` fetches a binary via curl | Standard Go distribution |
| Snyk | Critical | `modernc.org/sqlite` (C-to-Go transpile) carries upstream CVEs | Industry-standard pure-Go SQLite |

What claudemem actually does: zero network by default (TF-IDF or offline
Ollama); cloud embedding backends are opt-in per machine via `claudemem setup`,
with API keys from env vars only. Parameterized SQL, FTS5 query sanitization,
path-traversal protection, `0600`/`0700` storage permissions. About 13,000 lines
of Go source (plus ~9,000 lines of tests), fully auditable. Running
`govulncheck ./...` before a release is currently a recommended manual check,
not an automated release gate. See [SECURITY.md](SECURITY.md).

## Build from Source

```bash
git clone https://github.com/zelinewang/claudemem.git
cd claudemem
make build          # single static binary (CGO_ENABLED=0, pure Go)
make install        # install to ~/.local/bin/
./claudemem --version
```

Requires Go 1.25+. The build is a single pure-Go binary with no runtime
dependencies; goreleaser (`.goreleaser.yml`) cross-compiles linux/darwin/windows
on amd64/arm64 for releases.

## Status

Stable and in daily use; latest release
[v3.0.12](https://github.com/zelinewang/claudemem/releases). The storage format
is backward-compatible across upgrades — schema migrations preserve existing
notes and sessions. Issues and PRs welcome.

## License

[MIT](LICENSE) © Zane Wang

## References

- [braindump](https://github.com/MohGanji/braindump) — Go-based persistent notes for AI agents
- [claude-done](https://github.com/Genuifx/claude-done) — session-summary saving for Claude Code
