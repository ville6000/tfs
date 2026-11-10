package tui

import (
	"context"
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
	act := &Actions{
		PreparePR: func(context.Context) (PullRequestPrep, error) {
			return PullRequestPrep{Repository: "Repo", Commits: "- Add x\n- Fix y", Warning: "not linking work item #9",
				Draft: domain.PullRequestDraft{Project: "P", RepositoryID: "rid", SourceBranch: "feature/9-x",
					TargetBranch: "main", Title: "Add x", WorkItemIDs: []int{9}}}, nil
		},
		CreatePR: func(_ context.Context, d domain.PullRequestDraft) (domain.PullRequest, error) {
			created = d
			return domain.PullRequest{ID: 77, URL: "https://h/pr/77"}, nil
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
	for _, want := range []string{"feature/9-x", "Repo", "not linking work item #9", "Add x", "#9", "2 commit(s) not on main"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in:\n%s", want, view)
		}
	}

	// Values edited in the form become the draft.
	form.title, form.desc, form.target, form.workItems, form.draft = " New title ", "Body", " develop ", "#9, 12", true
	var status StatusMsg
	var announced, reloaded bool
	for _, m := range collect(form.form.SubmitCmd) {
		switch m := m.(type) {
		case StatusMsg:
			status = m
		case pullRequestCreatedMsg:
			announced = true
			_, reload := l.Update(m)
			for _, m := range collect(reload) {
				if _, ok := m.(pullRequestsLoadedMsg); ok {
					reloaded = true
				}
			}
		}
	}
	want := domain.PullRequestDraft{Project: "P", RepositoryID: "rid", SourceBranch: "feature/9-x", TargetBranch: "develop",
		Title: "New title", Description: "Body", IsDraft: true, WorkItemIDs: []int{9, 12}}
	if !reflect.DeepEqual(created, want) {
		t.Errorf("draft:\n got %+v\nwant %+v", created, want)
	}
	// The commits are added only when asked for, after the description.
	form.commits = true
	collect(form.form.SubmitCmd)
	if created.Description != "Body\n\n- Add x\n- Fix y" {
		t.Errorf("with commits: %q", created.Description)
	}
	form.desc = " "
	collect(form.form.SubmitCmd)
	if created.Description != "- Add x\n- Fix y" {
		t.Errorf("only commits: %q", created.Description)
	}
	if status.Err || !strings.Contains(status.Text, "!77") || !announced || !reloaded {
		t.Errorf("status %+v announced %v reloaded %v", status, announced, reloaded)
	}
}
