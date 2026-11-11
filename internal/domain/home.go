package domain

import (
	"strings"
	"time"
)

// ReviewRequest is a pull request waiting for the user's review.
type ReviewRequest struct {
	PullRequest
	// Via is the group the review was requested from; empty when the user is a reviewer
	// themself.
	Via string `json:"via,omitempty"`
}

// MyPullRequests are the active pull requests that concern the user.
type MyPullRequests struct {
	ToReview []ReviewRequest `json:"toReview"`
	Created  []PullRequest   `json:"created"`
	// Note says what may be missing, such as reviews requested from groups when the user's
	// group memberships could not be read.
	Note string `json:"note,omitempty"`
}

// WaitingForReview reports whether pr waits for a review by the user with identity id me, who
// belongs to the groups whose lower-cased ids are in groups, and the group the review was
// requested from ("" when the user is a reviewer themself).
//
// A pull request waits when it is active and not a draft, the user did not create it and has
// neither voted nor declined, and the user or one of their groups is a reviewer that has not
// voted. A group's vote is the vote of the member who reviewed for it, so a group request that a
// teammate already answered no longer waits.
func WaitingForReview(pr PullRequest, me string, groups map[string]bool) (string, bool) {
	if me == "" || !strings.EqualFold(pr.Status, "active") || pr.IsDraft || strings.EqualFold(pr.CreatedByID, me) {
		return "", false
	}
	direct := false
	var via *Reviewer
	for i, r := range pr.Reviewers {
		switch {
		case strings.EqualFold(r.ID, me):
			if r.Vote != 0 || r.HasDeclined {
				return "", false
			}
			direct = true
		case groups[strings.ToLower(r.ID)] && r.Vote == 0 && !r.HasDeclined:
			// Prefer a required group: that is the review the pull request cannot do without.
			if via == nil || (r.IsRequired && !via.IsRequired) {
				via = &pr.Reviewers[i]
			}
		}
	}
	switch {
	case direct:
		return "", true
	case via != nil:
		return via.Name, true
	}
	return "", false
}

// SplitMyPullRequests picks the pull requests waiting for the user's review and those the user
// created from prs, keeping their order.
func SplitMyPullRequests(prs []PullRequest, me string, groups map[string]bool) MyPullRequests {
	out := MyPullRequests{ToReview: []ReviewRequest{}, Created: []PullRequest{}}
	for _, pr := range prs {
		if me != "" && strings.EqualFold(pr.CreatedByID, me) {
			if strings.EqualFold(pr.Status, "active") {
				out.Created = append(out.Created, pr)
			}
			continue
		}
		if via, ok := WaitingForReview(pr, me, groups); ok {
			out.ToReview = append(out.ToReview, ReviewRequest{PullRequest: pr, Via: via})
		}
	}
	return out
}

// LatestBuilds keeps the newest build of each pipeline and branch among builds, which are
// newest first, leaving out builds queued before since.
func LatestBuilds(builds []Build, since time.Time) []Build {
	type key struct {
		pipeline int
		branch   string
	}
	seen := map[key]bool{}
	out := []Build{}
	for _, b := range builds {
		k := key{b.PipelineID, b.Branch}
		if seen[k] || (!since.IsZero() && !b.Queued.IsZero() && b.Queued.Before(since)) {
			continue
		}
		seen[k] = true
		out = append(out, b)
	}
	return out
}

// Home is what the home screen shows: what waits for the user, and how their own pull requests,
// builds and work items are doing.
type Home struct {
	Project        string            `json:"project"`
	ReviewRequests []ReviewRequest   `json:"reviewRequests"`
	Approvals      []ReleaseApproval `json:"approvals"`
	PullRequests   []PullRequest     `json:"pullRequests"`
	Builds         []Build           `json:"builds"`
	WorkItems      []WorkItem        `json:"workItems"`
	Notes          []string          `json:"notes,omitempty"`
	// Errors are the sections that could not be loaded, by their JSON name.
	Errors map[string]string `json:"errors,omitempty"`
}

// HomeBuildDays is how many days back the home screen looks for the user's builds.
const HomeBuildDays = 7
