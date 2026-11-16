package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/ville6000/tfs/internal/domain"
	"github.com/ville6000/tfs/internal/ui"
)

var (
	keyApproveComplete = key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "approve + auto-complete"))
	keyAutoComplete    = key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "auto-complete"))
)

// pullRequestChangedMsg announces a pull request voted on or set to auto-complete to every
// screen.
type pullRequestChangedMsg struct{ pr domain.PullRequest }

// pullRequestSummary describes pr for a confirmation.
func pullRequestSummary(pr domain.PullRequest) string {
	return fmt.Sprintf("!%d %s\n%s · %s → %s", pr.ID, pr.Title, pr.Repository, pr.SourceBranch, pr.TargetBranch)
}

// changedPR announces pr, read again from the server when it can be, with status.
func (a *Actions) changedPR(ctx context.Context, pr domain.PullRequest, status StatusMsg) tea.Msg {
	if fresh, err := a.Client.GetPullRequest(ctx, pr.ID); err == nil {
		pr = fresh
	}
	return CmdMsg{tea.Batch(func() tea.Msg { return pullRequestChangedMsg{pr} }, func() tea.Msg { return status })}
}

// approvePR asks to approve pr, then approves it.
func (a *Actions) approvePR(pr domain.PullRequest) tea.Cmd {
	if err := pr.CheckOpen(); err != nil {
		return StatusErr(err)
	}
	return Push(NewQuestion(fmt.Sprintf("Approve !%d", pr.ID), fmt.Sprintf("Approve !%d?", pr.ID), pullRequestSummary(pr), "Approve", func() tea.Cmd {
		return Busy("approving", func() tea.Msg {
			ctx := context.Background()
			if err := a.Client.Vote(ctx, pr, domain.VoteApprove); err != nil {
				return StatusMsg{Text: err.Error(), Err: true}
			}
			return a.changedPR(ctx, pr, StatusMsg{Text: fmt.Sprintf("approved !%d", pr.ID)})
		})
	}))
}

// autoComplete opens the auto-complete form for pr, which also approves it when approve. Without
// approve, a pull request already set to auto-complete is offered to turn it off instead.
func (a *Actions) autoComplete(pr domain.PullRequest, approve bool) tea.Cmd {
	if err := pr.CheckOpen(); err != nil {
		return StatusErr(err)
	}
	return Busy(fmt.Sprintf("reading !%d", pr.ID), func() tea.Msg {
		ctx := context.Background()
		// A pull request from a list may lack its completion options, so it is read again.
		pr, err := a.Client.GetPullRequest(ctx, pr.ID)
		if err == nil {
			err = pr.CheckOpen()
		}
		if err != nil {
			return StatusMsg{Text: err.Error(), Err: true}
		}
		if !approve && pr.HasAutoComplete() {
			return CmdMsg{Push(a.turnOffAutoComplete(pr))}
		}
		allowed, err := a.Client.AllowedMergeStrategies(ctx, pr)
		note := ""
		if err != nil {
			note = fmt.Sprintf("Could not read the merge types %s allows, so every one is offered: %v", pr.TargetBranch, err)
		}
		return CmdMsg{Push(NewAutoCompleteForm(a, pr, allowed, approve, note))}
	})
}

// turnOffAutoComplete asks to turn off auto-complete of pr, then turns it off.
func (a *Actions) turnOffAutoComplete(pr domain.PullRequest) *Confirm {
	question := fmt.Sprintf("Turn off auto-complete of !%d?", pr.ID)
	detail := pullRequestSummary(pr) + fmt.Sprintf("\nauto-complete: %s · set by %s", pr.CompletionOptions.Summary(), pr.AutoCompleteSetBy)
	return NewQuestion(fmt.Sprintf("Auto-complete !%d", pr.ID), question, detail, "Turn off", func() tea.Cmd {
		return Busy("turning off auto-complete", func() tea.Msg {
			updated, err := a.Client.CancelAutoComplete(context.Background(), pr)
			if err != nil {
				return StatusMsg{Text: err.Error(), Err: true}
			}
			return CmdMsg{tea.Batch(func() tea.Msg { return pullRequestChangedMsg{updated} },
				Status(fmt.Sprintf("turned off auto-complete of !%d", pr.ID)))}
		})
	})
}

// setAutoComplete sets pr to auto-complete with opts, approving it first when approve.
func (a *Actions) setAutoComplete(pr domain.PullRequest, opts domain.CompletionOptions, approve bool) tea.Cmd {
	label := "setting auto-complete"
	if approve {
		label = "approving and setting auto-complete"
	}
	return Busy(label, func() tea.Msg {
		ctx := context.Background()
		done := fmt.Sprintf("set !%d to auto-complete: %s", pr.ID, opts.Summary())
		if approve {
			if err := a.Client.Vote(ctx, pr, domain.VoteApprove); err != nil {
				return StatusMsg{Text: err.Error(), Err: true}
			}
			done = fmt.Sprintf("approved !%d and set it to auto-complete: %s", pr.ID, opts.Summary())
		}
		updated, err := a.Client.SetAutoComplete(ctx, pr, opts)
		switch {
		case err != nil && approve:
			return a.changedPR(ctx, pr, StatusMsg{Text: fmt.Sprintf("approved !%d, but could not set auto-complete: %v", pr.ID, err), Err: true})
		case err != nil:
			return StatusMsg{Text: err.Error(), Err: true}
		}
		return CmdMsg{tea.Batch(func() tea.Msg { return pullRequestChangedMsg{updated} }, Status(done))}
	})
}

