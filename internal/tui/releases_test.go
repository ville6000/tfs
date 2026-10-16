package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

func sampleRelease() domain.Release {
	return domain.Release{ID: 11, Name: "Release-11", Pipeline: "frontend", Stages: []domain.ReleaseStage{
		{ID: 1, Name: "Dev", Status: "succeeded"},
		{ID: 2, Name: "Prod", Status: "notStarted", PendingApprovals: []domain.ReleaseApproval{{ID: 900, Status: "pending", Approver: "Release Managers"}}},
	}}
}

func TestReleaseDashboardRows(t *testing.T) {
	d := NewReleaseDashboard(&Releases{})
	d.SetSize(120, 10)
	rel := sampleRelease()
	p := domain.ReleasePipeline{ID: 5, Name: "frontend", Stages: []domain.PipelineStage{{ID: 1, Name: "Dev"}, {ID: 2, Name: "Prod"}}}
	for i := range rel.Stages {
		rel.Stages[i].PipelineStageID = rel.Stages[i].ID
	}
	rows := []domain.PipelineOverview{
		{Pipeline: p, Latest: &rel, Deployments: domain.LatestPerStage(p, []domain.Release{rel})},
		{Pipeline: domain.ReleasePipeline{ID: 6, Name: "backend"}},
	}
	d.Update(overviewLoadedMsg{screen: d.id, rows: rows, mine: []domain.ReleaseApproval{{ID: 900}}})
	view := ansi.Strip(d.View())
	for _, want := range []string{"frontend", "Release-11", "Dev ✔ Release-11", "Prod ⏸ Release-11", "backend"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	if !strings.Contains(ansi.Strip(d.Title()), "1 waiting for you") {
		t.Errorf("title %q", ansi.Strip(d.Title()))
	}
}

func run(cmd tea.Cmd) tea.Msg {
	if cmd == nil {
		return nil
	}
	return cmd()
}

func TestReleaseViewActions(t *testing.T) {
	v := NewReleaseView(&Releases{}, 11)
	v.SetSize(100, 20)
	v.Update(releaseLoadedMsg{screen: v.sid, release: sampleRelease()})
	view := ansi.Strip(v.View())
	if !strings.Contains(view, "awaiting approval") || !strings.Contains(view, "Release Managers") {
		t.Errorf("view:\n%s", view)
	}
	// The view starts on the stage awaiting approval.
	if s, _ := v.selectedStage(); s.Name != "Prod" {
		t.Errorf("starts on %q", s.Name)
	}
	v.Update(press("k")) // Dev
	// Dev is not waiting for anything.
	_, cmd := v.Update(press("a"))
	if msg, ok := run(cmd).(StatusMsg); !ok || !strings.Contains(msg.Text, "not waiting") {
		t.Errorf("approve on Dev: %#v", run(cmd))
	}
	// There is no manual deployment.
	if _, cmd := v.Update(press("d")); cmd != nil {
		if _, isPush := run(cmd).(PushMsg); isPush {
			t.Error("d must not start a deployment")
		}
	}
	v.Update(press("j")) // Prod
	if _, cmd := v.Update(press("x")); cmd == nil {
		t.Error("reject on a pending stage should start the approval lookup")
	}
}

func TestReleaseViewWorkItems(t *testing.T) {
	v := NewReleaseView(&Releases{}, 11)
	v.SetSize(120, 24)
	if _, cmd := v.Update(releaseLoadedMsg{screen: v.sid, release: sampleRelease()}); cmd == nil {
		t.Fatal("loading the release should load the selected stage's work items")
	}
	if !strings.Contains(ansi.Strip(v.View()), "Work items for Prod") {
		t.Errorf("loading view:\n%s", ansi.Strip(v.View()))
	}
	v.Update(releaseChangesMsg{screen: v.sid, stageID: 2, changes: domain.ReleaseChanges{
		Stage: "Prod", SinceID: 9, Since: "Release-9", Notes: []string{"_scripts: Git artifacts are not compared"},
		WorkItems: []domain.WorkItem{
			{ID: 501, Type: "Bug", State: "Resolved", Title: "Fix login", URL: "https://tfs/wi/501"},
			{ID: 502, Type: "User Story", State: "Closed", Title: "Sign in with SSO"},
		},
	}})
	view := ansi.Strip(v.View())
	for _, want := range []string{"Work items new to Prod since Release-9", "Git artifacts are not compared", "501", "Fix login", "Sign in with SSO", "1/2"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}
	if n := strings.Count(view, "\n") + 1; n > 24 {
		t.Errorf("view is %d lines, more than its 24", n)
	}

	// Moving to Dev loads its work items once; coming back to Prod uses what was loaded.
	if _, cmd := v.Update(press("k")); cmd == nil {
		t.Error("Dev's work items should load")
	}
	if _, cmd := v.Update(press("j")); cmd != nil {
		t.Error("Prod's work items are already loaded")
	}

	var opened string
	v.r.Open = func(u string) error { opened = u; return nil }
	v.Update(press("tab"))
	if !v.onItems {
		t.Fatal("tab moves to the work items")
	}
	v.Update(press("o"))
	if opened != "https://tfs/wi/501" {
		t.Errorf("o opened %q", opened)
	}
	_, cmd := v.Update(press("enter"))
	if push, ok := run(cmd).(PushMsg); !ok {
		t.Errorf("enter: %#v", run(cmd))
	} else if d, ok := push.Screen.(*WorkItemDetail); !ok || d.id != 501 {
		t.Errorf("enter pushed %#v", push.Screen)
	}
	v.Update(press("j"))
	if w, _ := v.selectedItem(); w.ID != 502 {
		t.Errorf("j on the work items selects %d", w.ID)
	}
	if s, _ := v.selectedStage(); s.Name != "Prod" {
		t.Errorf("moving in the work items changed the stage to %q", s.Name)
	}
	// Approving still acts on the stage.
	if _, cmd := v.Update(press("a")); cmd == nil {
		t.Error("a should approve Prod from the work item list too")
	}
}

func TestApprovalsListFilterAndConfirm(t *testing.T) {
	l := NewApprovalsList(&Releases{Patterns: []string{"front*"}})
	l.SetSize(120, 10)
	l.Update(approvalsLoadedMsg{screen: l.id, approvals: []domain.ReleaseApproval{
		{ID: 1, Pipeline: "frontend", Release: "Release-11", Stage: "Prod", Type: "preDeploy"},
		{ID: 2, Pipeline: "backend", Release: "Release-3", Stage: "Prod"},
	}})
	view := ansi.Strip(l.View())
	if !strings.Contains(view, "frontend") || strings.Contains(view, "backend") {
		t.Errorf("pattern filter:\n%s", view)
	}
	_, cmd := l.Update(press("a"))
	push, ok := run(cmd).(PushMsg)
	if !ok {
		t.Fatalf("approve should ask for confirmation, got %#v", run(cmd))
	}
	c, ok := push.Screen.(*Confirm)
	if !ok || !strings.Contains(c.detail, "frontend · Release-11 → Prod (before deployment)") {
		t.Errorf("confirm screen: %#v", push.Screen)
	}
}
