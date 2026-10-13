package tui

import (
	"context"
	"fmt"
	"strconv"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

var (
	keyCheckout   = key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "checkout"))
	keyPRWorktree = key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "worktree"))
)

type pullRequestsLoadedMsg struct {
	screen int64
	prs    []domain.PullRequest
	err    error
}

// PullRequestList lists pull requests.
type PullRequestList struct {
	id    int64
	title string
	act   *Actions
	load  func(context.Context) ([]domain.PullRequest, error)
	prs   map[int]domain.PullRequest
	table Table

	// Picked receives the chosen id in picker mode.
	Picked *int
}

// NewPullRequestList creates a pull request list.
func NewPullRequestList(title string, act *Actions, load func(context.Context) ([]domain.PullRequest, error)) *PullRequestList {
	t := NewTable([]Column{
		{Title: "ID", Max: 8}, {Title: "REPO", Max: 24}, {Title: "AUTHOR", Max: 22},
		{Title: "APPROVAL", Max: 24}, {Title: "TARGET", Max: 20}, {Title: "TITLE", Flex: true},
	})
	t.SetEmptyText("Loading…")
	return &PullRequestList{id: nextID(), title: title, act: act, load: load, table: t, prs: map[int]domain.PullRequest{}}
}

// NewPullRequestPicker creates a list that quits with the chosen id stored in *picked.
func NewPullRequestPicker(title string, act *Actions, load func(context.Context) ([]domain.PullRequest, error), picked *int) *PullRequestList {
	l := NewPullRequestList(title, act, load)
	l.Picked = picked
	return l
}

func (l *PullRequestList) reload() tea.Cmd {
	return Busy("loading pull requests", func() tea.Msg {
		prs, err := l.load(context.Background())
		return pullRequestsLoadedMsg{screen: l.id, prs: prs, err: err}
	})
}

func (l *PullRequestList) Init() tea.Cmd    { return l.reload() }
func (l *PullRequestList) Title() string    { return l.title }
func (l *PullRequestList) Capturing() bool  { return l.table.Filtering() }
func (l *PullRequestList) SetSize(w, h int) { l.table.SetSize(w, h) }
func (l *PullRequestList) View() string     { return l.table.View() }

func (l *PullRequestList) Help() []key.Binding {
	if l.Picked != nil {
		return append([]key.Binding{keyPick}, l.table.Help()[:3]...)
	}
	return append([]key.Binding{keyEnter, keyOpen, keyCheckout, keyPRWorktree, keyCopy, keyRefresh}, l.table.Help()...)
}

func (l *PullRequestList) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case pullRequestsLoadedMsg:
		if msg.screen != l.id {
			return l, nil
		}
		if msg.err != nil {
			l.table.SetEmptyText("Failed to load.")
			return l, StatusErr(msg.err)
		}
		l.table.SetEmptyText("No pull requests match.")
		l.prs = map[int]domain.PullRequest{}
		rows := make([]Row, len(msg.prs))
		for i, p := range msg.prs {
			l.prs[p.ID] = p
			title := p.Title
			if p.IsDraft {
				title = "[draft] " + title
			}
			rows[i] = Row{
				Key:    p.ID,
				Cells:  []string{strconv.Itoa(p.ID), p.Repository, p.CreatedBy, p.Approval.String(), p.TargetBranch, title},
				Styles: []lipgloss.Style{ui.ID, ui.Accent, ui.Muted, ui.ApprovalStyle(p.Approval), ui.Muted, {}},
			}
		}
		l.table.SetRows(rows)
		return l, nil
	case tea.KeyPressMsg:
		if l.table.Filtering() {
			break
		}
		var pr domain.PullRequest
		r, ok := l.table.Selected()
		if ok {
			pr = l.prs[r.Key]
		}
		if l.Picked != nil {
			if key.Matches(msg, keyPick) && ok {
				*l.Picked = pr.ID
				return l, tea.Quit
			}
			break
		}
		switch {
		case key.Matches(msg, keyRefresh):
			return l, l.reload()
		case !ok:
		case key.Matches(msg, keyEnter):
			return l, Push(NewPullRequestDetail(l.act, pr))
		case key.Matches(msg, keyOpen):
			return l, l.act.open(pr.URL)
		case key.Matches(msg, keyCopy):
			return l, copyURL(pr.URL)
		case key.Matches(msg, keyCheckout):
			return l, l.act.checkout(pr, false)
		case key.Matches(msg, keyPRWorktree):
			return l, l.act.checkout(pr, true)
		}
	}
	var cmd tea.Cmd
	l.table, cmd = l.table.Update(msg)
	return l, cmd
}

// PullRequestDetail shows one pull request.
type PullRequestDetail struct {
	act   *Actions
	pr    domain.PullRequest
	vp    viewport.Model
	width int
}

// NewPullRequestDetail creates a pull request detail screen.
func NewPullRequestDetail(act *Actions, pr domain.PullRequest) *PullRequestDetail {
	return &PullRequestDetail{act: act, pr: pr, vp: viewport.New()}
}

func (d *PullRequestDetail) Init() tea.Cmd   { return nil }
func (d *PullRequestDetail) Title() string   { return fmt.Sprintf("!%d", d.pr.ID) }
func (d *PullRequestDetail) Capturing() bool { return false }
func (d *PullRequestDetail) View() string    { return d.vp.View() }
func (d *PullRequestDetail) Help() []key.Binding {
	return []key.Binding{keyOpen, keyCheckout, keyPRWorktree, keyCopy, keyUp, keyDown}
}

func (d *PullRequestDetail) SetSize(w, h int) {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
	if w != d.width {
		d.width = w
		d.vp.SetContent(ui.RenderPullRequest(d.pr, w, true))
	}
}

func (d *PullRequestDetail) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, keyOpen):
			return d, d.act.open(d.pr.URL)
		case key.Matches(km, keyCopy):
			return d, copyURL(d.pr.URL)
		case key.Matches(km, keyCheckout):
			return d, d.act.checkout(d.pr, false)
		case key.Matches(km, keyPRWorktree):
			return d, d.act.checkout(d.pr, true)
		}
	}
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return d, cmd
}
