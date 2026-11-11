package domain

import (
	"testing"
	"time"
)

func TestWaitingForReview(t *testing.T) {
	groups := map[string]bool{"g-web": true, "g-api": true}
	pr := func(reviewers ...Reviewer) PullRequest {
		return PullRequest{ID: 1, Status: "active", CreatedByID: "alice", Reviewers: reviewers}
	}
	cases := []struct {
		name string
		pr   PullRequest
		via  string
		ok   bool
	}{
		{"direct, no vote", pr(Reviewer{ID: "ME", Name: "Me"}), "", true},
		{"direct, approved", pr(Reviewer{ID: "me", Vote: 10}), "", false},
		{"direct, waiting for author", pr(Reviewer{ID: "me", Vote: -5}), "", false},
		{"direct, declined", pr(Reviewer{ID: "me", HasDeclined: true}), "", false},
		{"my group, no vote", pr(Reviewer{ID: "G-WEB", Name: "Web Team", IsGroup: true}), "Web Team", true},
		{"my group, a teammate approved", pr(Reviewer{ID: "g-web", Name: "Web Team", Vote: 10}), "", false},
		{"my group, but I approved for it", pr(Reviewer{ID: "g-web", Name: "Web Team"}, Reviewer{ID: "me", Vote: 10}), "", false},
		{"my group and me: direct wins", pr(Reviewer{ID: "g-web", Name: "Web Team"}, Reviewer{ID: "me"}), "", true},
		{"required group preferred", pr(Reviewer{ID: "g-web", Name: "Web Team"}, Reviewer{ID: "g-api", Name: "API", IsRequired: true}), "API", true},
		{"someone else's group", pr(Reviewer{ID: "g-ops", Name: "Ops"}), "", false},
		{"no reviewers", pr(), "", false},
		{"draft", PullRequest{Status: "active", IsDraft: true, Reviewers: []Reviewer{{ID: "me"}}}, "", false},
		{"completed", PullRequest{Status: "completed", Reviewers: []Reviewer{{ID: "me"}}}, "", false},
		{"my own", PullRequest{Status: "active", CreatedByID: "Me", Reviewers: []Reviewer{{ID: "g-web"}}}, "", false},
	}
	for _, c := range cases {
		via, ok := WaitingForReview(c.pr, "me", groups)
		if via != c.via || ok != c.ok {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, via, ok, c.via, c.ok)
		}
	}
	if _, ok := WaitingForReview(pr(Reviewer{}), "", groups); ok {
		t.Error("an unknown user waits for nothing")
	}
}

func TestSplitMyPullRequests(t *testing.T) {
	prs := []PullRequest{
		{ID: 3, Status: "active", CreatedByID: "me"},
		{ID: 2, Status: "active", CreatedByID: "bob", Reviewers: []Reviewer{{ID: "g", Name: "Web"}}},
		{ID: 1, Status: "active", CreatedByID: "bob", Reviewers: []Reviewer{{ID: "me", Vote: 10}}},
	}
	got := SplitMyPullRequests(prs, "me", map[string]bool{"g": true})
	if len(got.Created) != 1 || got.Created[0].ID != 3 {
		t.Errorf("created %+v", got.Created)
	}
	if len(got.ToReview) != 1 || got.ToReview[0].ID != 2 || got.ToReview[0].Via != "Web" {
		t.Errorf("to review %+v", got.ToReview)
	}
	if empty := SplitMyPullRequests(nil, "me", nil); empty.ToReview == nil || empty.Created == nil {
		t.Error("empty results should be empty lists, not null in JSON")
	}
}

func TestLatestBuilds(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	builds := []Build{
		{ID: 5, PipelineID: 1, Branch: "main", Queued: now.Add(-time.Hour)},
		{ID: 4, PipelineID: 1, Branch: "feature/x", Queued: now.Add(-2 * time.Hour)},
		{ID: 3, PipelineID: 1, Branch: "main", Queued: now.Add(-3 * time.Hour)}, // older run of 5's pipeline and branch
		{ID: 2, PipelineID: 2, Branch: "main", Queued: now.Add(-day)},
		{ID: 1, PipelineID: 3, Branch: "main", Queued: now.Add(-10 * day)}, // too old
	}
	got := LatestBuilds(builds, now.Add(-7*day))
	var ids []int
	for _, b := range got {
		ids = append(ids, b.ID)
	}
	if len(ids) != 3 || ids[0] != 5 || ids[1] != 4 || ids[2] != 2 {
		t.Errorf("latest builds %v", ids)
	}
}
