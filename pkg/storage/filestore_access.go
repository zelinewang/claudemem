package storage

import (
	"fmt"
	"os"
	"time"
)

// AccessStat holds access count information for an entry
type AccessStat struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	AccessCount  int    `json:"access_count"`
	LastAccessed string `json:"last_accessed"`
}

// LogAccess records an access event for an entry. Non-blocking: errors are
// logged to stderr but never returned to the caller so that access tracking
// does not interfere with normal operations.
func (fs *FileStore) LogAccess(entryID, accessType string) {
	if fs.db == nil {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	_, err := fs.db.Exec(
		`INSERT INTO access_log (entry_id, accessed_at, access_type) VALUES (?, ?, ?)`,
		entryID, now, accessType,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to log access for %s: %v\n", entryID, err)
	}
}

// GetTopAccessed returns the most frequently accessed entries.
func (fs *FileStore) GetTopAccessed(limit int) ([]AccessStat, error) {
	if fs.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT
			a.entry_id,
			COALESCE(e.title, '(deleted)') AS title,
			COALESCE(e.type, 'unknown') AS type,
			COUNT(*) AS access_count,
			MAX(a.accessed_at) AS last_accessed
		FROM access_log a
		LEFT JOIN entries e ON a.entry_id = e.id
		GROUP BY a.entry_id
		ORDER BY access_count DESC
		LIMIT ?`

	rows, err := fs.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("top accessed query failed: %w", err)
	}
	defer rows.Close()

	var results []AccessStat
	for rows.Next() {
		var s AccessStat
		if err := rows.Scan(&s.ID, &s.Title, &s.Type, &s.AccessCount, &s.LastAccessed); err != nil {
			return nil, fmt.Errorf("scan access stat failed: %w", err)
		}
		results = append(results, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	if results == nil {
		results = []AccessStat{}
	}

	return results, nil
}
