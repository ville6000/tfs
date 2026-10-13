package tui

import (
	"context"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/output"
	"github.com/ville6000/tfs/internal/ui"
)

var (
	keyCollapse    = key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "collapse"))
	keyExpand      = key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "expand"))
	keyToggle      = key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "toggle"))
	keyExpandAll   = key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "expand all"))
	keyCollapseAll = key.NewBinding(key.WithKeys("z"), key.WithHelp("z", "collapse all"))
)

type treeLoadedMsg struct {
	screen int64
	tree   domain.Tree
	err    error
}

// noParentKey identifies the synthetic "No parent" group.
const noParentKey = -1

type treeEntry struct {
	key      int
	node     *domain.TreeNode // nil for the "No parent" group
	children int
	prefix   string
	parent   int // index of the parent entry, -1 at top level
}

// TreeView is a collapsible work item hierarchy.
type TreeView struct {
	id        int64
	title     string
	act       *Actions
	load      func(context.Context) (domain.Tree, error)
	tree      *domain.Tree
	collapsed map[int]bool
	entries   []treeEntry
	cursor    int
	offset    int
	width     int
	height    int
	err       error
}

// NewTreeView creates a tree screen.
func NewTreeView(title string, act *Actions, load func(context.Context) (domain.Tree, error)) *TreeView {
	return &TreeView{id: nextID(), title: title, act: act, load: load, collapsed: map[int]bool{}}
}

func (t *TreeView) reload() tea.Cmd {
	return Busy("loading hierarchy", func() tea.Msg {
		tree, err := t.load(context.Background())
		return treeLoadedMsg{screen: t.id, tree: tree, err: err}
	})
}

func (t *TreeView) flatten() {
	var keep int
	if t.cursor < len(t.entries) {
		keep = t.entries[t.cursor].key
	}
	t.entries = t.entries[:0]
	if t.tree == nil {
		return
	}
	var walk func(nodes []*domain.TreeNode, prefix string, parent int)
	walk = func(nodes []*domain.TreeNode, prefix string, parent int) {
		for i, n := range nodes {
			branch, next := "├─ ", "│  "
			if i == len(nodes)-1 {
				branch, next = "└─ ", "   "
			}
			p := prefix + branch
			if parent < 0 {
				p, next = "", ""
			}
			idx := len(t.entries)
			t.entries = append(t.entries, treeEntry{key: n.Item.ID, node: n, children: len(n.Children), prefix: p, parent: parent})
			if !t.collapsed[n.Item.ID] {
				walk(n.Children, prefix+next, idx)
			}
		}
	}
	walk(t.tree.Roots, "", -1)
	if len(t.tree.Orphans) > 0 {
		idx := len(t.entries)
		t.entries = append(t.entries, treeEntry{key: noParentKey, children: len(t.tree.Orphans), parent: -1})
		if !t.collapsed[noParentKey] {
			for i, n := range t.tree.Orphans {
				branch, next := "├─ ", "│  "
				if i == len(t.tree.Orphans)-1 {
					branch, next = "└─ ", "   "
				}
				e := len(t.entries)
				t.entries = append(t.entries, treeEntry{key: n.Item.ID, node: n, children: len(n.Children), prefix: branch, parent: idx})
				if !t.collapsed[n.Item.ID] {
					walk(n.Children, next, e)
				}
			}
		}
	}
	t.cursor = 0
	for i, e := range t.entries {
		if e.key == keep {
			t.cursor = i
		}
	}
	t.clamp()
}

func (t *TreeView) clamp() {
	t.cursor = max(0, min(t.cursor, len(t.entries)-1))
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	t.offset = max(0, min(t.offset, len(t.entries)-t.height))
}

func (t *TreeView) Init() tea.Cmd   { return t.reload() }
func (t *TreeView) Title() string   { return t.title }
func (t *TreeView) Capturing() bool { return false }
func (t *TreeView) SetSize(w, h int) {
	t.width, t.height = w, max(1, h)
	t.clamp()
}

func (t *TreeView) Help() []key.Binding {
	return []key.Binding{keyEnter, keyOpen, keyCollapse, keyExpand, keyToggle, keyState, keyAssign, keyWorktree,
		keyCopy, keyExpandAll, keyCollapseAll, keyRefresh, keyUp, keyDown, keyPgUp, keyPgDown}
}

func (t *TreeView) selected() (domain.WorkItem, bool) {
	if t.cursor >= len(t.entries) || t.entries[t.cursor].node == nil {
		return domain.WorkItem{}, false
	}
	return t.entries[t.cursor].node.Item, true
}

