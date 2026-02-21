package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Stats returns statistics about the store
func (fs *FileStore) Stats() (*StoreStats, error) {
	if fs.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	stats := &StoreStats{
		Categories:    []CategoryInfo{},
		TopTags:       []TagInfo{},
		RecentEntries: []SearchResult{},
	}

	// Count by type
	typeQuery := `SELECT type, COUNT(*) FROM entries GROUP BY type`
	rows, err := fs.db.Query(typeQuery)
	if err != nil {
		return nil, fmt.Errorf("count by type failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entryType string
		var count int
		if err := rows.Scan(&entryType, &count); err != nil {
			return nil, fmt.Errorf("scan type count failed: %w", err)
		}
		switch entryType {
		case "note":
			stats.TotalNotes = count
		case "session":
			stats.TotalSessions = count
		}
	}

	// Count by category
	catQuery := `SELECT category, COUNT(*) FROM entries WHERE type='note' AND category IS NOT NULL GROUP BY category ORDER BY COUNT(*) DESC`
	rows, err = fs.db.Query(catQuery)
	if err != nil {
		return nil, fmt.Errorf("count by category failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cat CategoryInfo
		if err := rows.Scan(&cat.Name, &cat.Count); err != nil {
			return nil, fmt.Errorf("scan category count failed: %w", err)
		}
		stats.Categories = append(stats.Categories, cat)
	}

	// Count tags
	tagMap := make(map[string]int)
	tagQuery := `SELECT tags FROM entries WHERE tags IS NOT NULL AND tags != ''`
	rows, err = fs.db.Query(tagQuery)
	if err != nil {
		return nil, fmt.Errorf("get tags failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tagsStr string
		if err := rows.Scan(&tagsStr); err != nil {
			return nil, fmt.Errorf("scan tags failed: %w", err)
		}
		tags := strings.Fields(tagsStr)
		for _, tag := range tags {
			tagMap[tag]++
		}
	}

	// Convert tag map to sorted slice
	for tag, count := range tagMap {
		stats.TopTags = append(stats.TopTags, TagInfo{Name: tag, Count: count})
	}
	sort.Slice(stats.TopTags, func(i, j int) bool {
		return stats.TopTags[i].Count > stats.TopTags[j].Count
	})
	if len(stats.TopTags) > 10 {
		stats.TopTags = stats.TopTags[:10]
	}

	// Get recent entries
	recentQuery := `
		SELECT
			id, type, title, category, tags, created,
			date_str, branch, project
		FROM entries
		ORDER BY created DESC
		LIMIT 5`
	rows, err = fs.db.Query(recentQuery)
	if err != nil {
		return nil, fmt.Errorf("get recent entries failed: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r SearchResult
		var tagsStr, category, date, branch, project sql.NullString
		var createdStr string

		err := rows.Scan(
			&r.ID, &r.Type, &r.Title, &category, &tagsStr, &createdStr,
			&date, &branch, &project,
		)
		if err != nil {
			return nil, fmt.Errorf("scan recent entry failed: %w", err)
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

		// Parse created timestamp (could be Unix int or ISO string)
		r.Created, _ = parseCreatedTimestamp(createdStr)

		if tagsStr.Valid && tagsStr.String != "" {
			r.Tags = strings.Fields(tagsStr.String)
		} else {
			r.Tags = []string{}
		}

		// For recent entries, we don't need preview or score
		r.Score = 0
		r.Preview = ""

		stats.RecentEntries = append(stats.RecentEntries, r)
	}

	// Calculate storage size
	var totalSize int64
	err = filepath.Walk(fs.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	if err != nil {
		// Non-fatal, just log
		fmt.Fprintf(os.Stderr, "Warning: could not calculate storage size: %v\n", err)
	}
	stats.StorageSize = totalSize

	return stats, nil
}