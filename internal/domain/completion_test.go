package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestParseMergeStrategy(t *testing.T) {
	for in, want := range map[string]MergeStrategy{
		"merge": MergeNoFastForward, "noFastForward": MergeNoFastForward, "Squash": MergeSquash, "squash\n": MergeSquash,
		"rebase": MergeRebase, "semilinear": MergeRebaseMerge, "rebaseMerge": MergeRebaseMerge,
	} {
		if got, err := ParseMergeStrategy(in); err != nil || got != want {
			t.Errorf("ParseMergeStrategy(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseMergeStrategy("fast-forward"); err == nil || !strings.Contains(err.Error(), "merge, squash, rebase, semilinear") {
		t.Errorf("unknown strategy: %v", err)
	}
}

func TestAllowedMergeStrategies(t *testing.T) {
	if got := AllowedMergeStrategies(nil); got != nil {
		t.Errorf("no policy limits them: %v", got)
	}
	got := AllowedMergeStrategies([]MergePolicy{
		{Allowed: []MergeStrategy{MergeRebase, MergeSquash, MergeNoFastForward}},
		{Allowed: []MergeStrategy{MergeSquash, MergeRebase}},
	})
	if !slices.Equal(got, []MergeStrategy{MergeSquash, MergeRebase}) {
		t.Errorf("every policy's, in the web UI's order: %v", got)
	}
	if got := AllowedMergeStrategies([]MergePolicy{{Allowed: []MergeStrategy{MergeSquash}}, {Allowed: []MergeStrategy{MergeRebase}}}); got != nil {
		t.Errorf("none in common is left to the server: %v", got)
	}
}

func TestPickMergeStrategy(t *testing.T) {
	squashOnly := []MergeStrategy{MergeSquash}
	for _, c := range []struct {
		allowed   []MergeStrategy
		preferred MergeStrategy
		want      MergeStrategy
	}{
		{nil, "", MergeNoFastForward},
		{nil, MergeRebase, MergeRebase},
		{squashOnly, "", MergeSquash},
		{squashOnly, MergeNoFastForward, MergeSquash},
		{[]MergeStrategy{MergeSquash, MergeRebase}, MergeRebase, MergeRebase},
	} {
		if got := PickMergeStrategy(c.allowed, c.preferred); got != c.want {
			t.Errorf("PickMergeStrategy(%v, %q) = %q, want %q", c.allowed, c.preferred, got, c.want)
		}
	}
	if err := CheckMergeStrategy(squashOnly, MergeSquash, "main"); err != nil {
		t.Error(err)
	}
	if err := CheckMergeStrategy(nil, MergeRebase, "main"); err != nil {
		t.Error(err)
	}
	if err := CheckMergeStrategy(squashOnly, MergeRebase, "main"); err == nil || err.Error() != "the policies of main do not allow rebase (allowed: squash)" {
		t.Errorf("disallowed: %v", err)
	}
}

func TestCompletionSummaryAndOpen(t *testing.T) {
	if got := (CompletionOptions{}).Summary(); got != "merge" {
		t.Errorf("default summary %q", got)
	}
	o := CompletionOptions{MergeStrategy: MergeSquash, DeleteSourceBranch: true, CompleteWorkItems: true}
	if got := o.Summary(); got != "squash · delete source branch · complete work items" {
		t.Errorf("summary %q", got)
	}
	if err := (PullRequest{ID: 5, Status: "active"}).CheckOpen(); err != nil {
		t.Error(err)
	}
	if err := (PullRequest{ID: 5, Status: "completed"}).CheckOpen(); err == nil || err.Error() != "pull request !5 is completed" {
		t.Errorf("closed: %v", err)
	}
}
