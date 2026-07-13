# Contributing to claudemem

Thanks for your interest in improving claudemem. It's a single, zero-network Go
binary, so the local loop is fast.

## Development setup

Requires Go (see `go.mod` for the version). No CGO, no system libraries.

```bash
git clone https://github.com/zelinewang/claudemem.git
cd claudemem
make build        # builds a static ./claudemem binary
```

## Tests

```bash
make test-all     # unit + smoke + e2e + feature suites (what CI runs)
```

Smaller loops while iterating:

- `make test` — quick smoke test
- `go test ./... -count=1` — unit tests only
- `make e2e-test` / `make feature-test` — CLI black-box suites

CI runs `make test-all` on every pull request; please make sure it passes
locally first.

## Pull requests

- Branch off `master`; open one focused PR per change.
- Add or update tests for behavior changes — the suites under `tests/` and
  `e2e_test.sh` are the patterns to follow.
- Write conventional-ish commit subjects (`fix:`, `feat:`, `docs:`…). PRs are
  squash-merged, so the PR title becomes the commit message.
- Markdown is the source of truth; the SQLite index is a regenerable cache —
  don't commit a store or index.

## License

By contributing, you agree that your contributions are licensed under the
project's [MIT License](LICENSE).
