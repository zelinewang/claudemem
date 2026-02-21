package storage

import (
	"regexp"
	"strings"
)

var (
	// Pattern to match non-alphanumeric characters
	nonAlphaNumeric = regexp.MustCompile(`[^a-zA-Z0-9-]+`)
	// Pattern to match consecutive hyphens
	multipleHyphens = regexp.MustCompile(`-+`)
)

// Slugify converts a string into a filesystem-safe slug
func Slugify(s string) string {
	// Convert to lowercase
	slug := strings.ToLower(s)

	// Replace spaces and special characters with hyphens
	slug = nonAlphaNumeric.ReplaceAllString(slug, "-")

	// Remove consecutive hyphens
	slug = multipleHyphens.ReplaceAllString(slug, "-")

	// Trim hyphens from both ends
	slug = strings.Trim(slug, "-")

	// Limit to 100 characters
	if len(slug) > 100 {
		slug = slug[:100]
	}

	// Ensure we have something
	if slug == "" {
		slug = "untitled"
	}

	// Append .md extension
	return slug + ".md"
}