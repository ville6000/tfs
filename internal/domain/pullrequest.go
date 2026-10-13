package domain

import (
	"fmt"
	"strings"
	"time"
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