// What happens after merging, as chosen in the auto-complete form.
const (
	afterDeleteBranch      = "delete"
	afterCompleteWorkItems = "complete"
)

// AutoCompleteForm sets a pull request to auto-complete with the merge type and clean-up
// chosen, approving it first when made to. ctrl+s confirms from any field and esc cancels.
type AutoCompleteForm struct {
	act     *Actions
	pr      domain.PullRequest
	approve bool
	note    string
	form    *huh.Form
	yes     key.Binding
	width   int

	strategy domain.MergeStrategy
	after    []string
	ok       bool
}

// NewAutoCompleteForm creates the form for pr. allowed are the merge types the target branch's
// policies allow (none: every one); the choices start as the pull request has them. note says
// what the form could not find out.
func NewAutoCompleteForm(act *Actions, pr domain.PullRequest, allowed []domain.MergeStrategy, approve bool, note string) *AutoCompleteForm {
	f := &AutoCompleteForm{act: act, pr: pr, approve: approve, note: note,
		strategy: domain.PickMergeStrategy(allowed, pr.CompletionOptions.MergeStrategy)}
	if pr.CompletionOptions.DeleteSourceBranch {
		f.after = append(f.after, afterDeleteBranch)
	}
	if pr.CompletionOptions.CompleteWorkItems {
		f.after = append(f.after, afterCompleteWorkItems)
	}
	strategies, describe := allowed, fmt.Sprintf("Those the policies of %s allow", pr.TargetBranch)
	if len(strategies) == 0 {
		strategies, describe = domain.MergeStrategies, ""
	}
	options := make([]huh.Option[domain.MergeStrategy], len(strategies))
	for i, m := range strategies {
		options[i] = huh.NewOption(m.Label(), m)
	}
	verb, question := "Set auto-complete", fmt.Sprintf("Set !%d to auto-complete?", pr.ID)
	if approve {
		verb, question = "Approve", fmt.Sprintf("Approve !%d and set it to auto-complete?", pr.ID)
	}
	f.yes = key.NewBinding(key.WithKeys(keySubmit.Keys()...), key.WithHelp(keySubmit.Help().Key, strings.ToLower(verb)))
	f.form = huh.NewForm(huh.NewGroup(
		huh.NewSelect[domain.MergeStrategy]().Title("Merge type").Description(describe).Options(options...).Value(&f.strategy),
		huh.NewMultiSelect[string]().Title("After merging").Filterable(false).Options(
			huh.NewOption("Delete "+pr.SourceBranch, afterDeleteBranch),
			huh.NewOption("Complete linked work items", afterCompleteWorkItems),
		).Value(&f.after),
		huh.NewConfirm().Title(question).Affirmative(verb).Negative("Cancel").Value(&f.ok),
	)).WithTheme(huh.ThemeFunc(huh.ThemeCharm)).WithShowHelp(true)
	f.form.SubmitCmd = func() tea.Msg {
		if !f.ok {
			return CmdMsg{tea.Sequence(Pop(), Status("cancelled"))}
		}
		return CmdMsg{tea.Sequence(Pop(), f.submit())}
	}
	f.form.CancelCmd = Pop()
	return f
}

// options are the completion options chosen.
func (f *AutoCompleteForm) options() domain.CompletionOptions {
	o := f.pr.CompletionOptions
	o.MergeStrategy = f.strategy
	o.DeleteSourceBranch = slices.Contains(f.after, afterDeleteBranch)
	o.CompleteWorkItems = slices.Contains(f.after, afterCompleteWorkItems)
	return o
}

func (f *AutoCompleteForm) submit() tea.Cmd {
	return f.act.setAutoComplete(f.pr, f.options(), f.approve)
}

func (f *AutoCompleteForm) Init() tea.Cmd       { return f.form.Init() }
func (f *AutoCompleteForm) Title() string       { return fmt.Sprintf("Auto-complete !%d", f.pr.ID) }
func (f *AutoCompleteForm) Capturing() bool     { return true }
func (f *AutoCompleteForm) Help() []key.Binding { return []key.Binding{f.yes, keyCancel} }

func (f *AutoCompleteForm) SetSize(w, h int) {
	f.width = w
	f.form = f.form.WithWidth(max(20, w-2)).WithHeight(max(5, h-len(f.headerLines())-1))
}

func (f *AutoCompleteForm) headerLines() []string {
	lines := strings.Split(ui.Bold.Render(pullRequestSummary(f.pr)), "\n")
	if f.note != "" {
		lines = append(lines, styleLines(ui.Muted, clampLines(f.note, f.width, 2))...)
	}
	return lines
}

func (f *AutoCompleteForm) View() string {
	return strings.Join(f.headerLines(), "\n") + "\n\n" + f.form.View()
}

func (f *AutoCompleteForm) Update(msg tea.Msg) (Screen, tea.Cmd) {
	if km, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(km, keyCancel):
			return f, tea.Sequence(Pop(), Status("cancelled"))
		case key.Matches(km, f.yes):
			return f, tea.Sequence(Pop(), f.submit())
		}
	}
	m, cmd := f.form.Update(msg)
	if hf, ok := m.(*huh.Form); ok {
		f.form = hf
	}
	return f, cmd
}
