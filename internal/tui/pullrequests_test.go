package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

func TestPullRequestListMarks(t *testing.T) {
	l := NewPullRequestList("prs", &Actions{}, func(context.Context) ([]domain.PullRequest, error) { return nil, nil })
	l.SetSize(120, 10)
	l.Update(pullRequestsLoadedMsg{screen: l.id, prs: []domain.PullRequest{
		{ID: 5, Status: "active", Title: "Fix login", MergeStatus: "conflicts"},
		{ID: 6, Status: "active", Title: "Add reset", IsDraft: true, MergeStatus: "succeeded"},
		{ID: 7, Status: "abandoned", Title: "Old idea", MergeStatus: "conflicts"},
	}})
	view := ansi.Strip(l.View())
	for _, want := range []string{"[conflicts] Fix login", "[draft] Add reset"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	if strings.Contains(view, "[conflicts] Old idea") {
		t.Errorf("an abandoned pull request has nothing to merge:\n%s", view)
	}
}
