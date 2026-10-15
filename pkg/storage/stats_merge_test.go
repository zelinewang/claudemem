package storage

import (
	"fmt"
	"testing"
	"time"
)

// MergeStats adds up the stores' numbers, merges categories and tags by name, and keeps the newest
// recent entries of all of them with the store each came from.
func TestMergeStats_AddsUpTheStores(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := &StoreStats{
		TotalNotes: 2, TotalSessions: 1, StorageSize: 100,
		Categories: []CategoryInfo{{Name: "ops", Count: 2}},
		TopTags:    []TagInfo{{Name: "x", Count: 2}, {Name: "y", Count: 1}},
		RecentEntries: []SearchResult{
			{ID: "a1", Created: t0.Add(3 * time.Hour), Store: "A"},
			{ID: "a2", Created: t0, Store: "A"},
		},
	}
	b := &StoreStats{
		TotalNotes: 3, TotalSessions: 2, StorageSize: 50,
		Categories: []CategoryInfo{{Name: "life", Count: 1}, {Name: "ops", Count: 2}},
		TopTags:    []TagInfo{{Name: "y", Count: 4}},
		RecentEntries: []SearchResult{
			{ID: "b1", Created: t0.Add(5 * time.Hour), Store: "B"},
			{ID: "b2", Created: t0.Add(4 * time.Hour), Store: "B"},
			{ID: "b3", Created: t0.Add(2 * time.Hour), Store: "B"},
			{ID: "b4", Created: t0.Add(1 * time.Hour), Store: "B"},
		},
	}
	merged := MergeStats([]*StoreStats{a, b})
	if merged.TotalNotes != 5 || merged.TotalSessions != 3 || merged.StorageSize != 150 {
		t.Errorf("totals %d/%d/%d", merged.TotalNotes, merged.TotalSessions, merged.StorageSize)
	}
	if fmt.Sprint(merged.Categories) != "[{ops 4} {life 1}]" {
		t.Errorf("categories %v", merged.Categories)
	}
	if fmt.Sprint(merged.TopTags) != "[{y 5} {x 2}]" {
		t.Errorf("tags %v", merged.TopTags)
	}
	var recent []string
	for _, r := range merged.RecentEntries {
		recent = append(recent, r.ID+"@"+r.Store)
	}
	if fmt.Sprint(recent) != "[b1@B b2@B a1@A b3@B b4@B]" {
		t.Errorf("recent entries %v, want the five newest of both stores", recent)
	}
}

// Stats lists ten tags; StatsAllTags lists every tag, so merged tag counts are exact: here the tag
// that is eleventh in one store is first over both.
func TestStatsAllTags_MakesTheMergeExact(t *testing.T) {
	a, b := setupTestStore(t), setupTestStore(t)
	n := 0
	add := func(store *FileStore, tag string) {
		n++ // a category per note, so no note merges into another
		addTaggedNote(t, store, fmt.Sprintf("cat%02d", n), fmt.Sprintf("Entry %02d", n), "statskw", tag)
	}
	for i := 1; i <= 10; i++ {
		add(a, fmt.Sprintf("t%02d", i))
		add(a, fmt.Sprintf("t%02d", i))
	}
	add(a, "t11")
	add(b, "t11")
	add(b, "t11")

	capped, err := a.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.TopTags) != 10 {
		t.Errorf("Stats lists %d tags, want 10", len(capped.TopTags))
	}
	allA, err := a.StatsAllTags()
	if err != nil {
		t.Fatal(err)
	}
	allB, err := b.StatsAllTags()
	if err != nil {
		t.Fatal(err)
	}
	if len(allA.TopTags) != 11 {
		t.Errorf("StatsAllTags lists %d tags, want 11", len(allA.TopTags))
	}
	merged := MergeStats([]*StoreStats{allA, allB})
	if len(merged.TopTags) != 10 || merged.TopTags[0] != (TagInfo{Name: "t11", Count: 3}) {
		t.Errorf("merged top tags %v, want t11 (3) first and ten tags", merged.TopTags)
	}
	if merged.TotalNotes != 23 || len(merged.Categories) != 23 || len(merged.RecentEntries) != 5 {
		t.Errorf("merged %d notes, %d categories, %d recent", merged.TotalNotes, len(merged.Categories), len(merged.RecentEntries))
	}

	taggedA, err := a.TaggedStatsAllTags([]string{"t11"})
	if err != nil {
		t.Fatal(err)
	}
	taggedB, err := b.TaggedStatsAllTags([]string{"t11"})
	if err != nil {
		t.Fatal(err)
	}
	tagged := MergeTaggedStats([]*TaggedStats{taggedA, taggedB})
	if tagged.TotalNotes != 3 || fmt.Sprint(tagged.Tags) != "[t11]" || fmt.Sprint(tagged.TopTags) != "[{t11 3}]" || len(tagged.Categories) != 3 {
		t.Errorf("merged tagged stats %+v", tagged)
	}
}
