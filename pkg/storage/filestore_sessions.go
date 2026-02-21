package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zanelabz/claudemem/pkg/models"
)

// SaveSession saves a session to the filesystem and database
func (fs *FileStore) SaveSession(session *models.Session) error {
	// Generate filename: {date}_{branch}_{id[:8]}_{Slugify(title)}
	branch := strings.ReplaceAll(session.Branch, "/", "-")
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

	// Format markdown
	markdown := FormatSessionMarkdown(session)

	// Write file
	filepath := filepath.Join(fs.sessionsDir, filename)
	if err := os.WriteFile(filepath, []byte(markdown), 0644); err != nil {
		return fmt.Errorf("failed to write session file: %w", err)
	}

	// Insert into entries table
	_, err := fs.db.Exec(`
		INSERT INTO entries (id, type, title, branch, project, session_id, date_str, tags, filepath, created, updated)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID,
		"session",
		session.Title,
		session.Branch,
		session.Project,
		session.SessionID,
		session.Date,
		strings.Join(session.Tags, " "),
		filepath,
		session.Created.Format("2006-01-02T15:04:05Z"),
		session.Created.Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		// Try to clean up the file
		os.Remove(filepath)
		return fmt.Errorf("failed to insert session into database: %w", err)
	}

	// Insert into FTS table
	_, err = fs.db.Exec(`
		INSERT INTO memory_fts (id, title, content, tags)
		VALUES (?, ?, ?, ?)`,
		session.ID,
		session.Title,
		session.GetSearchableContent(),
		strings.Join(session.Tags, " "),
	)
	if err != nil {
		// Try to clean up both the file and database entry
		os.Remove(filepath)
		fs.db.Exec("DELETE FROM entries WHERE id = ?", session.ID)
		return fmt.Errorf("failed to insert session into FTS index: %w", err)
	}

	return nil
}

// GetSession retrieves a session by ID or ID prefix
func (fs *FileStore) GetSession(id string) (*models.Session, error) {
	// Try exact match first, then prefix match
	var filepath string
	err := fs.db.QueryRow(`
		SELECT filepath FROM entries
		WHERE (id = ? OR id LIKE ? || '%') AND type = 'session'
		LIMIT 1`,
		id, id,
	).Scan(&filepath)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query session: %w", err)
	}

	// Read file
	data, err := os.ReadFile(filepath)
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
		var filepath string
		if err := rows.Scan(&filepath); err != nil {
			continue
		}

		// Read and parse file
		data, err := os.ReadFile(filepath)
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
		var filepath string

		if err := rows.Scan(&id, &rank, &filepath); err != nil {
			continue
		}

		// Read and parse file
		data, err := os.ReadFile(filepath)
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