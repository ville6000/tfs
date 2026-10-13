package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/ville6000/tfs/internal/ado"
	"github.com/ville6000/tfs/internal/domain"
)

// Actions are the side effects screens can trigger.
type Actions struct {
	Client     *ado.Client
	Open       func(url string) error
	Worktree   func(ctx context.Context, w domain.WorkItem) (branch, path string, err error)
	CheckoutPR func(ctx context.Context, pr domain.PullRequest, worktree bool) (string, error)

	// Team resolves the team whose backlog is reordered in a project.
	Team func(ctx context.Context, project string) (string, error)

	mu         sync.Mutex
	categories map[string][]domain.TypeCategory // per project
	teams      map[string]string                // per project
}

// workItemChangedMsg announces an updated work item to every screen.
type workItemChangedMsg struct{ item domain.WorkItem }

// workItemCreatedMsg announces a new work item created as a child of parentID.
type workItemCreatedMsg struct {
	item     domain.WorkItem
	parentID int
}

// commentAddedMsg announces a new comment on a work item.
type commentAddedMsg struct{ id int }

// fallbackStates is offered when the server cannot list a type's states.
var fallbackStates = []string{"New", "Active", "Resolved", "Closed", "Removed", "To Do", "In Progress", "Done"}

func (a *Actions) open(url string) tea.Cmd {
	if err := a.Open(url); err != nil {
		return StatusErr(err)
	}
	return Status("opened " + url)
}

func copyURL(url string) tea.Cmd {
	return tea.Batch(tea.SetClipboard(url), Status("copied "+url))
}

func (a *Actions) changeState(w domain.WorkItem) tea.Cmd {
	return Busy("loading states", func() tea.Msg {
		states, err := a.Client.TypeStates(context.Background(), w.Project, w.Type)
		if err != nil || len(states) == 0 {
			states = fallbackStates
		}
		choice := NewChoice(fmt.Sprintf("State of #%d", w.ID), states, w.State, func(state string) tea.Cmd {
			return a.update(w.ID, "updating state", domain.WorkItemUpdate{State: &state})
		})
		return CmdMsg{Push(choice)}
	})
}

func (a *Actions) update(id int, label string, upd domain.WorkItemUpdate) tea.Cmd {
	return Busy(label, func() tea.Msg {
		item, err := a.Client.UpdateWorkItem(context.Background(), id, upd)
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		return CmdMsg{tea.Batch(
			func() tea.Msg { return workItemChangedMsg{item} },
			Status(fmt.Sprintf("#%d: %s · %s", item.ID, item.State, ui0(item.AssignedTo))),
		)}
	})
}

func ui0(s string) string {
	if s == "" {
		return "unassigned"
	}
	return s
}

func (a *Actions) assignToMe(w domain.WorkItem) tea.Cmd {
	return Busy("assigning", func() tea.Msg {
		me, err := a.Client.CurrentUser(context.Background())
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		return CmdMsg{a.update(w.ID, "assigning", domain.WorkItemUpdate{AssignedTo: &me})}
	})
}

func (a *Actions) worktree(w domain.WorkItem) tea.Cmd {
	if a.Worktree == nil {
		return nil
	}
	return Busy("creating worktree", func() tea.Msg {
		branch, path, err := a.Worktree(context.Background(), w)
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		return StatusMsg{Text: fmt.Sprintf("created worktree %s on branch %s", path, branch)}
	})
}

func (a *Actions) comment(id int) tea.Cmd {
	return Push(NewInput(fmt.Sprintf("Comment on #%d", id), func(text string) tea.Cmd {
		return Busy("posting comment", func() tea.Msg {
			if _, err := a.Client.AddComment(context.Background(), id, text); err != nil {
				return StatusMsg{Text: err.Error(), Err: true}
			}
			return CmdMsg{tea.Batch(
				func() tea.Msg { return commentAddedMsg{id} },
				Status(fmt.Sprintf("comment added to #%d", id)),
			)}
		})
	}))
}

func (a *Actions) checkout(pr domain.PullRequest, worktree bool) tea.Cmd {
	if a.CheckoutPR == nil {
		return nil
	}
	label := "checking out"
	if worktree {
		label = "creating worktree"
	}
	return Busy(label, func() tea.Msg {
		msg, err := a.CheckoutPR(context.Background(), pr, worktree)
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		return StatusMsg{Text: msg}
	})
}

// childTypes returns the types offered for a new child of parent, most likely first. Categories
// are fetched once per project; when they cannot be read a generic list is used.
func (a *Actions) childTypes(ctx context.Context, parent domain.WorkItem) []string {
	a.mu.Lock()
	cats, ok := a.categories[parent.Project]
	a.mu.Unlock()
	if !ok {
		var err error
		if cats, err = a.Client.TypeCategories(ctx, parent.Project); err != nil {
			cats = nil
		}
		a.mu.Lock()
		if a.categories == nil {
			a.categories = map[string][]domain.TypeCategory{}
		}
		a.categories[parent.Project] = cats
		a.mu.Unlock()
	}
	return domain.ChildTypeOptions(cats, parent.Type)
}

func (a *Actions) newChild(parent domain.WorkItem) tea.Cmd {
	return Busy("loading types", func() tea.Msg {
		types := a.childTypes(context.Background(), parent)
		return CmdMsg{Push(NewCreateChild(a, parent, types))}
	})
}

func (a *Actions) createChild(parent domain.WorkItem, typ, title string, assignToMe bool) tea.Cmd {
	return Busy("creating "+strings.ToLower(typ), func() tea.Msg {
		ctx := context.Background()
		draft := domain.WorkItemDraft{Project: parent.Project, Type: typ, Title: title, ParentID: parent.ID}
		if assignToMe {
			me, err := a.Client.CurrentUser(ctx)
			if err != nil {
				return StatusMsg{Text: err.Error(), Err: true}
			}
			draft.AssignedTo = me
		}
		if err := draft.Validate(nil); err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		item, err := a.Client.CreateWorkItem(ctx, draft)
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		if item.ParentID == 0 {
			item.ParentID = parent.ID
		}
		if item.Project == "" {
			item.Project = parent.Project
		}
		return CmdMsg{tea.Batch(
			func() tea.Msg { return workItemCreatedMsg{item: item, parentID: parent.ID} },
			Status(fmt.Sprintf("created %s #%d under #%d", item.Type, item.ID, parent.ID)),
		)}
	})
}

// reorder persists a backlog move and returns the new order values by id.
func (a *Actions) reorder(ctx context.Context, project string, op domain.ReorderOp) (map[int]float64, error) {
	a.mu.Lock()
	team, ok := a.teams[project]
	a.mu.Unlock()
	if !ok {
		if a.Team == nil {
			return nil, fmt.Errorf("no team configured for reordering")
		}
		var err error
		if team, err = a.Team(ctx, project); err != nil {
			return nil, err
		}
		a.mu.Lock()
		if a.teams == nil {
			a.teams = map[string]string{}
		}
		a.teams[project] = team
		a.mu.Unlock()
	}
	return a.Client.ReorderWorkItems(ctx, project, team, op)
}
