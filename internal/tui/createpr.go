package tui

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// PullRequestPrep is a new pull request for the current branch, prefilled by the CLI.
type PullRequestPrep struct {
	Draft      domain.PullRequestDraft
	Commits    string // the branch's commits not on the target, as a Markdown list
	Repository string
	Warning    string // e.g. why the branch's work item is not linked
}

// pullRequestCreatedMsg announces a new pull request to every screen.
type pullRequestCreatedMsg struct{ pr domain.PullRequest }

// CreatePullRequest is a form for creating a pull request from the current branch.
type CreatePullRequest struct {
	prep      PullRequestPrep
	form      *huh.Form
	title     string
	desc      string
	target    string
	workItems string
	draft     bool
	commits   bool
}

// NewCreatePullRequest creates the form, prefilled from prep.
func NewCreatePullRequest(act *Actions, prep PullRequestPrep) *CreatePullRequest {
	d := prep.Draft
	c := &CreatePullRequest{prep: prep, title: d.Title, desc: d.Description, target: d.TargetBranch,
		workItems: domain.FormatWorkItemIDs(d.WorkItemIDs), draft: d.IsDraft}
	fields := []huh.Field{
		huh.NewInput().Title("Title").Value(&c.title).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("title is required")
			}
			return nil
		}),
		huh.NewText().Title("Description").Description("Markdown (optional)").Lines(5).Value(&c.desc),
		huh.NewInput().Title("Target branch").Value(&c.target).Validate(func(s string) error {
			switch s = strings.TrimSpace(s); s {
			case "":
				return errors.New("target branch is required")
			case d.SourceBranch:
				return errors.New("target is the source branch")
			}
			return nil
		}),
		huh.NewInput().Title("Work items").Description("Comma-separated ids to link (optional)").
			Value(&c.workItems).Validate(func(s string) error {
			_, err := domain.ParseWorkItemIDs(s)
			return err
		}),
	}
	if prep.Commits != "" {
		n := strings.Count(prep.Commits, "\n") + 1
		fields = append(fields, huh.NewConfirm().Title("Add the branch's commits to the description?").
			Description(fmt.Sprintf("%d commit(s) not on %s, as a Markdown list after the description", n, d.TargetBranch)).
			Value(&c.commits))
	}
	fields = append(fields, huh.NewConfirm().Title("Draft?").Value(&c.draft))
	c.form = huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithShowHelp(true)
	// Read the values when the form completes, not when it is built.
	c.form.SubmitCmd = func() tea.Msg {
		ids, _ := domain.ParseWorkItemIDs(c.workItems)
		draft := d
		draft.Title = strings.TrimSpace(c.title)
		draft.Description = strings.TrimSpace(c.desc)
		if c.commits {
			draft.Description = domain.JoinDescription(draft.Description, prep.Commits)
		}
		draft.TargetBranch = strings.TrimSpace(c.target)
		draft.WorkItemIDs = ids
		draft.IsDraft = c.draft
		return CmdMsg{tea.Sequence(Pop(), act.createPR(draft))}
	}
	c.form.CancelCmd = Pop()
	return c
}

func (c *CreatePullRequest) Init() tea.Cmd       { return c.form.Init() }
func (c *CreatePullRequest) Title() string       { return "New pull request" }
func (c *CreatePullRequest) Capturing() bool     { return true }
func (c *CreatePullRequest) Help() []key.Binding { return []key.Binding{keyCancel} }

func (c *CreatePullRequest) SetSize(w, h int) {
	c.form = c.form.WithWidth(max(20, w-2)).WithHeight(max(5, h-3))
}

func (c *CreatePullRequest) View() string {
	header := fmt.Sprintf("From %s in %s (pushed first if the remote is behind)\n",
		ui.Accent.Render(c.prep.Draft.SourceBranch), c.prep.Repository)
	if c.prep.Warning != "" {
		header += ui.Muted.Render("Note: "+c.prep.Warning) + "\n"
	}
	return header + "\n" + c.form.View()
}

func (c *CreatePullRequest) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, keyCancel) {
		return c, Pop()
	}
	m, cmd := c.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		c.form = f
	}
	return c, cmd
}
