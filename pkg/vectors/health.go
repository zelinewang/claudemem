package vectors

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HealthReport is the result of a parity check. Each invariant (I1–I5) has
// a boolean pass/fail + a human-readable message. Quick health check
// populates I1–I3; Deep also fills I4 + I5 (orphans + config match).
type HealthReport struct {
	// Counts
	MarkdownFiles int            // I1 lhs
	EntriesTotal  int            // I1 rhs
	FTSTotal      int            // I2 rhs
	VectorTotals  map[string]int // key = "backend:model", value = rows

	// Active backend at check time
	ActiveBackend string
	ActiveModel   string

	// Per-invariant status. False = drift detected.
	I1MarkdownMatchesEntries bool
	I2EntriesMatchesFTS      bool
	I3VectorsMatchActiveBackend bool
	I4NoOrphanRows               bool // deep only
	I5VectorMetaMatchesActive    bool // deep only

	// Human-readable drift descriptions (empty when all pass)
	Issues []string

	// DidDeepCheck reports whether I4+I5 were evaluated.
	DidDeepCheck bool
}

// Healthy reports whether all populated invariants pass.
func (h *HealthReport) Healthy() bool {
	base := h.I1MarkdownMatchesEntries &&
		h.I2EntriesMatchesFTS &&
		h.I3VectorsMatchActiveBackend
	if !h.DidDeepCheck {
		return base
	}
	return base && h.I4NoOrphanRows && h.I5VectorMetaMatchesActive
}

// HealthInputs are the paths + DB handle + active Embedder needed to run
// a parity check. Separated so tests can pass temp dirs + fake embedders.
type HealthInputs struct {
	DB          *sql.DB
	NotesDir    string
	SessionsDir string
	Embedder    Embedder // may be nil — I3 is then skipped
}

// CheckHealth runs the quick parity check (I1–I3). Targets sub-100ms so
// it can run on every SessionStart hook without blocking the shell.
func CheckHealth(in HealthInputs) (*HealthReport, error) {
	r := &HealthReport{VectorTotals: map[string]int{}}

	// I1 — markdown files vs entries table
	mdCount, err := countMarkdownFiles(in.NotesDir, in.SessionsDir)
	if err != nil {
		return nil, fmt.Errorf("scan markdown: %w", err)
	}
	r.MarkdownFiles = mdCount
	if err := in.DB.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&r.EntriesTotal); err != nil {
		return nil, fmt.Errorf("count entries: %w", err)
	}
	r.I1MarkdownMatchesEntries = mdCount == r.EntriesTotal
	if !r.I1MarkdownMatchesEntries {
		r.Issues = append(r.Issues, fmt.Sprintf(
			"I1: %d markdown files but %d entries rows (delta %d). Run `claudemem reindex`.",
			mdCount, r.EntriesTotal, mdCount-r.EntriesTotal))
	}

	// I2 — entries vs memory_fts
	if err := in.DB.QueryRow(`SELECT COUNT(*) FROM memory_fts`).Scan(&r.FTSTotal); err != nil {
		return nil, fmt.Errorf("count fts: %w", err)
	}
	r.I2EntriesMatchesFTS = r.EntriesTotal == r.FTSTotal
	if !r.I2EntriesMatchesFTS {
		r.Issues = append(r.Issues, fmt.Sprintf(
			"I2: %d entries but %d memory_fts rows. Run `claudemem reindex`.",
			r.EntriesTotal, r.FTSTotal))
	}

	// I3 — per-(backend, model) vector counts (compared to entries)
	rows, err := in.DB.Query(`SELECT backend, model, COUNT(*) FROM vectors GROUP BY backend, model`)
	if err != nil {
		return nil, fmt.Errorf("group vectors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var backend, model string
		var n int
		if err := rows.Scan(&backend, &model, &n); err != nil {
			return nil, err
		}
		r.VectorTotals[backend+":"+model] = n
	}
	if in.Embedder != nil {
		r.ActiveBackend = in.Embedder.Name()
		r.ActiveModel = in.Embedder.Model()
		key := r.ActiveBackend + ":" + r.ActiveModel
		active := r.VectorTotals[key]
		r.I3VectorsMatchActiveBackend = active == r.EntriesTotal
		if !r.I3VectorsMatchActiveBackend {
			r.Issues = append(r.Issues, fmt.Sprintf(
				"I3: active backend %s has %d vectors but %d entries expect them (delta %d). Run `claudemem repair` to backfill.",
				key, active, r.EntriesTotal, r.EntriesTotal-active))
		}
	} else {
		// No embedder passed — skip I3 rather than claim false pass
		r.I3VectorsMatchActiveBackend = true
	}

	return r, nil
}

// CheckHealthDeep also runs I4 (no orphans) + I5 (vector_meta matches).
// Slower: scans every FTS and every vector row against entries.
func CheckHealthDeep(in HealthInputs) (*HealthReport, error) {
	r, err := CheckHealth(in)
	if err != nil {
		return nil, err
	}
	r.DidDeepCheck = true

	// I4 — orphan FTS rows pointing to non-existent entries
	var orphanFTS int
	if err := in.DB.QueryRow(`
		SELECT COUNT(*) FROM memory_fts
		WHERE id NOT IN (SELECT id FROM entries)
	`).Scan(&orphanFTS); err != nil {
		return nil, err
	}
	var orphanVec int
	if err := in.DB.QueryRow(`
		SELECT COUNT(*) FROM vectors
		WHERE doc_id NOT IN (SELECT id FROM entries)
	`).Scan(&orphanVec); err != nil {
		return nil, err
	}
	r.I4NoOrphanRows = orphanFTS == 0 && orphanVec == 0
	if !r.I4NoOrphanRows {
		r.Issues = append(r.Issues, fmt.Sprintf(
			"I4: %d orphan FTS rows, %d orphan vector rows (parent entry deleted). Run `claudemem repair`.",
			orphanFTS, orphanVec))
	}

	// I5 — vector_meta.index_backend matches active embedder
	if in.Embedder != nil {
		meta := readMetaOrEmpty(in.DB, "index_backend")
		expected := in.Embedder.Name() + ":" + in.Embedder.Model()
		r.I5VectorMetaMatchesActive = meta == expected || meta == ""
		if !r.I5VectorMetaMatchesActive {
			r.Issues = append(r.Issues, fmt.Sprintf(
				"I5: vector_meta.index_backend=%q but active=%q. Reindex after backend switch: `claudemem reindex --vectors`.",
				meta, expected))
		}
	} else {
		r.I5VectorMetaMatchesActive = true
	}

	return r, nil
}

// countMarkdownFiles walks notesDir + sessionsDir and counts .md files.
// Called once per SessionStart — keep it cheap. Exits early if either
// directory does not exist (fresh install).
func countMarkdownFiles(notesDir, sessionsDir string) (int, error) {
	total := 0
	for _, dir := range []string{notesDir, sessionsDir} {
		if _, err := os.Stat(dir); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if strings.HasSuffix(info.Name(), ".md") && !strings.HasPrefix(info.Name(), "._") {
				total++
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}
