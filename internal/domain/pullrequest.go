package domain

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Approval summarizes reviewer votes on a pull request.
type Approval int

// Approval states.
const (
	ApprovalPending Approval = iota
	ApprovalApproved
	ApprovalApprovedWithSuggestions
	ApprovalWaitingForAuthor
	ApprovalRejected
)

func (a Approval) String() string {
	switch a {
	case ApprovalApproved:
		return "approved"
	case ApprovalApprovedWithSuggestions:
		return "approved w/ suggestions"
	case ApprovalWaitingForAuthor:
		return "waiting for author"
	case ApprovalRejected:
		return "rejected"
	}
	return "pending"
}

// MarshalText renders the approval as its string form in JSON output.
func (a Approval) MarshalText() ([]byte, error) { return []byte(a.String()), nil }

// Reviewer is a pull request reviewer and their vote (10 approve, 5 suggestions, 0 none,
// -5 waiting for author, -10 reject).
type Reviewer struct {
	Name       string `json:"name"`
	Vote       int    `json:"vote"`
	IsRequired bool   `json:"isRequired,omitempty"`
}

// ComputeApproval derives the overall approval from reviewer votes.
func ComputeApproval(reviewers []Reviewer) Approval {
	if len(reviewers) == 0 {
		return ApprovalPending
	}
	maxVote := reviewers[0].Vote
	var required []Reviewer
	for _, r := range reviewers {
		if r.Vote == -10 {
			return ApprovalRejected
		}
		if r.Vote > maxVote {
			maxVote = r.Vote
		}
		if r.IsRequired {
			required = append(required, r)
		}
	}
	for _, r := range reviewers {
		if r.Vote == -5 {
			return ApprovalWaitingForAuthor
		}
	}
	if len(required) > 0 {
		all := func(min int) bool {
			for _, r := range required {
				if r.Vote < min {
					return false
				}
			}
			return true
		}
		switch {
		case all(10):
			return ApprovalApproved
		case all(5):
			return ApprovalApprovedWithSuggestions
		}
		return ApprovalPending
	}
	switch {
	case maxVote >= 10:
		return ApprovalApproved
	case maxVote >= 5:
		return ApprovalApprovedWithSuggestions
	}
	return ApprovalPending
}

// PullRequest is a summary of a pull request.
type PullRequest struct {
	ID           int        `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description,omitempty"`
	CreatedBy    string     `json:"createdBy"`
	Created      time.Time  `json:"created"`
	SourceBranch string     `json:"sourceBranch"`
	TargetBranch string     `json:"targetBranch"`
	Repository   string     `json:"repository"`
	Project      string     `json:"project,omitempty"`
	Status       string     `json:"status"`
	IsDraft      bool       `json:"isDraft,omitempty"`
	Approval     Approval   `json:"approval"`
	Reviewers    []Reviewer `json:"reviewers,omitempty"`
	URL          string     `json:"url"`
}

// PullRequestStatuses are the accepted --status values.
var PullRequestStatuses = []string{"Active", "Abandoned", "Completed", "All"}

// Pull request query limits.
const (
	DefaultPullRequestTop = 50
	MaxPullRequestTop     = 500
)

// PullRequestQuery describes a pull request search.
type PullRequestQuery struct {
	Project    string
	Status     string
	Top        int
	Creator    string // case-insensitive substring of the creator's display name
	Repository string // exact (case-insensitive) repository name
}

// Validate checks the query and normalizes the status casing.
func (q *PullRequestQuery) Validate() error {
	if strings.TrimSpace(q.Project) == "" {
		return fmt.Errorf("project is required")
	}
	ok := false
	for _, s := range PullRequestStatuses {
		if strings.EqualFold(s, q.Status) {
			q.Status, ok = s, true
		}
	}
	if !ok {
		return fmt.Errorf("--status must be one of: %s", strings.Join(PullRequestStatuses, ", "))
	}
	if q.Top <= 0 || q.Top > MaxPullRequestTop {
		return fmt.Errorf("--top must be between 1 and %d", MaxPullRequestTop)
	}
	return nil
}

