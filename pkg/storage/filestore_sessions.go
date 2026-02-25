package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zelinewang/claudemem/pkg/models"
)

// SaveSessionResult describes what happened when saving a session
type SaveSessionResult struct {
	Action    string `json:"action"`     // "created" or "updated"
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Date      string `json:"date"`
}

// SaveSession saves a session to the filesystem and database.
// Dedup: same date + same project + same branch → update existing session instead of duplicating.
func (fs *FileStore) SaveSession(session *models.Session) (*SaveSessionResult, error) {
	if err := validateTitle(session.Title); err != nil {
		return nil, fmt.Errorf("invalid session title: %w", err)
	}

	// ── Dedup check: same date + project + branch → update existing ──
	var existingID, existingFpath string
	err := fs.db.QueryRow(`
		SELECT id, filepath FROM entries
		WHERE type = 'session' AND date_str = ? AND project = ? AND branch = ?
		ORDER BY created DESC LIMIT 1
	`, session.Date, session.Project, session.Branch).Scan(&existingID, &existingFpath)

	if err == nil && existingID != "" {
		// Existing session found — MERGE content (not overwrite) to prevent data loss.
		// Different conversations on the same day/branch can't see each other's context,
		// so a later /wrapup is NOT a superset of an earlier one.
		oldFullPath := filepath.Join(fs.baseDir, existingFpath)

		// Read existing session to merge with
		existing, readErr := fs.readSessionFile(oldFullPath)
		if readErr == nil {
			// Use existing ID to keep continuity
			session.ID = existingID

			// Merge Summary: always append with timestamp separator when both non-empty.
			// Even identical summaries get a separator — the timestamp itself is valuable
			// as it records that a second wrap-up happened at this time.
			if existing.Summary != "" && session.Summary != "" {
				separator := fmt.Sprintf("\n\n--- Updated %s ---\n", time.Now().Format("2006-01-02 15:04"))
				session.Summary = existing.Summary + separator + session.Summary
			} else if session.Summary == "" {
				session.Summary = existing.Summary
			}

			// Merge WhatHappened: always append with separator when both non-empty
			if existing.WhatHappened != "" && session.WhatHappened != "" {
				separator := fmt.Sprintf("\n\n--- Updated %s ---\n", time.Now().Format("2006-01-02 15:04"))
				session.WhatHappened = existing.WhatHappened + separator + session.WhatHappened
			} else if session.WhatHappened == "" {
				session.WhatHappened = existing.WhatHappened
			}

			// Merge list fields: deduplicate by content
			session.Decisions = mergeStringSlice(existing.Decisions, session.Decisions)
			session.Insights = mergeStringSlice(existing.Insights, session.Insights)
			session.Questions = mergeStringSlice(existing.Questions, session.Questions)
			session.NextSteps = mergeStringSlice(existing.NextSteps, session.NextSteps)

			// Merge Changes: deduplicate by path
			session.Changes = mergeFileChanges(existing.Changes, session.Changes)

			// Merge Problems: deduplicate by problem text
			session.Problems = mergeProblems(existing.Problems, session.Problems)

			// Merge RelatedNotes: deduplicate by ID
			session.RelatedNotes = mergeRelatedNotes(existing.RelatedNotes, session.RelatedNotes)

			// Merge ExtraSections: deduplicate by name, append content for same name
			session.ExtraSections = mergeExtraSections(existing.ExtraSections, session.ExtraSections)

			// Merge Tags
			session.Tags = mergeTags(session.Tags, existing.Tags)

			// Preserve original title if new one is empty
			if session.Title == "" {
				session.Title = existing.Title
			}
		} else {
			// Can't read existing — fall through to overwrite
			session.ID = existingID
		}

		// Write merged content
		markdown := FormatSessionMarkdown(session)
		if err := os.WriteFile(oldFullPath, []byte(markdown), 0600); err != nil {
			return nil, fmt.Errorf("failed to update session file: %w", err)
		}

		// Update DB entries
		fs.db.Exec(`UPDATE entries SET title = ?, tags = ?, updated = ? WHERE id = ?`,
			session.Title, strings.Join(session.Tags, " "),
			time.Now().Format("2006-01-02T15:04:05Z"), existingID)

		// Update FTS
		fs.db.Exec(`DELETE FROM memory_fts WHERE id = ?`, existingID)
		fs.db.Exec(`INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`,
			existingID, session.Title, session.GetSearchableContent(),
			strings.Join(session.Tags, " "))

		return &SaveSessionResult{
			Action:    "updated",
			SessionID: existingID,
			Title:     session.Title,
			Date:      session.Date,
		}, nil
	}

	// ── Normal create path ──
	branch := strings.TrimSuffix(Slugify(session.Branch), ".md")
	idPrefix := session.ID
	if len(idPrefix) > 8 {
		idPrefix = idPrefix[:8]
	}
	filename := fmt.Sprintf("%s_%s_%s_%s",
		session.Date,
		branch,
		idPrefix,
		strings.TrimSuffix(Slugify(session.Title), ".md"))
	filename = filename + ".md"

	markdown := FormatSessionMarkdown(session)

	fullPath := filepath.Join(fs.sessionsDir, filename)
	if err := os.WriteFile(fullPath, []byte(markdown), 0600); err != nil {
		return nil, fmt.Errorf("failed to write session file: %w", err)
	}

	relPath := filepath.Join("sessions", filename)

	_, err = fs.db.Exec(`
		INSERT INTO entries (id, type, title, branch, project, session_id, date_str, tags, filepath, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, "session", session.Title, session.Branch, session.Project,
		session.SessionID, session.Date, strings.Join(session.Tags, " "),
		relPath, session.Created.Format("2006-01-02T15:04:05Z"),
		session.Created.Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		os.Remove(fullPath)
		return nil, fmt.Errorf("failed to insert session: %w", err)
	}

	_, err = fs.db.Exec(`
		INSERT INTO memory_fts (id, title, content, tags) VALUES (?, ?, ?, ?)`,
		session.ID, session.Title, session.GetSearchableContent(),
		strings.Join(session.Tags, " "),
	)
	if err != nil {
		os.Remove(fullPath)
		fs.db.Exec("DELETE FROM entries WHERE id = ?", session.ID)
		return nil, fmt.Errorf("failed to insert into FTS: %w", err)
	}

	return &SaveSessionResult{
		Action:    "created",
		SessionID: session.ID,
		Title:     session.Title,
		Date:      session.Date,
	}, nil
}

// GetSession retrieves a session by ID or ID prefix
func (fs *FileStore) GetSession(id string) (*models.Session, error) {
	// Try exact match first, then prefix match
	var fpath string
	err := fs.db.QueryRow(`
		SELECT filepath FROM entries
		WHERE (id = ? OR id LIKE ? || '%') AND type = 'session'
		LIMIT 1`,
		id, id,
	).Scan(&fpath)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query session: %w", err)
	}

	// Read file (filepath is relative, need to join with base dir)
	fullPath := filepath.Join(fs.baseDir, fpath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}

	// Parse markdown
	session, err := ParseSessionMarkdown(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse session markdown: %w", err)
	}

	return session, nil
}

// ListSessions lists sessions with optional filters
func (fs *FileStore) ListSessions(opts SessionListOpts) ([]*models.Session, error) {
	// Build query
	query := "SELECT filepath FROM entries WHERE type = 'session'"
	args := []interface{}{}

	// Add filters
	if opts.Branch != "" {
		query += " AND branch LIKE ?"
		args = append(args, "%"+opts.Branch+"%")
	}
	if opts.Project != "" {
		query += " AND project = ?"
		args = append(args, opts.Project)
	}
	if opts.StartDate != "" && opts.EndDate != "" {
		query += " AND date_str BETWEEN ? AND ?"
		args = append(args, opts.StartDate, opts.EndDate)
	} else if opts.StartDate != "" {
		query += " AND date_str >= ?"
		args = append(args, opts.StartDate)
	} else if opts.EndDate != "" {
		query += " AND date_str <= ?"
		args = append(args, opts.EndDate)
	}

	// Order and limit
	query += " ORDER BY date_str DESC, created DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	} else {
		query += " LIMIT 10" // Default limit
	}

	// Execute query
	rows, err := fs.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	// Read sessions
	var sessions []*models.Session
	for rows.Next() {
		var fpath string
		if err := rows.Scan(&fpath); err != nil {
			continue
		}

		// Read and parse file (fpath is relative, join with baseDir)
		fullPath := filepath.Join(fs.baseDir, fpath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		session, err := ParseSessionMarkdown(data)
		if err != nil {
			continue
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// SearchSessions searches sessions by full-text query
func (fs *FileStore) SearchSessions(query string, opts SessionListOpts) ([]*models.Session, error) {
	// Build FTS query
	ftsQuery := `
		SELECT f.id, f.rank, e.filepath
		FROM memory_fts f
		JOIN entries e ON f.id = e.id
		WHERE memory_fts MATCH ? AND e.type = 'session'`

	args := []interface{}{query}

	// Add optional filters
	if opts.Branch != "" {
		ftsQuery += " AND e.branch LIKE ?"
		args = append(args, "%"+opts.Branch+"%")
	}
	if opts.StartDate != "" && opts.EndDate != "" {
		ftsQuery += " AND e.date_str BETWEEN ? AND ?"
		args = append(args, opts.StartDate, opts.EndDate)
	} else if opts.StartDate != "" {
		ftsQuery += " AND e.date_str >= ?"
		args = append(args, opts.StartDate)
	} else if opts.EndDate != "" {
		ftsQuery += " AND e.date_str <= ?"
		args = append(args, opts.EndDate)
	}

	// Order by relevance and limit
	ftsQuery += " ORDER BY f.rank LIMIT 50"

	// Execute query
	rows, err := fs.db.Query(ftsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search sessions: %w", err)
	}
	defer rows.Close()

	// Read sessions
	var sessions []*models.Session
	for rows.Next() {
		var id string
		var rank float64
		var fpath string

		if err := rows.Scan(&id, &rank, &fpath); err != nil {
			continue
		}

		// Read and parse file (fpath is relative, join with baseDir)
		fullPath := filepath.Join(fs.baseDir, fpath)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		session, err := ParseSessionMarkdown(data)
		if err != nil {
			continue
		}

		sessions = append(sessions, session)
	}

	return sessions, nil
}

// readSessionFile reads and parses a session markdown file
func (fs *FileStore) readSessionFile(fullPath string) (*models.Session, error) {
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read session file: %w", err)
	}
	return ParseSessionMarkdown(data)
}

// mergeStringSlice combines two string slices, deduplicating by exact content
func mergeStringSlice(existing, new []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range existing {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	for _, s := range new {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// mergeFileChanges combines file change lists, merging descriptions for same paths
func mergeFileChanges(existing, new []models.FileChange) []models.FileChange {
	seen := make(map[string]int) // path → index in result
	var result []models.FileChange
	for _, c := range existing {
		if _, exists := seen[c.Path]; !exists {
			seen[c.Path] = len(result)
			result = append(result, c)
		}
	}
	for _, c := range new {
		if idx, exists := seen[c.Path]; exists {
			// Same path — append description if different
			if result[idx].Description != c.Description {
				result[idx].Description = result[idx].Description + "; " + c.Description
			}
		} else {
			seen[c.Path] = len(result)
			result = append(result, c)
		}
	}
	return result
}

// mergeProblems combines problem/solution lists, deduplicating by problem text
func mergeProblems(existing, new []models.ProblemSolution) []models.ProblemSolution {
	seen := make(map[string]bool)
	var result []models.ProblemSolution
	for _, p := range existing {
		if !seen[p.Problem] {
			seen[p.Problem] = true
			result = append(result, p)
		}
	}
	for _, p := range new {
		if !seen[p.Problem] {
			seen[p.Problem] = true
			result = append(result, p)
		}
	}
	return result
}

// mergeRelatedNotes combines related note lists, deduplicating by ID
func mergeRelatedNotes(existing, new []models.RelatedNote) []models.RelatedNote {
	seen := make(map[string]bool)
	var result []models.RelatedNote
	for _, rn := range existing {
		if !seen[rn.ID] {
			seen[rn.ID] = true
			result = append(result, rn)
		}
	}
	for _, rn := range new {
		if !seen[rn.ID] {
			seen[rn.ID] = true
			result = append(result, rn)
		}
	}
	return result
}

// mergeExtraSections combines custom section lists, appending content for same-named sections
func mergeExtraSections(existing, new []models.ExtraSection) []models.ExtraSection {
	seen := make(map[string]int) // name → index in result
	var result []models.ExtraSection
	for _, es := range existing {
		lowerName := strings.ToLower(es.Name)
		if _, exists := seen[lowerName]; !exists {
			seen[lowerName] = len(result)
			result = append(result, es)
		}
	}
	for _, es := range new {
		lowerName := strings.ToLower(es.Name)
		if idx, exists := seen[lowerName]; exists {
			// Same section name — append content if different
			if result[idx].Content != es.Content {
				separator := fmt.Sprintf("\n\n--- Updated %s ---\n", time.Now().Format("2006-01-02 15:04"))
				result[idx].Content = result[idx].Content + separator + es.Content
			}
		} else {
			seen[lowerName] = len(result)
			result = append(result, es)
		}
	}
	return result
}

// Helper to parse date range strings like "7d" or "today"
func parseDateRange(s string) (string, string, error) {
	now := time.Now()
	today := now.Format("2006-01-02")

	switch s {
	case "today":
		return today, today, nil
	case "yesterday":
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
		return yesterday, yesterday, nil
	case "week", "7d":
		weekAgo := now.AddDate(0, 0, -7).Format("2006-01-02")
		return weekAgo, today, nil
	case "month", "30d":
		monthAgo := now.AddDate(0, 0, -30).Format("2006-01-02")
		return monthAgo, today, nil
	default:
		// Check if it's a number followed by 'd' (days)
		if strings.HasSuffix(s, "d") {
			var days int
			if _, err := fmt.Sscanf(s, "%dd", &days); err == nil {
				start := now.AddDate(0, 0, -days).Format("2006-01-02")
				return start, today, nil
			}
		}
		return "", "", fmt.Errorf("invalid date range: %s", s)
	}
}