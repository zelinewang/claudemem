package storage

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	MaxTitleLen    = 500
	MaxContentLen  = 10 * 1024 * 1024 // 10MB
	MaxCategoryLen = 100
	MaxTagLen      = 100
	MaxTagCount    = 50
	MaxPathLen     = 200
)

// sanitizePath validates a path component (category, branch) to prevent path traversal.
// Rejects: empty, contains "..", "/", "\", starts with ".", control chars, too long.
func sanitizePath(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("path component cannot be empty")
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("invalid path component: contains '..'")
	}
	if strings.ContainsAny(s, "/\\") {
		return "", fmt.Errorf("invalid path component: contains path separator")
	}
	if strings.HasPrefix(s, ".") {
		return "", fmt.Errorf("invalid path component: starts with '.'")
	}
	for _, c := range s {
		if c < 32 || c == 0x7f || c == 0 {
			return "", fmt.Errorf("invalid path component: contains control character")
		}
	}
	if len(s) > MaxPathLen {
		return "", fmt.Errorf("path component too long (max %d chars)", MaxPathLen)
	}
	return s, nil
}

// validateTitle checks that a title is non-empty and within length limits.
func validateTitle(title string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("title cannot be empty")
	}
	if len(title) > MaxTitleLen {
		return fmt.Errorf("title too long (max %d chars, got %d)", MaxTitleLen, len(title))
	}
	// Reject null bytes
	if strings.ContainsRune(title, 0) {
		return fmt.Errorf("title contains null byte")
	}
	return nil
}

// validateContent checks content length.
func validateContent(content string) error {
	if len(content) > MaxContentLen {
		return fmt.Errorf("content too long (max %d bytes, got %d)", MaxContentLen, len(content))
	}
	return nil
}

// validateTags checks tag validity.
func validateTags(tags []string) error {
	if len(tags) > MaxTagCount {
		return fmt.Errorf("too many tags (max %d, got %d)", MaxTagCount, len(tags))
	}
	for _, tag := range tags {
		if len(tag) > MaxTagLen {
			return fmt.Errorf("tag too long (max %d chars): %q", MaxTagLen, tag)
		}
		if strings.ContainsRune(tag, 0) {
			return fmt.Errorf("tag contains null byte")
		}
	}
	return nil
}

// validateFilepathWithinBase ensures a resolved filepath stays within the base directory.
// Belt-and-suspenders defense-in-depth check after all other path validation.
func validateFilepathWithinBase(base, target string) error {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("failed to resolve base path: %w", err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("failed to resolve target path: %w", err)
	}
	if !strings.HasPrefix(absTarget, absBase+string(filepath.Separator)) && absTarget != absBase {
		return fmt.Errorf("path %q escapes base directory %q", absTarget, absBase)
	}
	return nil
}