// FilterPullRequests applies the client-side creator and repository filters.
func FilterPullRequests(prs []PullRequest, q PullRequestQuery) []PullRequest {
	out := prs[:0:0]
	for _, p := range prs {
		if q.Creator != "" && !strings.Contains(strings.ToLower(p.CreatedBy), strings.ToLower(q.Creator)) {
			continue
		}
		if q.Repository != "" && !strings.EqualFold(p.Repository, q.Repository) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// TrimRef strips the refs/heads/ prefix from a git ref.
func TrimRef(ref string) string { return strings.TrimPrefix(ref, "refs/heads/") }

// PullRequestDraft is a new pull request to create.
type PullRequestDraft struct {
	Project      string
	RepositoryID string
	SourceBranch string
	TargetBranch string
	Title        string
	Description  string
	IsDraft      bool
	WorkItemIDs  []int
}

// MaxPullRequestDescription is the longest pull request description the server accepts, in
// characters (UTF-16 code units, as the server counts them).
const MaxPullRequestDescription = 4000

// textLength is the length of s as the server counts it.
func textLength(s string) int {
	n := 0
	for _, r := range s {
		n += max(1, utf16.RuneLen(r))
	}
	return n
}

// Validate checks the draft has what the server requires.
func (d PullRequestDraft) Validate() error {
	source, target := TrimRef(strings.TrimSpace(d.SourceBranch)), TrimRef(strings.TrimSpace(d.TargetBranch))
	switch {
	case strings.TrimSpace(d.Project) == "":
		return fmt.Errorf("project is required")
	case strings.TrimSpace(d.RepositoryID) == "":
		return fmt.Errorf("repository is required")
	case source == "" || target == "":
		return fmt.Errorf("source and target branches are required")
	case source == target:
		return fmt.Errorf("source and target branch are both %q", source)
	case strings.TrimSpace(d.Title) == "":
		return fmt.Errorf("a pull request needs a title; pass --title")
	}
	if n := textLength(d.Description); n > MaxPullRequestDescription {
		return fmt.Errorf("the description is %d characters; the server allows at most %d", n, MaxPullRequestDescription)
	}
	for _, id := range d.WorkItemIDs {
		if id <= 0 {
			return fmt.Errorf("invalid work item id %d: must be a positive number", id)
		}
	}
	return nil
}

// FormatWorkItemIDs renders ids as "#1, #2".
func FormatWorkItemIDs(ids []int) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = fmt.Sprintf("#%d", id)
	}
	return strings.Join(s, ", ")
}

// ParseWorkItemIDs parses a comma- or space-separated list such as "#1, 2"; empty input is none.
func ParseWorkItemIDs(s string) ([]int, error) {
	var ids []int
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(strings.TrimPrefix(f, "#"))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid work item id %q", f)
		}
		ids = append(ids, n)
	}
	return ids, nil
}

// CommitListMarkdown renders commit subjects as a Markdown bullet list, the way the web UI
// describes a new pull request.
func CommitListMarkdown(subjects []string) string {
	var b strings.Builder
	for _, s := range subjects {
		if s = strings.TrimSpace(s); s != "" {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// JoinDescription appends the commit list to a description, separated by a blank line. When
// the whole list would make the description longer than MaxPullRequestDescription, it is cut
// short with a last line saying how many commits were left out. When not even that fits, the
// list is added whole and Validate reports the length.
func JoinDescription(description, commits string) string {
	description, commits = strings.TrimSpace(description), strings.TrimSpace(commits)
	join := func(list string) string {
		if description == "" || list == "" {
			return description + list
		}
		return description + "\n\n" + list
	}
	budget := MaxPullRequestDescription - textLength(description) // room left for the list
	if description != "" {
		budget -= 2 // the blank line before it
	}
	if commits == "" || textLength(commits) <= budget {
		return join(commits)
	}
	more := func(n int) string {
		if n == 1 {
			return "- … and 1 more commit"
		}
		return fmt.Sprintf("- … and %d more commits", n)
	}
	lines := strings.Split(commits, "\n")
	kept, used := 0, 0
	for ; kept < len(lines); kept++ {
		n := textLength(lines[kept]) + 1 // with its newline
		if used+n+textLength(more(len(lines)-kept-1)) > budget {
			break
		}
		used += n
	}
	if kept == 0 {
		return join(commits)
	}
	return join(strings.Join(lines[:kept], "\n") + "\n" + more(len(lines)-kept))
}
