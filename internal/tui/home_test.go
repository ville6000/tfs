package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

var homeNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func newTestHome() *Home {
	h := NewHome("Portal", &Actions{}, &Builds{}, &Releases{}, HomeSources{})
	h.now = func() time.Time { return homeNow }
	h.SetSize(120, 40)
	return h
}

func homePRs() homeLoadedMsg {
	return homeLoadedMsg{source: sourcePullRequests, prs: domain.MyPullRequests{
		ToReview: []domain.ReviewRequest{
			{PullRequest: domain.PullRequest{ID: 41, Repository: "web", CreatedBy: "Bob", Title: "Add reset endpoint", Created: homeNow.Add(-50 * time.Hour)}, Via: "Web Team"},
			{PullRequest: domain.PullRequest{ID: 40, Repository: "api", CreatedBy: "Carol", Title: "Fix login"}},
		},
		Created: []domain.PullRequest{{ID: 39, Repository: "web", Title: "Mine", Approval: domain.ApprovalApproved, TargetBranch: "main"}},
	}}
}

func homeItems(ids ...int) homeLoadedMsg {
	msg := homeLoadedMsg{source: sourceWorkItems}
	for _, id := range ids {
		msg.items = append(msg.items, domain.WorkItem{ID: id, Type: "Bug", State: "Active", Title: "Item"})
	}
	return msg
}

func (h *Home) feed(msg homeLoadedMsg) tea.Cmd {
	msg.screen = h.id
	_, cmd := h.Update(msg)
	return cmd
}

func keyNames(s Screen) string {
	var keys []string
	for _, b := range s.Help() {
		keys = append(keys, b.Help().Key)
	}
	return strings.Join(keys, " ")
}

func TestHomeSections(t *testing.T) {
	h := newTestHome()
	view := ansi.Strip(h.View())
	for _, want := range []string{"Waiting for your review  loading…", "Your work items  loading…"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q before loading:\n%s", want, view)
		}
	}

	h.feed(homeItems(7))
	h.feed(homePRs())
	h.feed(homeLoadedMsg{source: sourceApprovals})
	h.feed(homeLoadedMsg{source: sourceBuilds, err: errors.New("server returned 401: no Build scope")})
	view = ansi.Strip(h.View())
	for _, want := range []string{
		"Waiting for your review  2", "41  web  Bob    Web Team  2d", "40  api  Carol  you",
		"Release approvals  nothing waiting",
		"Your pull requests  1", "approved",
		"Your builds", "no Build scope",
		"Your work items  1", "7  Bug  Active",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	// Loading a later section first does not leave the cursor there: it starts on the first entry.
	if e, _ := h.selected(); e.(domain.ReviewRequest).ID != 41 {
		t.Errorf("starts on %+v", e)
	}
}

func TestHomeNavigation(t *testing.T) {
	h := newTestHome()
	h.feed(homePRs())
	h.feed(homeItems(7, 8))
	if keys := keyNames(h); !strings.Contains(keys, "c w") || strings.Contains(keys, "s b") {
		t.Errorf("pull request keys: %s", keys)
	}

	h.Update(press("j"))
	h.Update(press("j")) // past the last review, into my pull requests
	if e, _ := h.selected(); e.(domain.PullRequest).ID != 39 {
		t.Fatalf("selected %+v", e)
	}
	h.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // empty approvals and builds are skipped
	if e, _ := h.selected(); e.(domain.WorkItem).ID != 7 {
		t.Fatalf("tab selected %+v", e)
	}
	if keys := keyNames(h); !strings.Contains(keys, "s b w c") {
		t.Errorf("work item keys: %s", keys)
	}
	h.Update(tea.KeyPressMsg{Code: tea.KeyTab}) // wraps around
	if e, _ := h.selected(); e.(domain.ReviewRequest).ID != 41 {
		t.Fatalf("tab wrapped to %+v", e)
	}
	h.Update(press("G"))
	if e, _ := h.selected(); e.(domain.WorkItem).ID != 8 {
		t.Fatalf("G selected %+v", e)
	}

	// A reload keeps the cursor on the same item, wherever it moved to.
	h.feed(homeItems(9, 8, 7))
	if e, _ := h.selected(); e.(domain.WorkItem).ID != 8 {
		t.Errorf("after reload %+v", e)
	}
	// An update from another screen shows in place.
	h.Update(workItemChangedMsg{domain.WorkItem{ID: 8, Type: "Bug", State: "Resolved", Title: "Item"}})
	if !strings.Contains(ansi.Strip(h.View()), "8  Bug  Resolved") {
		t.Errorf("changed item not shown:\n%s", ansi.Strip(h.View()))
	}

	if _, cmd := h.Update(press("enter")); cmd == nil {
		t.Fatal("enter should open the work item")
	} else if push, ok := cmd().(PushMsg); !ok || push.Screen.Title() != "#8" {
		t.Errorf("enter pushed %#v", cmd())
	}
}

func TestHomeScrollsToSelection(t *testing.T) {
	h := newTestHome()
	h.SetSize(100, 8)
	h.feed(homePRs())
	h.feed(homeItems(1, 2, 3, 4, 5, 6))
	h.Update(press("G"))
	view := ansi.Strip(h.View())
	if !strings.Contains(view, "6  Bug") || strings.Contains(view, "Waiting for your review") {
		t.Errorf("bottom view:\n%s", view)
	}
	h.Update(press("g"))
	if view := ansi.Strip(h.View()); !strings.HasPrefix(view, "Waiting for your review") {
		t.Errorf("top view:\n%s", view)
	}
}

func TestHomeRunningBuildsRefresh(t *testing.T) {
	h := newTestHome()
	running := homeLoadedMsg{source: sourceBuilds, builds: []domain.Build{{ID: 3, Pipeline: "ci", Status: "inProgress", Started: homeNow.Add(-90 * time.Second)}}}
	if cmd := h.feed(running); cmd == nil {
		t.Error("a running build should schedule a refresh")
	}
	if cmd := h.feed(running); cmd != nil {
		t.Error("only one refresh should be scheduled at a time")
	}
	if !strings.Contains(ansi.Strip(h.View()), "running  1m") {
		t.Errorf("build row:\n%s", ansi.Strip(h.View()))
	}
}
