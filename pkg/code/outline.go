package code

import (
	"bufio"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Symbol represents a code symbol extracted from a file
type Symbol struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // function, method, class, struct, interface, type, const, var
	Line      int    `json:"line"`
	Signature string `json:"signature"`
}

// OutlineResult represents the outline of a single file
type OutlineResult struct {
	File    string   `json:"file"`
	Lang    string   `json:"language"`
	Symbols []Symbol `json:"symbols"`
}

// langPatterns maps language → list of regex patterns for symbol extraction
var langPatterns = map[string][]symbolPattern{
	"go": {
		{regexp.MustCompile(`^func\s+\((\w+)\s+\*?(\w+)\)\s+(\w+)\s*\(`), "method",
			func(m []string) string { return fmt.Sprintf("(%s %s) %s()", m[1], m[2], m[3]) }},
		{regexp.MustCompile(`^func\s+(\w+)\s*\(`), "function",
			func(m []string) string { return m[1] + "()" }},
		{regexp.MustCompile(`^type\s+(\w+)\s+struct\s*\{`), "struct",
			func(m []string) string { return "type " + m[1] + " struct" }},
		{regexp.MustCompile(`^type\s+(\w+)\s+interface\s*\{`), "interface",
			func(m []string) string { return "type " + m[1] + " interface" }},
		{regexp.MustCompile(`^type\s+(\w+)\s+`), "type",
			func(m []string) string { return "type " + m[1] }},
		{regexp.MustCompile(`^var\s+(\w+)\s+`), "var",
			func(m []string) string { return "var " + m[1] }},
		{regexp.MustCompile(`^\s+(\w+)\s*=\s*&cobra\.Command`), "command",
			func(m []string) string { return "command " + m[1] }},
	},
	"python": {
		{regexp.MustCompile(`^class\s+(\w+)[\s(:]`), "class",
			func(m []string) string { return "class " + m[1] }},
		{regexp.MustCompile(`^(\s*)async\s+def\s+(\w+)\s*\(`), "function",
			func(m []string) string {
				if m[1] != "" {
					return "async method " + m[2] + "()"
				}
				return "async " + m[2] + "()"
			}},
		{regexp.MustCompile(`^(\s*)def\s+(\w+)\s*\(`), "function",
			func(m []string) string {
				if m[1] != "" {
					return "method " + m[2] + "()"
				}
				return m[2] + "()"
			}},
		{regexp.MustCompile(`^(\w+)\s*=\s*`), "var",
			func(m []string) string { return m[1] }},
	},
	"typescript": {
		{regexp.MustCompile(`^export\s+(default\s+)?class\s+(\w+)`), "class",
			func(m []string) string { return "class " + m[2] }},
		{regexp.MustCompile(`^export\s+(default\s+)?function\s+(\w+)\s*[(<]`), "function",
			func(m []string) string { return m[2] + "()" }},
		{regexp.MustCompile(`^export\s+(default\s+)?(?:const|let)\s+(\w+)`), "var",
			func(m []string) string { return m[2] }},
		{regexp.MustCompile(`^export\s+(?:interface|type)\s+(\w+)`), "type",
			func(m []string) string { return "type " + m[1] }},
		{regexp.MustCompile(`^(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s+)?(?:\([^)]*\)|[^=])*=>`), "function",
			func(m []string) string { return m[1] + "()" }},
		{regexp.MustCompile(`^function\s+(\w+)\s*[(<]`), "function",
			func(m []string) string { return m[1] + "()" }},
		{regexp.MustCompile(`^class\s+(\w+)`), "class",
			func(m []string) string { return "class " + m[1] }},
		{regexp.MustCompile(`^interface\s+(\w+)`), "interface",
			func(m []string) string { return "interface " + m[1] }},
	},
	"rust": {
		{regexp.MustCompile(`^pub\s+fn\s+(\w+)`), "function",
			func(m []string) string { return "pub fn " + m[1] + "()" }},
		{regexp.MustCompile(`^fn\s+(\w+)`), "function",
			func(m []string) string { return "fn " + m[1] + "()" }},
		{regexp.MustCompile(`^pub\s+struct\s+(\w+)`), "struct",
			func(m []string) string { return "pub struct " + m[1] }},
		{regexp.MustCompile(`^struct\s+(\w+)`), "struct",
			func(m []string) string { return "struct " + m[1] }},
		{regexp.MustCompile(`^pub\s+enum\s+(\w+)`), "type",
			func(m []string) string { return "pub enum " + m[1] }},
		{regexp.MustCompile(`^impl(?:<[^>]+>)?\s+(\w+)`), "impl",
			func(m []string) string { return "impl " + m[1] }},
		{regexp.MustCompile(`^pub\s+trait\s+(\w+)`), "interface",
			func(m []string) string { return "pub trait " + m[1] }},
		{regexp.MustCompile(`^trait\s+(\w+)`), "interface",
			func(m []string) string { return "trait " + m[1] }},
	},
}

type symbolPattern struct {
	regex    *regexp.Regexp
	symType  string
	nameFunc func([]string) string
}

// DetectLanguage returns the language based on file extension
func DetectLanguage(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs":
		return "typescript" // JS shares TS patterns for basic outline
	case ".rs":
		return "rust"
	default:
		return ""
	}
}

// Outline extracts symbols from source code content
func Outline(filename, content string) *OutlineResult {
	lang := DetectLanguage(filename)
	if lang == "" {
		return &OutlineResult{File: filename, Lang: "unknown", Symbols: []Symbol{}}
	}

	patterns, ok := langPatterns[lang]
	if !ok {
		return &OutlineResult{File: filename, Lang: lang, Symbols: []Symbol{}}
	}

	var symbols []Symbol
	scanner := bufio.NewScanner(strings.NewReader(content))
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		for _, p := range patterns {
			matches := p.regex.FindStringSubmatch(line)
			if matches != nil {
				name := p.nameFunc(matches)
				symbols = append(symbols, Symbol{
					Name:      name,
					Type:      p.symType,
					Line:      lineNum,
					Signature: strings.TrimSpace(line),
				})
				break // First match wins per line
			}
		}
	}

	return &OutlineResult{
		File:    filename,
		Lang:    lang,
		Symbols: symbols,
	}
}
