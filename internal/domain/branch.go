package domain

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// DefaultBranchTemplate is used when no branchTemplate is configured.
const DefaultBranchTemplate = "feature/{id}-{slug}"

var leadingTags = regexp.MustCompile(`^(\s*\[[^\]]*\])+`)

// TitleSlug derives a branch-friendly slug from a work item title, dropping leading [tags].
func TitleSlug(title string) string {
	s := leadingTags.ReplaceAllString(title, "")
	s = strings.TrimLeft(strings.TrimSpace(s), ":")
	var b strings.Builder
	lastDash := false
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			lastDash = false
		} else if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// BranchName renders a branch template. Placeholders: {id}, {slug}, {type} (slugified type).
func BranchName(template string, w WorkItem) string {
	if strings.TrimSpace(template) == "" {
		template = DefaultBranchTemplate
	}
	r := strings.NewReplacer(
		"{id}", strconv.Itoa(w.ID),
		"{slug}", TitleSlug(w.Title),
		"{type}", Slugify(w.Type),
	)
	return r.Replace(template)
}
