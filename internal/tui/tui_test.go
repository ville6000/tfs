package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

func press(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func typeText(t Table, s string) Table {
	for _, r := range s {
		t, _ = t.Update(press(string(r)))
	}
	return t
}

func TestTableFilter(t *testing.T) {
	tb := NewTable([]Column{{Title: "ID"}, {Title: "TITLE", Flex: true}})
	tb.SetSize(60, 10)
	tb.SetRows([]Row{
		{Key: 1, Cells: []string{"1", "Login page"}},
		{Key: 2, Cells: []string{"2", "Billing"}},
		{Key: 3, Cells: []string{"3", "Login API"}},
	})
	tb, _ = tb.Update(press("/"))
	if !tb.Filtering() {
		t.Fatal("expected filtering")
	}
	tb = typeText(tb, "login api")
	if len(tb.rows) != 1 || tb.rows[0].Key != 3 {
		t.Fatalf("rows = %+v", tb.rows)
	}
	tb, _ = tb.Update(press("enter"))
	if tb.Filtering() {
		t.Error("enter should leave filter input")
	}
	tb, _ = tb.Update(press("esc"))
	if len(tb.rows) != 3 {
		t.Errorf("esc should clear the filter: %d rows", len(tb.rows))
	}
	tb, _ = tb.Update(press("G"))
	if r, _ := tb.Selected(); r.Key != 3 {
		t.Errorf("G should select last, got %d", r.Key)
	}
	// Reload keeps the selection on the same key.
	tb.SetRows([]Row{{Key: 3, Cells: []string{"3", "x"}}, {Key: 1, Cells: []string{"1", "y"}}})
	if r, _ := tb.Selected(); r.Key != 3 {
		t.Errorf("selection lost after reload: %d", r.Key)
	}
	view := ansi.Strip(tb.View())
	if !strings.Contains(view, "TITLE") || !strings.Contains(view, "1/2") {
		t.Errorf("view:\n%s", view)
	}
}

func sampleTree() domain.Tree {
	return domain.BuildForest([]domain.WorkItem{
		{ID: 1, Type: "Epic", Title: "E"},
		{ID: 2, Type: "Feature", Title: "F", ParentID: 1},
		{ID: 3, Type: "Task", Title: "T", ParentID: 2},
		{ID: 4, Type: "Bug", Title: "B"},
	}, domain.NewFilter(nil, nil, nil, nil))
}

func TestTreeCollapseExpand(t *testing.T) {
	tv := NewTreeView("t", &Actions{}, func(context.Context) (domain.Tree, error) { return sampleTree(), nil })
	tv.SetSize(80, 20)
	tree := sampleTree()
	tv.Update(treeLoadedMsg{screen: tv.id, tree: tree})
	if len(tv.entries) != 5 { // 1,2,3, No parent, 4
		t.Fatalf("entries = %d", len(tv.entries))
	}
	tv.Update(press("j")) // on #2
	tv.Update(press("left"))
	if len(tv.entries) != 4 || !tv.collapsed[2] {
		t.Fatalf("collapse #2: %d entries", len(tv.entries))
	}
	tv.Update(press("left")) // already collapsed: jump to parent
	if tv.entries[tv.cursor].key != 1 {
		t.Errorf("cursor should move to parent, at %d", tv.entries[tv.cursor].key)
	}
	tv.Update(press("z"))
	if len(tv.entries) != 2 {
		t.Errorf("collapse all: %d entries", len(tv.entries))
	}
	tv.Update(press("e"))
	if len(tv.entries) != 5 {
		t.Errorf("expand all: %d entries", len(tv.entries))
	}
	view := ansi.Strip(tv.View())
	if !strings.Contains(view, "No parent") || !strings.Contains(view, "└─") {
		t.Errorf("view:\n%s", view)
	}
}

type stubScreen struct {
	title string
	got   []tea.Msg
}

func (s *stubScreen) Init() tea.Cmd                      { return nil }
func (s *stubScreen) Update(m tea.Msg) (Screen, tea.Cmd) { s.got = append(s.got, m); return s, nil }
func (s *stubScreen) View() string                       { return s.title }
func (s *stubScreen) SetSize(int, int)                   {}
func (s *stubScreen) Title() string                      { return s.title }
func (s *stubScreen) Help() []key.Binding                { return nil }
func (s *stubScreen) Capturing() bool                    { return false }

func TestAppStackAndBroadcast(t *testing.T) {
	root, child := &stubScreen{title: "root"}, &stubScreen{title: "child"}
	app := NewApp(root)
	app.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	app.Update(PushMsg{child})
	if app.top() != child {
		t.Fatal("push failed")
	}
	type custom struct{}
	app.Update(custom{})
	if len(root.got) != 1 || len(child.got) != 1 {
		t.Errorf("broadcast: root=%d child=%d", len(root.got), len(child.got))
	}
	app.Update(press("x"))
	if len(root.got) != 1 || len(child.got) != 2 {
		t.Error("keys must only reach the top screen")
	}
	app.Update(press("esc"))
	if app.top() != root {
		t.Error("esc should pop")
	}
	if _, cmd := app.Update(press("q")); cmd == nil {
		t.Error("q should quit")
	}
	view := ansi.Strip(app.View().Content)
	if !strings.Contains(view, "root") {
		t.Errorf("view:\n%s", view)
	}
}

func TestTreeInsertsCreatedChild(t *testing.T) {
	tv := NewTreeView("t", &Actions{}, nil)
	tv.SetSize(80, 20)
	tv.Update(treeLoadedMsg{screen: tv.id, tree: sampleTree()})
	tv.Update(press("z")) // collapse everything; creation must expand the parent
	tv.Update(workItemCreatedMsg{item: domain.WorkItem{ID: 9, Type: "Feature", Title: "New"}, parentID: 1})
	sel, ok := tv.selected()
	if !ok || sel.ID != 9 {
		t.Fatalf("new child should be selected, got %+v", sel)
	}
	if tv.collapsed[1] {
		t.Error("parent should be expanded")
	}
	if !strings.Contains(ansi.Strip(tv.View()), "#9 Feature") {
		t.Errorf("view:\n%s", ansi.Strip(tv.View()))
	}
}
