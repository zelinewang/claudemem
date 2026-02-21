package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Search implements full-text search across notes and sessions
func (fs *FileStore) Search(query, entryType string, limit int) ([]SearchResult, error) {
	if fs.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	if limit <= 0 {
		limit = 20
	}

	// Build the query
	var args []interface{}
	sqlQuery := `
		SELECT
			e.id, e.type, e.title, e.category, e.tags, e.created,
			e.date_str, e.branch, e.project,
			f.rank,
			substr(f.content, 1, 200) as preview
		FROM memory_fts f
		JOIN entries e ON f.id = e.id
		WHERE memory_fts MATCH ?`
	args = append(args, query)

	if entryType != "" {
		sqlQuery += " AND e.type = ?"
		args = append(args, entryType)
	}

	sqlQuery += " ORDER BY f.rank LIMIT ?"
	args = append(args, limit)

	rows, err := fs.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var tagsStr sql.NullString
		var category, date, branch, project sql.NullString
		var createdStr string
		var rank float64
		var preview string

		err := rows.Scan(
			&r.ID, &r.Type, &r.Title, &category, &tagsStr, &createdStr,
			&date, &branch, &project,
			&rank, &preview,
		)
		if err != nil {
			return nil, fmt.Errorf("scan row failed: %w", err)
		}

		// Set optional fields
		if category.Valid {
			r.Category = category.String
		}
		if date.Valid {
			r.Date = date.String
		}
		if branch.Valid {
			r.Branch = branch.String
		}
		if project.Valid {
			r.Project = project.String
		}

		// Parse created timestamp (could be Unix int or ISO string)
		r.Created, _ = parseCreatedTimestamp(createdStr)

		// Parse tags
		if tagsStr.Valid && tagsStr.String != "" {
			r.Tags = strings.Fields(tagsStr.String)
		} else {
			r.Tags = []string{}
		}

		// FTS5 returns negative rank scores (more negative = better match)
		// Convert to positive score where higher is better
		r.Score = -rank

		// Clean up preview
		r.Preview = strings.TrimSpace(preview)
		if len(r.Preview) > 100 {
			r.Preview = r.Preview[:100] + "..."
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return results, nil
}

// parseCreatedTimestamp handles both Unix timestamp strings and ISO format strings
func parseCreatedTimestamp(s string) (time.Time, error) {
	// Try Unix timestamp first
	var unix int64
	if _, err := fmt.Sscanf(s, "%d", &unix); err == nil {
		return time.Unix(unix, 0), nil
	}
	// Try ISO formats
	for _, format := range []string{
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
	} {
		if t, err := time.Parse(format, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", s)
}