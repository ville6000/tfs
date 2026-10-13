package tui

import (
	"context"
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

var keyParent = key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "parent"))

type detailLoadedMsg struct {
	screen int64
	detail domain.WorkItemDetail
	err    error
}

// WorkItemDetail shows one work item with its description and comments.
type WorkItemDetail struct {
	sid    int64
	id     int
	act    *Actions
	detail *domain.WorkItemDetail
	vp     viewport.Model
	width  int
}

// NewWorkItemDetail creates a detail screen for a work item.
func NewWorkItemDetail(act *Actions, id int) *WorkItemDetail {
	return &WorkItemDetail{sid: nextID(), id: id, act: act, vp: viewport.New()}
}

func (d *WorkItemDetail) reload() tea.Cmd {
	return Busy(fmt.Sprintf("loading #%d", d.id), func() tea.Msg {
		det, err := d.act.Client.GetWorkItemDetail(context.Background(), d.id)
		return detailLoadedMsg{screen: d.sid, detail: det, err: err}
	})
}

func (d *WorkItemDetail) render() {
	if d.detail == nil {
		d.vp.SetContent(ui.Muted.Render("Loading…"))
		return
	}
	d.vp.SetContent(ui.RenderWorkItem(*d.detail, d.act.Client.Collection(), d.width, true))
}

func (d *WorkItemDetail) Init() tea.Cmd   { d.render(); return d.reload() }
func (d *WorkItemDetail) Title() string   { return fmt.Sprintf("#%d", d.id) }
func (d *WorkItemDetail) Capturing() bool { return false }
func (d *WorkItemDetail) View() string    { return d.vp.View() }
func (d *WorkItemDetail) SetSize(w, h int) {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
	if w != d.width {
		d.width = w
		d.render()
	}
}

func (d *WorkItemDetail) Help() []key.Binding {
	return []key.Binding{keyOpen, keyComment, keyState, keyAssign, keyWorktree, keyParent, keyCopy, keyRefresh, keyUp, keyDown}
}

func (d *WorkItemDetail) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case detailLoadedMsg:
		if msg.screen != d.sid {
			return d, nil
		}
		if msg.err != nil {
			d.vp.SetContent(ui.Danger.Render(msg.err.Error()))
			return d, StatusErr(msg.err)
		}
		d.detail = &msg.detail
		d.render()
		return d, nil
	case workItemChangedMsg:
		if msg.item.ID == d.id && d.detail != nil {
			d.detail.WorkItem = msg.item
			d.render()
		}
		return d, nil
	case commentAddedMsg:
		if msg.id == d.id {
			return d, d.reload()
		}
		return d, nil
	case tea.KeyPressMsg:
		if d.detail == nil {
			break
		}
		w := d.detail.WorkItem
		switch {
		case key.Matches(msg, keyOpen):
			return d, d.act.open(w.URL)
		case key.Matches(msg, keyCopy):
			return d, copyURL(w.URL)
		case key.Matches(msg, keyComment):
			return d, d.act.comment(w.ID)
		case key.Matches(msg, keyState):
			return d, d.act.changeState(w)
		case key.Matches(msg, keyAssign):
			return d, d.act.assignToMe(w)
		case key.Matches(msg, keyWorktree):
			return d, d.act.worktree(w)
		case key.Matches(msg, keyRefresh):
			return d, d.reload()
		case key.Matches(msg, keyParent):
			if w.HasParent() {
				return d, Push(NewWorkItemDetail(d.act, w.ParentID))
			}
			return d, Status("no parent")
		}
	}
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return d, cmd
}
