package domain

import (
	"strings"
	"unicode"
)

// ResolveSlug turns a convenience slug ("user-story", "in_progress") into the canonical Azure
// DevOps name ("User Story", "In Progress"). Values that already look canonical (contain a space
// or an uppercase letter) are returned unchanged.
func ResolveSlug(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return trimmed
	}
	for _, r := range trimmed {
		if unicode.IsSpace(r) || unicode.IsUpper(r) {
			return trimmed
		}
	}
	words := strings.FieldsFunc(trimmed, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		rs := []rune(w)
		rs[0] = unicode.ToUpper(rs[0])
		words[i] = string(rs)
	}
	return strings.Join(words, " ")
}

// ResolveSlugs resolves each non-blank value and removes case-insensitive duplicates.
func ResolveSlugs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, v := range values {
		r := ResolveSlug(v)
		if r == "" || seen[strings.ToLower(r)] {
			continue
		}
		seen[strings.ToLower(r)] = true
		out = append(out, r)
	}
	return out
}

// Slugify converts a canonical name to its slug ("User Story" -> "user-story").
func Slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}
