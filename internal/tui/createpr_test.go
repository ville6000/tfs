package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ville6000/tfs/internal/domain"
)

// collect runs a command tree and returns its leaf messages.
func collect(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	switch msg := run(cmd).(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, collect(c)...)
		}
	case CmdMsg:
		out = append(out, collect(msg.Cmd)...)
	case busyDoneMsg:
		out = append(out, collect(func() tea.Msg { return msg.msg })...)
	default:
		if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
			for i := range v.Len() {
				out = append(out, collect(v.Index(i).Interface().(tea.Cmd))...)
			}
			break
		}
		out = append(out, msg)
	}
	return out
}

func TestNewPullRequestFromList(t *testing.T) {
	var created domain.PullRequestDraft
	var addedCommits bool
	fail := errors.New("git push: rejected by the server")
	var createErr error
	act := &Actions{
		PreparePR: func(context.Context) (PullRequestPrep, error) {
			return PullRequestPrep{Repository: "Repo", Commits: 2, Warning: "not linking work item #9:\nGET failed\n\n(401)",
				Draft: domain.PullRequestDraft{Project: "P", RepositoryID: "rid", SourceBranch: "feature/9-x",
					TargetBranch: "main", Title: "Add x", WorkItemIDs: []int{9}},
				Create: func(_ context.Context, d domain.PullRequestDraft, addCommits bool) (domain.PullRequest, string, error) {
					created, addedCommits = d, addCommits
					if createErr != nil {
						return domain.PullRequest{}, "", createErr
					}
					return domain.PullRequest{ID: 77, URL: "https://h/pr/77"}, "", nil
				}}, nil
		},
	}
	l := NewPullRequestList("prs", act, func(context.Context) ([]domain.PullRequest, error) { return nil, nil })
	l.SetSize(100, 20)
	if !strings.Contains(helpText(l.Help()), "n new PR") {
		t.Error("help should mention n")
	}

	_, cmd := l.Update(press("n"))
	var form *CreatePullRequest
	for _, m := range collect(cmd) {
		if p, ok := m.(PushMsg); ok {
			form, _ = p.Screen.(*CreatePullRequest)
		}
	}
	if form == nil {
		t.Fatal("n should open the create form")
	}
	form.SetSize(100, 30)
	run(form.Init())
	view := ansi.Strip(form.View())
	for _, want := range []string{"feature/9-x", "Repo", "Note: not linking work item #9: GET failed (401)", "Add x", "#9", "2 commit(s) not on main"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}

	// submit sends the form's submission through the form, as the App does, and returns the
	// messages the create produces.
	submit := func() []tea.Msg {
		t.Helper()
		_, cmd := form.Update(form.form.SubmitCmd())
		if !form.submitting || !strings.Contains(ansi.Strip(form.View()), "Creating the pull request") {
			t.Error("the form should show it is creating the pull request")
		}
		if _, c := form.Update(press("esc")); c != nil {
			t.Error("keys should be ignored while creating")
		}
		return collect(cmd)
	}

	// A failed create keeps the form open with what was entered and the error.
	form.title, form.desc, form.target, form.workItems, form.draft = " New title ", "Body", " refs/heads/develop ", "#9, 12", true
	createErr = fail
	for _, m := range submit() {
		if _, c := form.Update(m); c != nil {
			run(c)
		}
	}
	view = ansi.Strip(form.View())
	if form.submitting || !strings.Contains(view, "Could not create the pull request: git push: rejected") || !strings.Contains(view, "New title") {
		t.Errorf("after a failure:\n%s", view)
	}
	want := domain.PullRequestDraft{Project: "P", RepositoryID: "rid", SourceBranch: "feature/9-x", TargetBranch: "develop",
		Title: "New title", Description: "Body", IsDraft: true, WorkItemIDs: []int{9, 12}}
	if !reflect.DeepEqual(created, want) || addedCommits {
		t.Errorf("draft:\n got %+v (commits %v)\nwant %+v", created, addedCommits, want)
	}
	if !strings.Contains(view, "The commits not on develop") {
		t.Errorf("the commit toggle should follow the target:\n%s", view)
	}

	// Success closes the form and reloads the list.
	createErr = nil
	form.commits = true
	var status StatusMsg
	var popped, reloaded bool
	for _, m := range submit() {
		switch m := m.(type) {
		case StatusMsg:
			status = m
		case pullRequestCreatedMsg:
			if _, c := form.Update(m); c != nil {
				_, popped = c().(PopMsg)
			}
			_, reload := l.Update(m)
			for _, m := range collect(reload) {
				if _, ok := m.(pullRequestsLoadedMsg); ok {
					reloaded = true
				}
			}
		}
	}
	if !addedCommits {
		t.Error("the commits should be asked for")
	}
	if status.Err || !strings.Contains(status.Text, "!77") || !popped || !reloaded {
		t.Errorf("status %+v popped %v reloaded %v", status, popped, reloaded)
	}
}

// Yes/No sit at the left edge like the other fields, not centered under a long description.
func TestCreatePullRequestButtonsAlignLeft(t *testing.T) {
	prep := PullRequestPrep{Repository: "Repo", Commits: 2,
		Draft: domain.PullRequestDraft{SourceBranch: "feature/9-x", TargetBranch: "main", Title: "Add x"}}
	form := NewCreatePullRequest(&Actions{}, prep)
	form.SetSize(100, 60)
	run(form.Init())
	var rows int
	for _, line := range strings.Split(ansi.Strip(form.View()), "\n") {
		if strings.Contains(line, "Yes") && strings.Contains(line, "No") {
			rows++
			if indent := len(line) - len(strings.TrimLeft(line, " ┃│")); indent > 4 {
				t.Errorf("buttons indented by %d: %q", indent, line)
			}
		}
	}
	if rows != 2 {
		t.Fatalf("want 2 button rows, got %d", rows)
	}
}
