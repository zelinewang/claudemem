package storage

import (
	"strings"
	"testing"
)

func TestSanitizePath_Valid(t *testing.T) {
	testCases := []string{
		"api-specs",
		"my-category",
		"feature-branch",
		"project123",
		"valid_name",
	}

	for _, tc := range testCases {
		result, err := sanitizePath(tc)
		if err != nil {
			t.Errorf("sanitizePath(%q) returned unexpected error: %v", tc, err)
		}
		if result != tc {
			t.Errorf("sanitizePath(%q) = %q, want %q", tc, result, tc)
		}
	}
}

func TestSanitizePath_Empty(t *testing.T) {
	_, err := sanitizePath("")
	if err == nil {
		t.Errorf("sanitizePath('') should return error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("Expected error about empty path, got: %v", err)
	}
}

func TestSanitizePath_PathTraversal(t *testing.T) {
	testCases := []string{
		"..",
		"foo/../bar",
		"../etc/passwd",
		"foo/..",
		"foo/bar/../..",
	}

	for _, tc := range testCases {
		_, err := sanitizePath(tc)
		if err == nil {
			t.Errorf("sanitizePath(%q) should return error for path traversal", tc)
		}
		if !strings.Contains(err.Error(), "'..'") {
			t.Errorf("Expected error about '..', got: %v", err)
		}
	}
}

func TestSanitizePath_Separator(t *testing.T) {
	testCases := []string{
		"foo/bar",
		"foo\\bar",
		"/absolute",
		"relative/path",
		"C:\\Windows",
	}

	for _, tc := range testCases {
		_, err := sanitizePath(tc)
		if err == nil {
			t.Errorf("sanitizePath(%q) should return error for path separator", tc)
		}
		if !strings.Contains(err.Error(), "separator") {
			t.Errorf("Expected error about path separator, got: %v", err)
		}
	}
}

func TestSanitizePath_DotPrefix(t *testing.T) {
	testCases := []string{
		".hidden",
		".git",
		".env",
	}

	for _, tc := range testCases {
		_, err := sanitizePath(tc)
		if err == nil {
			t.Errorf("sanitizePath(%q) should return error for dot prefix", tc)
		}
		if !strings.Contains(err.Error(), "starts with '.'") {
			t.Errorf("Expected error about dot prefix, got: %v", err)
		}
	}
}

func TestSanitizePath_ControlChars(t *testing.T) {
	testCases := []string{
		"foo\x00bar",  // null byte
		"foo\x01bar",  // control char
		"foo\x1fbar",  // control char
		"foo\x7fbar",  // DEL char
		"tab\there",   // tab is control char < 32
		"newline\nbar", // newline is control char
	}

	for _, tc := range testCases {
		_, err := sanitizePath(tc)
		if err == nil {
			t.Errorf("sanitizePath(%q) should return error for control character", tc)
		}
		if !strings.Contains(err.Error(), "control character") {
			t.Errorf("Expected error about control character, got: %v", err)
		}
	}
}

func TestSanitizePath_TooLong(t *testing.T) {
	// Create a string that's 201 characters long
	longPath := strings.Repeat("a", 201)

	_, err := sanitizePath(longPath)
	if err == nil {
		t.Errorf("sanitizePath() should return error for path longer than %d chars", MaxPathLen)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("Expected error about path too long, got: %v", err)
	}

	// Verify exactly at limit is OK
	atLimit := strings.Repeat("a", MaxPathLen)
	result, err := sanitizePath(atLimit)
	if err != nil {
		t.Errorf("sanitizePath() should accept path of exactly %d chars, got error: %v", MaxPathLen, err)
	}
	if result != atLimit {
		t.Errorf("sanitizePath() should return unchanged path at limit")
	}
}

func TestValidateTitle_Valid(t *testing.T) {
	testCases := []string{
		"My Title",
		"A",
		"Title with spaces",
		"Title-with-dashes",
		"Title_with_underscores",
		"Title123",
		strings.Repeat("a", 100), // reasonably long title
	}

	for _, tc := range testCases {
		err := validateTitle(tc)
		if err != nil {
			t.Errorf("validateTitle(%q) returned unexpected error: %v", tc, err)
		}
	}
}

func TestValidateTitle_Empty(t *testing.T) {
	err := validateTitle("")
	if err == nil {
		t.Errorf("validateTitle('') should return error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("Expected error about empty title, got: %v", err)
	}
}

func TestValidateTitle_WhitespaceOnly(t *testing.T) {
	testCases := []string{
		"   ",
		"\t\t",
		"\n\n",
		"  \t\n  ",
	}

	for _, tc := range testCases {
		err := validateTitle(tc)
		if err == nil {
			t.Errorf("validateTitle(%q) should return error for whitespace-only", tc)
		}
		if !strings.Contains(err.Error(), "empty") {
			t.Errorf("Expected error about empty title, got: %v", err)
		}
	}
}

func TestValidateTitle_NullByte(t *testing.T) {
	titleWithNull := "foo\x00bar"

	err := validateTitle(titleWithNull)
	if err == nil {
		t.Errorf("validateTitle() should return error for title with null byte")
	}
	if !strings.Contains(err.Error(), "null byte") {
		t.Errorf("Expected error about null byte, got: %v", err)
	}
}

func TestValidateTitle_TooLong(t *testing.T) {
	// Create a string that's 501 characters long
	longTitle := strings.Repeat("a", 501)

	err := validateTitle(longTitle)
	if err == nil {
		t.Errorf("validateTitle() should return error for title longer than %d chars", MaxTitleLen)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("Expected error about title too long, got: %v", err)
	}
}

func TestValidateTitle_AtBoundary(t *testing.T) {
	// Exactly 500 characters should be OK
	exactlyAtLimit := strings.Repeat("a", 500)

	err := validateTitle(exactlyAtLimit)
	if err != nil {
		t.Errorf("validateTitle() should accept title of exactly %d chars, got error: %v", MaxTitleLen, err)
	}
}

func TestValidateTags_Valid(t *testing.T) {
	testCases := [][]string{
		{"tag1", "tag2"},
		{"api", "security", "authentication"},
		{}, // empty is valid
		{"single"},
		make([]string, 50), // exactly at limit
	}

	for _, tc := range testCases {
		err := validateTags(tc)
		if err != nil {
			t.Errorf("validateTags(%v) returned unexpected error: %v", tc, err)
		}
	}
}

func TestValidateTags_Empty(t *testing.T) {
	err := validateTags([]string{})
	if err != nil {
		t.Errorf("validateTags([]) should accept empty slice, got error: %v", err)
	}

	err = validateTags(nil)
	if err != nil {
		t.Errorf("validateTags(nil) should accept nil slice, got error: %v", err)
	}
}

func TestValidateTags_TooMany(t *testing.T) {
	// Create 51 tags (one more than limit)
	tooManyTags := make([]string, 51)
	for i := range tooManyTags {
		tooManyTags[i] = "tag"
	}

	err := validateTags(tooManyTags)
	if err == nil {
		t.Errorf("validateTags() should return error for more than %d tags", MaxTagCount)
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Errorf("Expected error about too many tags, got: %v", err)
	}
}

func TestValidateTags_NullInTag(t *testing.T) {
	tagsWithNull := []string{"good", "bad\x00tag", "another"}

	err := validateTags(tagsWithNull)
	if err == nil {
		t.Errorf("validateTags() should return error for tag with null byte")
	}
	if !strings.Contains(err.Error(), "null byte") {
		t.Errorf("Expected error about null byte, got: %v", err)
	}
}

func TestValidateTags_TagTooLong(t *testing.T) {
	longTag := strings.Repeat("a", MaxTagLen+1)
	tags := []string{"ok", longTag}

	err := validateTags(tags)
	if err == nil {
		t.Errorf("validateTags() should return error for tag longer than %d chars", MaxTagLen)
	}
	if !strings.Contains(err.Error(), "tag too long") {
		t.Errorf("Expected error about tag too long, got: %v", err)
	}
}