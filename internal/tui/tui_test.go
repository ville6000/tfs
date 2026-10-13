package tui

import (
	"context"
	"errors"
	"strconv"
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

func TestTreeAssigneeColumn(t *testing.T) {
	tree := domain.BuildForest([]domain.WorkItem{
		{ID: 1, Type: "Epic", Title: "Short", AssignedTo: "Alice"},
		{ID: 2, Type: "Feature", Title: "A much longer feature title", ParentID: 1, AssignedTo: "Bob"},
		{ID: 3, Type: "Task", Title: "Unassigned", ParentID: 2},
	}, domain.NewFilter(nil, nil, nil, nil))
	tv := NewTreeView("t", &Actions{}, nil)
	tv.SetSize(100, 10)
	tv.Update(treeLoadedMsg{screen: tv.id, tree: tree})
	lines := strings.Split(ansi.Strip(tv.View()), "\n")
	col := func(line, name string) int { // display column, not byte offset
		i := strings.Index(line, name)
		if i < 0 {
			return -1
		}
		return ansi.StringWidth(line[:i])
	}
	a, b := col(lines[0], "Alice"), col(lines[1], "Bob")
	if a < 0 || a != b {
		t.Fatalf("assignees not aligned (%d, %d):\n%s", a, b, strings.Join(lines, "\n"))
	}
	if !strings.HasSuffix(strings.TrimRight(lines[2], " "), "Unassigned") {
		t.Errorf("unassigned row should end with its title: %q", lines[2])
	}

	// Narrow: titles are truncated before the assignee is dropped.
	tv.SetSize(52, 10)
	lines = strings.Split(ansi.Strip(tv.View()), "\n")
	if !strings.HasSuffix(lines[1], "Bob") || !strings.Contains(lines[1], "…") {
		t.Errorf("narrow line: %q", lines[1])
	}

	// Very narrow: no room for the column at all.
	tv.SetSize(30, 10)
	if strings.Contains(ansi.Strip(tv.View()), "Alice") {
		t.Errorf("assignee should be dropped when too narrow:\n%s", ansi.Strip(tv.View()))
	}
}

func orderedTree() domain.Tree {
	tree := domain.BuildForest([]domain.WorkItem{
		{ID: 1, Type: "Epic", Title: "A", Order: 10, Project: "P"},
		{ID: 2, Type: "Epic", Title: "B", Order: 20, Project: "P"},
		{ID: 3, Type: "Epic", Title: "C", Order: 30, Project: "P"},
	}, domain.NewFilter(nil, nil, nil, nil))
	tree.Ordered = true
	return tree
}

func entryIDs(tv *TreeView) string {
	var s []string
	for _, e := range tv.entries {
		s = append(s, strconv.Itoa(e.key))
	}
	return strings.Join(s, ",")
}

func TestTreeMoveQueuesSaves(t *testing.T) {
	tv := NewTreeView("t", &Actions{}, nil)
	tv.SetSize(80, 20)
	tv.Update(treeLoadedMsg{screen: tv.id, tree: orderedTree()})
	tv.Update(press("G")) // #3

	if _, cmd := tv.Update(press("K")); cmd == nil || entryIDs(tv) != "1,3,2" || !tv.saving {
		t.Fatalf("K: %s saving=%v", entryIDs(tv), tv.saving)
	}
	if sel, _ := tv.selected(); sel.ID != 3 {
		t.Errorf("selection should follow the moved item, got #%d", sel.ID)
	}
	// A second move while the first is saving is applied locally and queued.
	tv.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift})
	if entryIDs(tv) != "3,1,2" || len(tv.pendingMoves) != 1 {
		t.Fatalf("shift+up: %s pending=%v", entryIDs(tv), tv.pendingMoves)
	}
	// First save completes: its order is recorded and the queued move is sent next.
	tv.Update(reorderDoneMsg{screen: tv.id, id: 3, orders: map[int]float64{3: 15}})
	if !tv.saving || len(tv.pendingMoves) != 0 || tv.tree.Roots[0].Item.Order != 15 {
		t.Fatalf("after first save: saving=%v pending=%v order=%v", tv.saving, tv.pendingMoves, tv.tree.Roots[0].Item.Order)
	}
	tv.Update(reorderDoneMsg{screen: tv.id, id: 3, orders: map[int]float64{3: 5}})
	if tv.saving {
		t.Error("queue should be drained")
	}

	tv.Update(press("J"))
	if entryIDs(tv) != "1,3,2" {
		t.Errorf("J: %s", entryIDs(tv))
	}
	tv.Update(press("J"))
	tv.Update(reorderDoneMsg{screen: tv.id, id: 3, err: errors.New("boom")})
	if tv.saving || len(tv.pendingMoves) != 0 {
		t.Errorf("error should clear the queue: saving=%v pending=%v", tv.saving, tv.pendingMoves)
	}
}

func TestTreeMoveNeedsBacklogOrder(t *testing.T) {
	tv := NewTreeView("t", &Actions{}, nil)
	tv.SetSize(80, 20)
	tree := orderedTree()
	tree.Ordered = false
	tv.Update(treeLoadedMsg{screen: tv.id, tree: tree})
	tv.Update(press("j"))
	tv.Update(press("K"))
	if entryIDs(tv) != "1,2,3" || tv.saving {
		t.Errorf("unordered tree must not move: %s", entryIDs(tv))
	}
}
