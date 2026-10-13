package domain

import (
	"strings"
	"testing"
)

func TestResolveSlug(t *testing.T) {
	cases := map[string]string{
		"user-story":  "User Story",
		"in_progress": "In Progress",
		"bug":         "Bug",
		"User Story":  "User Story",
		"Bug":         "Bug",
		"  task  ":    "Task",
		"":            "",
		"to-do":       "To Do",
	}
	for in, want := range cases {
		if got := ResolveSlug(in); got != want {
			t.Errorf("ResolveSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveSlugsDedupes(t *testing.T) {
	got := ResolveSlugs([]string{"bug", "Bug", " ", "task"})
	if strings.Join(got, ",") != "Bug,Task" {
		t.Fatalf("got %v", got)
	}
}

func TestScopeFromFlags(t *testing.T) {
	if _, err := ScopeFromFlags(true, true, false, nil, nil); err == nil {
		t.Error("expected conflict error")
	}
	if _, err := ScopeFromFlags(false, true, false, []string{"New"}, nil); err == nil {
		t.Error("expected combination error")
	}
	if s, _ := ScopeFromFlags(false, false, true, nil, nil); s != ScopeResolved {
		t.Errorf("got %v", s)
	}
}

func TestResolveStateFilters(t *testing.T) {
	inc, exc := ResolveStateFilters(ScopeDefault, nil, nil, true)
	if len(inc) != 0 || strings.Join(exc, ",") != "Closed,Removed" {
		t.Errorf("default: %v %v", inc, exc)
	}
	inc, exc = ResolveStateFilters(ScopeDefault, []string{"active"}, nil, true)
	if strings.Join(inc, ",") != "Active" || len(exc) != 0 {
		t.Errorf("explicit: %v %v", inc, exc)
	}
	inc, exc = ResolveStateFilters(ScopeDefault, nil, nil, false)
	if len(inc) != 0 || len(exc) != 0 {
		t.Errorf("no default: %v %v", inc, exc)
	}
	inc, exc = ResolveStateFilters(ScopeOpen, nil, nil, true)
	if len(inc) != 0 || len(exc) != len(NotOpenStates) {
		t.Errorf("open: %v %v", inc, exc)
	}
	inc, _ = ResolveStateFilters(ScopeResolved, nil, nil, true)
	if len(inc) != len(ResolvedStates) {
		t.Errorf("resolved: %v", inc)
	}
}

func TestFilter(t *testing.T) {
	f := NewFilter([]string{"user-story"}, nil, nil, []string{"closed"})
	if !f.Matches(WorkItem{Type: "User Story", State: "Active"}) {
		t.Error("should match")
	}
	if f.Matches(WorkItem{Type: "User Story", State: "Closed"}) {
		t.Error("closed should be excluded")
	}
	if f.Matches(WorkItem{Type: "Bug", State: "Active"}) {
		t.Error("bug not in allow-list")
	}
}

func TestBuildForest(t *testing.T) {
	items := []WorkItem{
		{ID: 1, Type: "Epic", State: "Active"},
		{ID: 2, Type: "Feature", State: "Closed", ParentID: 1},
		{ID: 3, Type: "Task", State: "Active", ParentID: 2},
		{ID: 4, Type: "Task", State: "Closed", ParentID: 1},
		{ID: 5, Type: "Bug", State: "Active"},
		{ID: 6, Type: "Task", State: "Active", ParentID: 999}, // parent not fetched
	}
	tree := BuildForest(items, NewFilter(nil, nil, nil, []string{"Closed"}))
	if len(tree.Roots) != 1 || tree.Roots[0].Item.ID != 1 {
		t.Fatalf("roots: %+v", tree.Roots)
	}
	epic := tree.Roots[0]
	if len(epic.Children) != 1 || epic.Children[0].Item.ID != 2 {
		t.Fatalf("closed task 4 should be pruned, feature 2 kept: %+v", epic.Children)
	}
	if epic.Children[0].Matches {
		t.Error("feature 2 is an unmatched ancestor")
	}
	if len(tree.Orphans) != 2 || tree.Orphans[0].Item.ID != 5 || tree.Orphans[1].Item.ID != 6 {
		t.Fatalf("orphans: %+v", tree.Orphans)
	}
}

func TestBuildForestCycle(t *testing.T) {
	items := []WorkItem{{ID: 1, Type: "Epic", ParentID: 2}, {ID: 2, Type: "Epic", ParentID: 1}}
	tree := BuildForest(items, NewFilter(nil, nil, nil, nil))
	if !tree.IsEmpty() {
		t.Errorf("cycle without a top-level item yields nothing: %+v", tree)
	}
}

func TestBuildChain(t *testing.T) {
	chain := []WorkItem{{ID: 3, Type: "Task"}, {ID: 2, Type: "Feature"}, {ID: 1, Type: "Epic"}}
	tree := BuildChain(chain, NewFilter(nil, nil, nil, nil))
	if tree.Roots[0].Item.ID != 1 || tree.Roots[0].Children[0].Children[0].Item.ID != 3 {
		t.Fatalf("chain: %+v", tree.Roots[0])
	}
}

func TestTitleSlugAndBranch(t *testing.T) {
	cases := map[string]string{
		"[UI][Web] Fix login: button!": "fix-login-button",
		":  Hello   World ":            "hello-world",
		"Ääkköset ok":                  "ääkköset-ok",
		"[only tag]":                   "",
	}
	for in, want := range cases {
		if got := TitleSlug(in); got != want {
			t.Errorf("TitleSlug(%q) = %q, want %q", in, got, want)
		}
	}
	w := WorkItem{ID: 42, Type: "User Story", Title: "Add thing"}
	if got := BranchName("", w); got != "feature/42-add-thing" {
		t.Errorf("default: %s", got)
	}
	if got := BranchName("{type}/{id}", w); got != "user-story/42" {
		t.Errorf("custom: %s", got)
	}
}

func TestComputeApproval(t *testing.T) {
	cases := []struct {
		name string
		in   []Reviewer
		want Approval
	}{
		{"none", nil, ApprovalPending},
		{"reject wins", []Reviewer{{Vote: 10}, {Vote: -10}}, ApprovalRejected},
		{"waiting", []Reviewer{{Vote: 10}, {Vote: -5}}, ApprovalWaitingForAuthor},
		{"required all approve", []Reviewer{{Vote: 10, IsRequired: true}, {Vote: 0}}, ApprovalApproved},
		{"required one missing", []Reviewer{{Vote: 10, IsRequired: true}, {Vote: 0, IsRequired: true}}, ApprovalPending},
		{"required suggestions", []Reviewer{{Vote: 5, IsRequired: true}, {Vote: 10, IsRequired: true}}, ApprovalApprovedWithSuggestions},
		{"optional max", []Reviewer{{Vote: 0}, {Vote: 5}}, ApprovalApprovedWithSuggestions},
	}
	for _, c := range cases {
		if got := ComputeApproval(c.in); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestParseRemote(t *testing.T) {
	cases := map[string]RepositoryRef{
		"https://ado.example.com/DefaultCollection/My%20Proj/_git/Repo":     {"My Proj", "Repo"},
		"https://ado.example.com/tfs/Col/Proj/_git/Repo.git":                {"Proj", "Repo"},
		"ssh://ado.example.com:22/DefaultCollection/Proj/_git/Repo":         {"Proj", "Repo"},
		"git@ado.example.com:DefaultCollection/Proj/_git/Repo":              {"Proj", "Repo"},
		"https://user@ado.example.com/DefaultCollection/Proj/_git/Repo?x=1": {"Proj", "Repo"},
	}
	for in, want := range cases {
		got, err := ParseRemote(in)
		if err != nil || got != want {
			t.Errorf("ParseRemote(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "https://github.com/a/b.git", "https://h/_git/Repo"} {
		if _, err := ParseRemote(bad); err == nil {
			t.Errorf("ParseRemote(%q) should fail", bad)
		}
	}
}

func TestPullRequestCreateURL(t *testing.T) {
	got := PullRequestCreateURL("https://h/Col/", RepositoryRef{"My Proj", "Repo"}, "feature/a b", "main", "abc")
	want := "https://h/Col/My%20Proj/_git/Repo/pullrequestcreate?sourceRef=feature/a%20b&targetRef=main&sourceRepositoryId=abc&targetRepositoryId=abc"
	if got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestDraftValidate(t *testing.T) {
	d := WorkItemDraft{Project: "P", Type: "User Story", Title: "x"}
	if err := d.Validate(nil); err != nil {
		t.Errorf("no rule configured: %v", err)
	}
	if err := d.Validate([]string{"user-story"}); err == nil {
		t.Error("rule configured: expected error")
	}
	d.ParentID = 1
	if err := d.Validate([]string{"user-story"}); err != nil {
		t.Errorf("with parent: %v", err)
	}
}

func TestPullRequestQueryValidate(t *testing.T) {
	q := PullRequestQuery{Project: "P", Status: "active", Top: 10}
	if err := q.Validate(); err != nil || q.Status != "Active" {
		t.Errorf("got %v %q", err, q.Status)
	}
	q.Status = "bogus"
	if err := q.Validate(); err == nil {
		t.Error("expected status error")
	}
}
