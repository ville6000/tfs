package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

var fixedNow = time.Date(2026, 10, 13, 9, 10, 0, 0, time.UTC)

func testBuilds() *Builds { return &Builds{now: func() time.Time { return fixedNow }} }

func TestBuildListRefreshesWhileRunning(t *testing.T) {
	l := NewBuildList(testBuilds(), "builds")
	l.SetSize(140, 10)
	_, cmd := l.Update(buildsLoadedMsg{screen: l.id, builds: []domain.Build{
		{ID: 2, Pipeline: "frontend-ci", Branch: "main", Status: "inProgress", Started: fixedNow.Add(-90 * time.Second)},
		{ID: 1, Pipeline: "frontend-ci", Branch: "main", Status: "completed", Result: "failed"},
	}})
	if cmd == nil || !l.ticking {
		t.Fatal("a running build should schedule a refresh")
	}
	view := ansi.Strip(l.View())
	for _, want := range []string{"● running", "1m30s", "✘ failed", "frontend-ci"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q:\n%s", want, view)
		}
	}
	// A second load while a refresh is pending must not start another timer.
	if _, cmd := l.Update(buildsLoadedMsg{screen: l.id, builds: []domain.Build{{ID: 2, Status: "inProgress"}}}); cmd != nil {
		t.Error("only one refresh timer at a time")
	}
	l.Update(buildTickMsg{screen: l.id})
	if _, cmd := l.Update(buildsLoadedMsg{screen: l.id, builds: []domain.Build{{ID: 2, Status: "completed", Result: "succeeded"}}}); cmd != nil || l.ticking {
		t.Error("finished builds should stop refreshing")
	}
}

func timeline() []domain.TimelineRecord {
	return []domain.TimelineRecord{
		{ID: "j", Type: "Job", Name: "Build", Order: 1, State: "completed", Result: "failed"},
		{ID: "t1", ParentID: "j", Type: "Task", Name: "Checkout", Order: 1, State: "completed", Result: "succeeded", LogID: 1},
		{ID: "t2", ParentID: "j", Type: "Task", Name: "npm test", Order: 2, State: "completed", Result: "failed", LogID: 2, Errors: 2,
			Issues: []domain.BuildIssue{{Type: "error", Message: "2 tests failed"}}},
		{ID: "t3", ParentID: "j", Type: "Task", Name: "Lint", Order: 3, State: "completed", Result: "failed", LogID: 3},
	}
}

func TestBuildViewFocusesFailures(t *testing.T) {
	v := NewBuildView(testBuilds(), 50)
	v.SetSize(120, 24)
	v.Update(buildLoadedMsg{screen: v.sid, build: domain.Build{ID: 50, Pipeline: "frontend-ci", Number: "20261013.4", Status: "completed", Result: "failed"}, timeline: timeline()})
	if sel, _ := v.table.Selected(); v.rows[sel.Key].Record.Name != "npm test" {
		t.Fatalf("should start on the first failed task, got %s", v.rows[sel.Key].Record.Name)
	}
	view := ansi.Strip(v.View())
	if !strings.Contains(view, "2 errors") || !strings.Contains(view, "2 tests failed") || !strings.Contains(view, "  Checkout") {
		t.Errorf("view:\n%s", view)
	}
	v.Update(press("f"))
	if sel, _ := v.table.Selected(); v.rows[sel.Key].Record.Name != "Lint" {
		t.Errorf("f should move to the next failure")
	}
	v.Update(press("f"))
	if sel, _ := v.table.Selected(); v.rows[sel.Key].Record.Name != "npm test" {
		t.Errorf("f should wrap around")
	}
	_, cmd := v.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	push, ok := run(cmd).(PushMsg)
	if !ok {
		t.Fatalf("enter should open the log, got %#v", run(cmd))
	}
	if lv, ok := push.Screen.(*LogView); !ok || lv.record.LogID != 2 {
		t.Errorf("log view for wrong record: %#v", push.Screen)
	}
}

func TestLogViewFormats(t *testing.T) {
	l := NewLogView(testBuilds(), domain.Build{ID: 50}, domain.TimelineRecord{Name: "npm test", LogID: 2})
	l.SetSize(80, 5)
	l.Update(logLoadedMsg{screen: l.sid, text: "2026-10-13T09:00:00Z start\n2026-10-13T09:00:01Z ##[error]2 tests failed\n"})
	view := ansi.Strip(l.View())
	if !strings.Contains(view, "✘ 2 tests failed") || strings.Contains(view, "2026-10-13T") {
		t.Errorf("log view:\n%s", view)
	}
}