func (t *TreeView) setAll(collapsed bool) {
	t.collapsed = map[int]bool{}
	if !collapsed || t.tree == nil {
		return
	}
	var walk func([]*domain.TreeNode)
	walk = func(ns []*domain.TreeNode) {
		for _, n := range ns {
			if len(n.Children) > 0 {
				t.collapsed[n.Item.ID] = true
			}
			walk(n.Children)
		}
	}
	walk(t.tree.Roots)
	walk(t.tree.Orphans)
	t.collapsed[noParentKey] = true
}

func replaceItem(nodes []*domain.TreeNode, w domain.WorkItem) {
	for _, n := range nodes {
		if n.Item.ID == w.ID {
			n.Item = w
		}
		replaceItem(n.Children, w)
	}
}

func (t *TreeView) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case treeLoadedMsg:
		if msg.screen != t.id {
			return t, nil
		}
		if msg.err != nil {
			t.err = msg.err
			return t, StatusErr(msg.err)
		}
		t.err = nil
		t.tree = &msg.tree
		t.flatten()
		return t, nil
	case workItemChangedMsg:
		if t.tree != nil {
			replaceItem(t.tree.Roots, msg.item)
			replaceItem(t.tree.Orphans, msg.item)
		}
		return t, nil
	case tea.KeyPressMsg:
		if len(t.entries) == 0 {
			if key.Matches(msg, keyRefresh) {
				return t, t.reload()
			}
			return t, nil
		}
		e := t.entries[t.cursor]
		w, isItem := t.selected()
		switch {
		case key.Matches(msg, keyUp):
			t.cursor--
		case key.Matches(msg, keyDown):
			t.cursor++
		case key.Matches(msg, keyPgUp):
			t.cursor -= t.height
		case key.Matches(msg, keyPgDown):
			t.cursor += t.height
		case key.Matches(msg, keyTop):
			t.cursor = 0
		case key.Matches(msg, keyBottom):
			t.cursor = len(t.entries) - 1
		case key.Matches(msg, keyCollapse):
			if e.children > 0 && !t.collapsed[e.key] {
				t.collapsed[e.key] = true
				t.flatten()
			} else if e.parent >= 0 {
				t.cursor = e.parent
			}
		case key.Matches(msg, keyExpand):
			if e.children > 0 && t.collapsed[e.key] {
				delete(t.collapsed, e.key)
				t.flatten()
			} else if e.children > 0 {
				t.cursor++
			}
		case key.Matches(msg, keyToggle):
			if e.children > 0 {
				t.collapsed[e.key] = !t.collapsed[e.key]
				t.flatten()
			}
		case key.Matches(msg, keyExpandAll):
			t.setAll(false)
			t.flatten()
		case key.Matches(msg, keyCollapseAll):
			t.setAll(true)
			t.flatten()
		case key.Matches(msg, keyRefresh):
			return t, t.reload()
		case !isItem:
		case key.Matches(msg, keyEnter):
			return t, Push(NewWorkItemDetail(t.act, w.ID))
		case key.Matches(msg, keyOpen):
			return t, t.act.open(w.URL)
		case key.Matches(msg, keyCopy):
			return t, copyURL(w.URL)
		case key.Matches(msg, keyState):
			return t, t.act.changeState(w)
		case key.Matches(msg, keyAssign):
			return t, t.act.assignToMe(w)
		case key.Matches(msg, keyWorktree):
			return t, t.act.worktree(w)
		}
		t.clamp()
	}
	return t, nil
}

func (t *TreeView) View() string {
	if t.err != nil {
		return ui.Danger.Render(t.err.Error())
	}
	if t.tree == nil {
		return ui.Muted.Render("  Loading…")
	}
	if len(t.entries) == 0 {
		return ui.Muted.Render("  No work items match.")
	}
	var b strings.Builder
	end := min(len(t.entries), t.offset+t.height)
	for i := t.offset; i < end; i++ {
		e := t.entries[i]
		marker := "  "
		if i == t.cursor {
			marker = ui.Accent.Render("▌ ")
		}
		toggle := "  "
		if e.children > 0 {
			toggle = "▾ "
			if t.collapsed[e.key] {
				toggle = "▸ "
			}
		}
		var label string
		if e.node == nil {
			label = ui.Bold.Render(output.NoParentLabel)
		} else {
			label = output.TreeLine(e.node)
			if i == t.cursor {
				label = ui.Bold.Render(label)
			}
		}
		if t.collapsed[e.key] && e.children > 0 {
			label += ui.Muted.Render(" (+" + strconv.Itoa(e.children) + ")")
		}
		line := marker + ui.Muted.Render(e.prefix) + ui.Accent.Render(toggle) + label
		b.WriteString(ansi.Truncate(line, t.width, "…") + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
