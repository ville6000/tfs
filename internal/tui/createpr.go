package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

// PullRequestPrep is a new pull request for the current branch, prefilled by the CLI.
type PullRequestPrep struct {
	Draft      domain.PullRequestDraft
	Commits    int // the branch's commits not on Draft.TargetBranch
	Repository string
	Warning    string // e.g. why the branch's work item is not linked
	// Create pushes the branch when needed and creates d, first adding the branch's commits
	// not on d's target to the description when addCommits. warning is set when the pull
	// request was created but something needs the user's attention.
	Create func(ctx context.Context, d domain.PullRequestDraft, addCommits bool) (pr domain.PullRequest, warning string, err error)
}

// pullRequestCreatedMsg announces a new pull request, created from the form with id form, to
// every screen.
type pullRequestCreatedMsg struct {
	form int64
	pr   domain.PullRequest
}

// pullRequestCreateFailedMsg tells the form with id form that creating its pull request failed.
type pullRequestCreateFailedMsg struct {
	form int64
	err  error
}

// CreatePullRequest is a form for creating a pull request from the current branch. It stays
// open while the pull request is created, and reopens with the values entered when that fails.
type CreatePullRequest struct {
	id         int64
	act        *Actions
	prep       PullRequestPrep
	form       *huh.Form
	width      int
	height     int
	submitting bool
	err        error // why the last create failed

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
	c := &CreatePullRequest{id: nextID(), act: act, prep: prep, title: d.Title, desc: d.Description,
		target: d.TargetBranch, workItems: domain.FormatWorkItemIDs(d.WorkItemIDs), draft: d.IsDraft}
	c.form = c.newForm()
	return c
}

// targetBranch is the target entered in the form.
func (c *CreatePullRequest) targetBranch() string { return domain.TrimRef(strings.TrimSpace(c.target)) }

// newForm builds the form over c's values, so a rebuilt form shows what was entered.
func (c *CreatePullRequest) newForm() *huh.Form {
	d := c.prep.Draft
	fields := []huh.Field{
		huh.NewInput().Title("Title").Value(&c.title).Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("title is required")
			}
			return nil
		}),
		huh.NewText().Title("Description").Description("Markdown (optional)").Lines(5).Value(&c.desc),
		huh.NewInput().Title("Target branch").Value(&c.target).Validate(func(s string) error {
			switch domain.TrimRef(strings.TrimSpace(s)) {
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
	if c.prep.Commits > 0 {
		// The count is against the target the form opened with; the list itself is read when
		// the pull request is created, against the target entered.
		describe := func() string {
			if t := c.targetBranch(); t != "" && t != d.TargetBranch {
				return fmt.Sprintf("The commits not on %s, as a Markdown list after the description", t)
			}
			return fmt.Sprintf("%d commit(s) not on %s, as a Markdown list after the description", c.prep.Commits, d.TargetBranch)
		}
		fields = append(fields, huh.NewConfirm().Title("Add the branch's commits to the description?").
			Description(describe()).DescriptionFunc(describe, &c.target).
			WithButtonAlignment(lipgloss.Left).Value(&c.commits))
	}
	fields = append(fields, huh.NewConfirm().Title("Draft?").WithButtonAlignment(lipgloss.Left).Value(&c.draft))
	f := huh.NewForm(huh.NewGroup(fields...)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithShowHelp(true)
	// Read the values when the form completes, not when it is built.
	f.SubmitCmd = func() tea.Msg {
		ids, _ := domain.ParseWorkItemIDs(c.workItems)
		draft := d
		draft.Title = strings.TrimSpace(c.title)
		draft.Description = strings.TrimSpace(c.desc)
		draft.TargetBranch = c.targetBranch()
		draft.WorkItemIDs = ids
		draft.IsDraft = c.draft
		return createPullRequestMsg{form: c.id, draft: draft, addCommits: c.commits}
	}
	f.CancelCmd = Pop()
	return f
}

// createPullRequestMsg is the form's submission, handled by the form itself so it knows the
// request is running.
type createPullRequestMsg struct {
	form       int64
	draft      domain.PullRequestDraft
	addCommits bool
}

func (c *CreatePullRequest) Init() tea.Cmd   { return c.form.Init() }
func (c *CreatePullRequest) Title() string   { return "New pull request" }
func (c *CreatePullRequest) Capturing() bool { return true }

func (c *CreatePullRequest) Help() []key.Binding {
	if c.submitting {
		return nil
	}
	return []key.Binding{keyCancel}
}

func (c *CreatePullRequest) SetSize(w, h int) {
	c.width, c.height = w, h
	c.layout()
}

// layout gives the form the height the header leaves.
func (c *CreatePullRequest) layout() {
	c.form = c.form.WithWidth(max(20, c.width-2)).WithHeight(max(5, c.height-len(c.headerLines())-1))
}

// headerLines describes the source branch and any warning or error, each kept to a few lines
// so the form below stays on screen.
func (c *CreatePullRequest) headerLines() []string {
	lines := []string{fmt.Sprintf("From %s in %s (pushed first if the remote is behind)",
		ui.Accent.Render(c.prep.Draft.SourceBranch), c.prep.Repository)}
	if c.prep.Warning != "" {
		lines = append(lines, styleLines(ui.Muted, clampLines("Note: "+c.prep.Warning, c.width, 2))...)
	}
	if c.err != nil {
		lines = append(lines, styleLines(ui.Danger, clampLines("Could not create the pull request: "+c.err.Error(), c.width, 3))...)
	}
	return lines
}

// clampLines joins s onto one line, wraps it to width and keeps at most n lines, ending the
// last with … when text was cut.
func clampLines(s string, width, n int) []string {
	s = strings.Join(strings.Fields(s), " ")
	if width <= 0 {
		return []string{s}
	}
	lines := strings.Split(ansi.Wrap(s, width, ""), "\n")
	if len(lines) > n {
		lines = lines[:n]
		lines[n-1] = ansi.Truncate(lines[n-1], width-1, "") + "…"
	}
	return lines
}

func styleLines(st lipgloss.Style, lines []string) []string {
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return lines
}

func (c *CreatePullRequest) View() string {
	header := strings.Join(c.headerLines(), "\n") + "\n\n"
	if c.submitting {
		return header + ui.Muted.Render("Creating the pull request…")
	}
	return header + c.form.View()
}

func (c *CreatePullRequest) Update(msg tea.Msg) (Screen, tea.Cmd) {
	switch msg := msg.(type) {
	case createPullRequestMsg:
		if msg.form != c.id {
			return c, nil
		}
		c.submitting, c.err = true, nil
		c.layout()
		return c, c.act.createPR(c.id, c.prep, msg.draft, msg.addCommits)
	case pullRequestCreatedMsg:
		if msg.form != c.id {
			return c, nil
		}
		return c, Pop()
	case pullRequestCreateFailedMsg:
		if msg.form != c.id {
			return c, nil
		}
		c.submitting, c.err = false, msg.err
		c.form = c.newForm()
		c.layout()
		return c, c.form.Init()
	case tea.KeyPressMsg:
		if !c.submitting && key.Matches(msg, keyCancel) {
			return c, Pop()
		}
	}
	if c.submitting {
		return c, nil // the form closes or reopens when the server answers
	}
	m, cmd := c.form.Update(msg)
	if f, ok := m.(*huh.Form); ok {
		c.form = f
	}
	return c, cmd
}
