package storage

import (
	"strings"
	"testing"
)

func TestSlugify_Normal(t *testing.T) {
	result := Slugify("Hello World")
	expected := "hello-world.md"
	if result != expected {
		t.Errorf("Slugify('Hello World') = %q, want %q", result, expected)
	}
}

func TestSlugify_SpecialChars(t *testing.T) {
	result := Slugify("foo@bar#baz!")
	expected := "foo-bar-baz.md"
	if result != expected {
		t.Errorf("Slugify('foo@bar#baz!') = %q, want %q", result, expected)
	}
}

func TestSlugify_Empty(t *testing.T) {
	result := Slugify("")
	expected := "untitled.md"
	if result != expected {
		t.Errorf("Slugify('') = %q, want %q", result, expected)
	}
}

func TestSlugify_Long(t *testing.T) {
	// Create a 200 character string
	longString := strings.Repeat("a", 200)
	result := Slugify(longString)

	// Result should be truncated to 100 chars + ".md" = 103 total
	if len(result) > 103 {
		t.Errorf("Slugify() result too long: %d chars (max 103), got %q", len(result), result)
	}

	// Should end with .md
	if !strings.HasSuffix(result, ".md") {
		t.Errorf("Slugify() result should end with '.md', got %q", result)
	}

	// Should be exactly 100 'a's + .md
	expected := strings.Repeat("a", 100) + ".md"
	if result != expected {
		t.Errorf("Slugify() = %q, want %q", result, expected)
	}
}

func TestSlugify_ConsecutiveHyphens(t *testing.T) {
	result := Slugify("a---b")
	expected := "a-b.md"
	if result != expected {
		t.Errorf("Slugify('a---b') = %q, want %q", result, expected)
	}

	// Test with spaces that become hyphens
	result = Slugify("word   with   spaces")
	expected = "word-with-spaces.md"
	if result != expected {
		t.Errorf("Slugify('word   with   spaces') = %q, want %q", result, expected)
	}
}

func TestSlugify_OnlySpecialChars(t *testing.T) {
	testCases := []string{
		"!@#$%",
		"***",
		"...",
		"---",
		"   ",
	}

	for _, tc := range testCases {
		result := Slugify(tc)
		expected := "untitled.md"
		if result != expected {
			t.Errorf("Slugify(%q) = %q, want %q", tc, result, expected)
		}
	}
}

func TestSlugify_AlreadyClean(t *testing.T) {
	result := Slugify("hello")
	expected := "hello.md"
	if result != expected {
		t.Errorf("Slugify('hello') = %q, want %q", result, expected)
	}

	result = Slugify("hello-world")
	expected = "hello-world.md"
	if result != expected {
		t.Errorf("Slugify('hello-world') = %q, want %q", result, expected)
	}
}

func TestSlugify_LeadingTrailingHyphens(t *testing.T) {
	result := Slugify("-hello-")
	expected := "hello.md"
	if result != expected {
		t.Errorf("Slugify('-hello-') = %q, want %q", result, expected)
	}

	result = Slugify("---hello---")
	expected = "hello.md"
	if result != expected {
		t.Errorf("Slugify('---hello---') = %q, want %q", result, expected)
	}

	// With special chars that become hyphens
	result = Slugify("@hello@")
	expected = "hello.md"
	if result != expected {
		t.Errorf("Slugify('@hello@') = %q, want %q", result, expected)
	}
}

func TestSlugify_MixedCase(t *testing.T) {
	result := Slugify("CamelCaseTitle")
	expected := "camelcasetitle.md"
	if result != expected {
		t.Errorf("Slugify('CamelCaseTitle') = %q, want %q", result, expected)
	}

	result = Slugify("UPPERCASE TITLE")
	expected = "uppercase-title.md"
	if result != expected {
		t.Errorf("Slugify('UPPERCASE TITLE') = %q, want %q", result, expected)
	}
}

func TestSlugify_Unicode(t *testing.T) {
	// Non-ASCII characters should be replaced with hyphens
	result := Slugify("café")
	expected := "caf.md" // é is non-alphanumeric, gets removed/replaced
	if result != expected {
		t.Errorf("Slugify('café') = %q, want %q", result, expected)
	}

	result = Slugify("hello 世界")
	expected = "hello.md" // Chinese characters are non-alphanumeric
	if result != expected {
		t.Errorf("Slugify('hello 世界') = %q, want %q", result, expected)
	}
}

func TestSlugify_Numbers(t *testing.T) {
	result := Slugify("test123")
	expected := "test123.md"
	if result != expected {
		t.Errorf("Slugify('test123') = %q, want %q", result, expected)
	}

	result = Slugify("123")
	expected = "123.md"
	if result != expected {
		t.Errorf("Slugify('123') = %q, want %q", result, expected)
	}

	result = Slugify("test 123 abc")
	expected = "test-123-abc.md"
	if result != expected {
		t.Errorf("Slugify('test 123 abc') = %q, want %q", result, expected)
	}
}