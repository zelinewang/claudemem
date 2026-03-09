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

	// Log access for each search hit (non-blocking)
	for _, r := range results {
		fs.LogAccess(r.ID, "search_hit")
	}

	return results, nil
}

// SearchWithOpts implements advanced search with faceted filters and recency boost
func (fs *FileStore) SearchWithOpts(opts SearchOpts) ([]SearchResult, error) {
	if fs.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}

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
	args = append(args, opts.Query)

	if opts.Type != "" {
		sqlQuery += " AND e.type = ?"
		args = append(args, opts.Type)
	}

	if opts.Category != "" {
		sqlQuery += " AND e.category = ?"
		args = append(args, opts.Category)
	}

	if len(opts.Tags) > 0 {
		for _, tag := range opts.Tags {
			sqlQuery += " AND e.tags LIKE ?"
			args = append(args, "%"+tag+"%")
		}
	}

	if opts.After != "" {
		sqlQuery += " AND e.date_str >= ?"
		args = append(args, opts.After)
	}

	if opts.Before != "" {
		sqlQuery += " AND e.date_str <= ?"
		args = append(args, opts.Before)
	}

	// Fetch more than limit so recency reranking has headroom
	fetchLimit := limit * 2
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	sqlQuery += " ORDER BY f.rank LIMIT ?"
	args = append(args, fetchLimit)

	rows, err := fs.db.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("search query failed: %w", err)
	}
	defer rows.Close()

	now := time.Now()
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

		r.Created, _ = parseCreatedTimestamp(createdStr)

		if tagsStr.Valid && tagsStr.String != "" {
			r.Tags = strings.Fields(tagsStr.String)
		} else {
			r.Tags = []string{}
		}

		// Base score from FTS5 (negative rank → positive)
		ftsScore := -rank

		// Recency boost: entries from the last 7 days get up to 20% boost,
		// decaying linearly over 30 days to 0%
		daysSinceCreated := now.Sub(r.Created).Hours() / 24
		recencyBoost := 0.0
		if daysSinceCreated < 7 {
			recencyBoost = 0.20
		} else if daysSinceCreated < 30 {
			recencyBoost = 0.20 * (1.0 - (daysSinceCreated-7)/23.0)
		}
		r.Score = ftsScore * (1.0 + recencyBoost)

		r.Preview = strings.TrimSpace(preview)
		if len(r.Preview) > 100 {
			r.Preview = r.Preview[:100] + "..."
		}

		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	// Sort by chosen strategy
	switch opts.Sort {
	case "date":
		sortResultsByDate(results)
	default:
		// "relevance" — sort by boosted score (descending)
		sortResultsByScore(results)
	}

	// Apply limit after reranking
	if len(results) > limit {
		results = results[:limit]
	}

	// Log access for each search hit (non-blocking)
	for _, r := range results {
		fs.LogAccess(r.ID, "search_hit")
	}

	return results, nil
}

// sortResultsByScore sorts results by score descending (highest first)
func sortResultsByScore(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

// sortResultsByDate sorts results by created date descending (newest first)
func sortResultsByDate(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Created.After(results[j-1].Created); j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
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