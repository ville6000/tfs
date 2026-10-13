package tui

import (
	"context"
	"strconv"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

var (
	keyEnter    = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details"))
	keyOpen     = key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open in browser"))
	keyState    = key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "set state"))
	keyAssign   = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "assign to me"))
	keyWorktree = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "worktree"))
	keyComment  = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "comment"))
	keyCopy     = key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy url"))
	keyRefresh  = key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh"))
	keyPick     = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "choose"))
	keyNewChild = key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "new child"))
)

type workItemsLoadedMsg struct {
	screen int64
	items  []domain.WorkItem
	err    error
}

// WorkItemList lists work items.
type WorkItemList struct {
	id     int64
	title  string
	act    *Actions
	load   func(context.Context) ([]domain.WorkItem, error)
	items  map[int]domain.WorkItem
	table  Table
	loaded bool

	// Picked receives the chosen id in picker mode (nil for a browsing list).
	Picked *int
}

// NewWorkItemList creates a work item list; load is called on start and refresh.
func NewWorkItemList(title string, act *Actions, load func(context.Context) ([]domain.WorkItem, error)) *WorkItemList {
	t := NewTable([]Column{
		{Title: "ID", Max: 8}, {Title: "TYPE", Max: 20}, {Title: "STATE", Max: 14},
		{Title: "ASSIGNED", Max: 22}, {Title: "TITLE", Flex: true},
	})
	t.SetEmptyText("Loading…")
	return &WorkItemList{id: nextID(), title: title, act: act, load: load, table: t, items: map[int]domain.WorkItem{}}
}

// NewWorkItemPicker creates a list that quits with the chosen id stored in *picked.
func NewWorkItemPicker(title string, act *Actions, load func(context.Context) ([]domain.WorkItem, error), picked *int) *WorkItemList {
	l := NewWorkItemList(title, act, load)
	l.Picked = picked
	return l
}

func (l *WorkItemList) reload() tea.Cmd {
	return Busy("loading work items", func() tea.Msg {
		items, err := l.load(context.Background())
		return workItemsLoadedMsg{screen: l.id, items: items, err: err}
	})
}

func workItemRow(w domain.WorkItem) Row {
	return Row{
		Key:    w.ID,
		Cells:  []string{strconv.Itoa(w.ID), w.Type, w.State, ui.OrDash(w.AssignedTo), w.Title},
		Styles: []lipgloss.Style{ui.ID, ui.TypeStyle(w.Type), ui.StateStyle(w.State), ui.Muted, {}},
	}
}

func (l *WorkItemList) Init() tea.Cmd    { return l.reload() }
func (l *WorkItemList) Title() string    { return l.title }
func (l *WorkItemList) Capturing() bool  { return l.table.Filtering() }
func (l *WorkItemList) SetSize(w, h int) { l.table.SetSize(w, h) }
func (l *WorkItemList) View() string     { return l.table.View() }

func (l *WorkItemList) Help() []key.Binding {
	if l.Picked != nil {
		return append([]key.Binding{keyPick}, l.table.Help()[:3]...)
	}
	return append([]key.Binding{keyEnter, keyOpen, keyState, keyAssign, keyWorktree, keyComment, keyCopy, keyRefresh}, l.table.Help()...)
}

func (l *WorkItemList) selected() (domain.WorkItem, bool) {
	r, ok := l.table.Selected()
	if !ok {
		return domain.WorkItem{}, false
	}
	w, ok := l.items[r.Key]
	return w, ok
}

func (l *WorkItemList) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case workItemsLoadedMsg:
		if msg.screen != l.id {
			return l, nil
		}
		if msg.err != nil {
			l.table.SetEmptyText("Failed to load.")
			return l, StatusErr(msg.err)
		}
		l.loaded = true
		l.table.SetEmptyText("No work items match.")
		l.items = map[int]domain.WorkItem{}
		rows := make([]Row, len(msg.items))
		for i, w := range msg.items {
			l.items[w.ID] = w
			rows[i] = workItemRow(w)
		}
		l.table.SetRows(rows)
		return l, nil
	case workItemChangedMsg:
		if _, ok := l.items[msg.item.ID]; ok {
			l.items[msg.item.ID] = msg.item
			l.table.UpdateRow(workItemRow(msg.item))
		}
		return l, nil
	case tea.KeyPressMsg:
		if l.table.Filtering() {
			break
		}
		w, ok := l.selected()
		if l.Picked != nil {
			if key.Matches(msg, keyPick) && ok {
				*l.Picked = w.ID
				return l, tea.Quit
			}
			break
		}
		switch {
		case key.Matches(msg, keyRefresh):
			return l, l.reload()
		case !ok:
		case key.Matches(msg, keyEnter):
			return l, Push(NewWorkItemDetail(l.act, w.ID))
		case key.Matches(msg, keyOpen):
			return l, l.act.open(w.URL)
		case key.Matches(msg, keyCopy):
			return l, copyURL(w.URL)
		case key.Matches(msg, keyState):
			return l, l.act.changeState(w)
		case key.Matches(msg, keyAssign):
			return l, l.act.assignToMe(w)
		case key.Matches(msg, keyWorktree):
			return l, l.act.worktree(w)
		case key.Matches(msg, keyComment):
			return l, l.act.comment(w.ID)
		}
	}
	var cmd tea.Cmd
	l.table, cmd = l.table.Update(msg)
	return l, cmd
}
