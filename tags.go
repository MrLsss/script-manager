package main

import (
	"strings"
	"unicode/utf8"
)

// Tag limits. Tags are free-form labels for a scenario ("运维", "备份"), not
// another place to file a script: a script lives in one folder but may carry
// several tags.
const (
	maxTags   = 32
	maxTagLen = 24
)

// parseTags reads a comma or space separated list into normalized tags:
// trimmed, without control characters, deduplicated case-insensitively and
// capped in length and count. An empty text yields no tags at all.
func parseTags(text string) []string {
	parts := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', ';', '；', ' ', '\t', '\n', '\r':
			return true
		}
		return false
	})
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		part = cleanTag(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(out) >= maxTags {
			break
		}
		out = append(out, part)
	}
	return out
}

// cleanTag drops control characters and cuts a tag at the length limit.
func cleanTag(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	for utf8.RuneCountInString(s) > maxTagLen {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return strings.TrimSpace(s)
}

// formatTags renders tags for the editor.
func formatTags(tags []string) string {
	return strings.Join(tags, ", ")
}

// tagMatches reports whether a query matches any of a script's tags.
func tagMatches(tags []string, query string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
