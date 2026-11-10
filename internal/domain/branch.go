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

// parseBranch matches branch against the branch template and returns the {id} and {slug} parts.
// ok is false when the branch does not follow the template.
func parseBranch(template, branch string) (id int, slug string, ok bool) {
	if strings.TrimSpace(template) == "" {
		template = DefaultBranchTemplate
	}
	pattern := strings.NewReplacer(
		`\{id\}`, `(?P<id>\d+)`,
		`\{slug\}`, `(?P<slug>.*)`,
		`\{type\}`, `[^/]*`,
	).Replace(regexp.QuoteMeta(template))
	re, err := regexp.Compile("^" + pattern + "$")
	if err != nil {
		return 0, "", false
	}
	m := re.FindStringSubmatch(branch)
	if m == nil {
		return 0, "", false
	}
	if i := re.SubexpIndex("id"); i > 0 {
		id, _ = strconv.Atoi(m[i])
	}
	if i := re.SubexpIndex("slug"); i > 0 {
		slug = m[i]
	}
	return id, slug, true
}

// WorkItemIDFromBranch extracts the work item id from a branch named by the branch template,
// e.g. 42 from "feature/42-add-thing" with the default template. ok is false when the branch
// does not follow the template.
func WorkItemIDFromBranch(template, branch string) (id int, ok bool) {
	id, _, matched := parseBranch(template, branch)
	if !matched || id <= 0 {
		return 0, false
	}
	return id, true
}

var leadingID = regexp.MustCompile(`^\d+[-_.]*`)

// PullRequestTitleFromBranch turns a branch name into a pull request title without its prefix:
// "feature/42-add-reset-endpoint" becomes "Add reset endpoint". The {slug} of the branch
// template is used when the branch follows it; otherwise the last path segment without a
// leading id. It returns "" when nothing is left.
func PullRequestTitleFromBranch(template, branch string) string {
	words := ""
	if _, slug, ok := parseBranch(template, branch); ok && slug != "" {
		words = slug
	} else {
		words = branch[strings.LastIndex(branch, "/")+1:]
		words = leadingID.ReplaceAllString(words, "")
	}
	words = strings.Join(strings.FieldsFunc(words, func(r rune) bool { return r == '-' || r == '_' || unicode.IsSpace(r) }), " ")
	r := []rune(words)
	if len(r) == 0 {
		return ""
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
