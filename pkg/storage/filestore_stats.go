package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// topTagsShown is how many tags Stats and TaggedStats list.
const topTagsShown = 10

// recentEntriesShown is how many entries Stats lists as recent activity.
const recentEntriesShown = 5

// Stats returns statistics about the store
func (fs *FileStore) Stats() (*StoreStats, error) {
	return fs.stats(topTagsShown)
}

// StatsAllTags is Stats with every tag listed, so MergeStats can add up the tags of several stores
// exactly.
func (fs *FileStore) StatsAllTags() (*StoreStats, error) {
	return fs.stats(0)
}

// stats lists the tagLimit most used tags, or every tag when tagLimit is 0.
func (fs *FileStore) stats(tagLimit int) (*StoreStats, error) {
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
	if tagLimit > 0 && len(stats.TopTags) > tagLimit {
		stats.TopTags = stats.TopTags[:tagLimit]
	}

	// Get recent entries
	recentQuery := `
		SELECT
			id, type, title, category, tags, created,
			date_str, branch, project
		FROM entries
		ORDER BY created DESC
		LIMIT ?`
	rows, err = fs.db.Query(recentQuery, recentEntriesShown)
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

// TaggedStats counts only the entries carrying every required tag: notes, sessions, the notes'
// categories and the ten most used tags among them. Counts only: no titles and no file reads. Ties
// sort by name, so the output is stable.
func (fs *FileStore) TaggedStats(required []string) (*TaggedStats, error) {
	return fs.taggedStats(required, topTagsShown)
}

// TaggedStatsAllTags is TaggedStats with every tag listed, for MergeTaggedStats.
func (fs *FileStore) TaggedStatsAllTags(required []string) (*TaggedStats, error) {
	return fs.taggedStats(required, 0)
}

// taggedStats lists the tagLimit most used tags, or every tag when tagLimit is 0.
func (fs *FileStore) taggedStats(required []string, tagLimit int) (*TaggedStats, error) {
	if fs.db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	tagFilter, tagArgs := tagsWhere("tags", required)
	rows, err := fs.db.Query(`SELECT type, category, tags FROM entries WHERE 1 = 1`+tagFilter, tagArgs...)
	if err != nil {
		return nil, fmt.Errorf("count tagged entries failed: %w", err)
	}
	defer rows.Close()

	stats := &TaggedStats{Tags: required, Categories: []CategoryInfo{}, TopTags: []TagInfo{}}
	categories := make(map[string]int)
	tagCounts := make(map[string]int)
	for rows.Next() {
		var entryType, category, tagsStr string
		if err := rows.Scan(&entryType, &category, &tagsStr); err != nil {
			return nil, fmt.Errorf("scan tagged entry failed: %w", err)
		}
		switch entryType {
		case "note":
			stats.TotalNotes++
			categories[category]++
		case "session":
			stats.TotalSessions++
		}
		for _, tag := range strings.Fields(tagsStr) {
			tagCounts[tag]++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	for name, count := range categories {
		stats.Categories = append(stats.Categories, CategoryInfo{Name: name, Count: count})
	}
	sort.Slice(stats.Categories, func(i, j int) bool {
		a, b := stats.Categories[i], stats.Categories[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Name < b.Name)
	})
	for name, count := range tagCounts {
		stats.TopTags = append(stats.TopTags, TagInfo{Name: name, Count: count})
	}
	sort.Slice(stats.TopTags, func(i, j int) bool {
		a, b := stats.TopTags[i], stats.TopTags[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Name < b.Name)
	})
	if tagLimit > 0 && len(stats.TopTags) > tagLimit {
		stats.TopTags = stats.TopTags[:tagLimit]
	}
	return stats, nil
}

// MergeStats adds up the stats of several stores: the counts and storage sizes, the categories and
// tags by name (the ten most used tags), and the five newest entries of all the stores, newest first.
// The parts should come from StatsAllTags, so that the tags are counted exactly; each part's recent
// entries keep their Store field.
func MergeStats(parts []*StoreStats) *StoreStats {
	merged := &StoreStats{Categories: []CategoryInfo{}, TopTags: []TagInfo{}, RecentEntries: []SearchResult{}}
	categories := make(map[string]int)
	tags := make(map[string]int)
	for _, part := range parts {
		merged.TotalNotes += part.TotalNotes
		merged.TotalSessions += part.TotalSessions
		merged.StorageSize += part.StorageSize
		for _, c := range part.Categories {
			categories[c.Name] += c.Count
		}
		for _, t := range part.TopTags {
			tags[t.Name] += t.Count
		}
		merged.RecentEntries = append(merged.RecentEntries, part.RecentEntries...)
	}
	merged.Categories = countsByName(categories)
	for _, c := range countsByName(tags) {
		merged.TopTags = append(merged.TopTags, TagInfo(c))
	}
	if len(merged.TopTags) > topTagsShown {
		merged.TopTags = merged.TopTags[:topTagsShown]
	}
	sort.SliceStable(merged.RecentEntries, func(i, j int) bool {
		return merged.RecentEntries[i].Created.After(merged.RecentEntries[j].Created)
	})
	if len(merged.RecentEntries) > recentEntriesShown {
		merged.RecentEntries = merged.RecentEntries[:recentEntriesShown]
	}
	return merged
}

// MergeTaggedStats adds up the tagged stats of several stores, as MergeStats does. The parts should
// come from TaggedStatsAllTags, with the same tags.
func MergeTaggedStats(parts []*TaggedStats) *TaggedStats {
	merged := &TaggedStats{Categories: []CategoryInfo{}, TopTags: []TagInfo{}}
	categories := make(map[string]int)
	tags := make(map[string]int)
	for _, part := range parts {
		if merged.Tags == nil {
			merged.Tags = part.Tags
		}
		merged.TotalNotes += part.TotalNotes
		merged.TotalSessions += part.TotalSessions
		for _, c := range part.Categories {
			categories[c.Name] += c.Count
		}
		for _, t := range part.TopTags {
			tags[t.Name] += t.Count
		}
	}
	merged.Categories = countsByName(categories)
	for _, c := range countsByName(tags) {
		merged.TopTags = append(merged.TopTags, TagInfo(c))
	}
	if len(merged.TopTags) > topTagsShown {
		merged.TopTags = merged.TopTags[:topTagsShown]
	}
	return merged
}

// countsByName turns name counts into a list, most counted first and ties by name.
func countsByName(counts map[string]int) []CategoryInfo {
	list := make([]CategoryInfo, 0, len(counts))
	for name, count := range counts {
		list = append(list, CategoryInfo{Name: name, Count: count})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		return a.Count > b.Count || (a.Count == b.Count && a.Name < b.Name)
	})
	return list
}
